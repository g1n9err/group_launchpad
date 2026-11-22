package integrations

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

// Operation labels recorded in external_operation_logs (PRD 8.7).
const (
	OpSlackListChannels = "slack.conversations.list"
	OpSlackCreateChan   = "slack.conversations.create"
	OpSlackLookupUser   = "slack.users.lookupByEmail"
	OpSlackInvite       = "slack.conversations.invite"
	OpSlackPostMessage  = "slack.chat.postMessage"
)

// SlackJoinRecorder reconciles onboarding state from provisioning results:
// conversations.invite ADDS a workspace member to a channel immediately, so
// a successful invite (or "already in channel") IS the Slack join — waiting
// for a member_joined_channel event would leave members "pending" forever
// when the Events webhook is unreachable.
type SlackJoinRecorder interface {
	FindUserIDsByEmail(ctx context.Context, normalizedEmail string) ([]uuid.UUID, error)
	FindGroupMember(ctx context.Context, groupID, userID uuid.UUID) (memberID uuid.UUID, onboardingStatus string, found bool, err error)
	MarkGroupMemberJoinedSlack(ctx context.Context, memberID uuid.UUID) error
}

// ProvisionChannelStatus values.
const (
	ChannelCreated = "created"
	ChannelReused  = "reused"
	ChannelFailed  = "failed"
	WelcomePosted  = "posted"
	WelcomeSkipped = "skipped"
	WelcomeFailed  = "failed"
	InviteFailed   = "failed"
)

// ChannelNameMaxLength is Slack's hard limit for channel names.
const ChannelNameMaxLength = 80

var slugCollapse = regexp.MustCompile(`[^a-z0-9]+`)

// ChannelName derives the PRD §4.2 convention: group "Python Backend 13" +
// suffix "general" → "python-backend-13-general".
func ChannelName(groupName, suffix string) (string, error) {
	slug := slugCollapse.ReplaceAllString(strings.ToLower(strings.TrimSpace(groupName)), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "", core.Validation("group name yields an empty channel slug", map[string]string{"name": "must contain letters or digits"})
	}
	name := slug + "-" + suffix
	if len(name) > ChannelNameMaxLength {
		// Preserve the suffix; trim the slug.
		max := ChannelNameMaxLength - len(suffix) - 1
		if max < 1 {
			return "", core.Validation("channel name too long", map[string]string{"name": "group name too long"})
		}
		name = slug[:max] + "-" + suffix
	}
	return name, nil
}

// ProvisionInput carries everything needed to provision a group's Slack
// presence. InviteEmails come from the launch workflow — staff and student
// addresses are configured or stored data, never hardcoded in the adapter.
type ProvisionInput struct {
	GroupID         uuid.UUID
	GroupName       string
	CourseName      string
	StartDate       string // ISO date, display only
	InviteEmails    []string
	WelcomeTemplate string // optional; default template used when empty
}

// ChannelResult reports one channel's provisioning outcome.
type ChannelResult struct {
	Name      string `json:"name"`
	ChannelID string `json:"channelId"`
	Status    string `json:"status"` // created | reused | failed
	Error     string `json:"error,omitempty"`
}

// InviteResult reports one (email × channel) invitation outcome.
type InviteResult struct {
	Email       string `json:"email"`
	ChannelName string `json:"channelName"`
	Status      string `json:"status"` // invited | already_member | failed
	Error       string `json:"error,omitempty"`
}

// WelcomeResult reports one channel's welcome-message outcome.
type WelcomeResult struct {
	ChannelName string `json:"channelName"`
	Status      string `json:"status"` // posted | skipped | failed
	Error       string `json:"error,omitempty"`
}

// ProvisionResult is the complete outcome of one provisioning run.
type ProvisionResult struct {
	Channels []ChannelResult `json:"channels"`
	Invites  []InviteResult  `json:"invites"`
	Welcomes []WelcomeResult `json:"welcomeMessages"`
}

// SlackChannelsService provisions a group's Slack channels, invitations, and
// welcome messages idempotently (PRD §4.2, §8): every run reuses existing
// resources, records per-item failures, and is safe to retry.
type SlackChannelsService struct {
	gateway    SlackGateway
	resources  SlackResourceRepository
	operations ExternalOperationRecorder
	// Joins, when set, marks roster members as joined_slack for successful
	// invitations (see SlackJoinRecorder). Optional: nil disables write-back.
	Joins      SlackJoinRecorder
	configured bool
	resolver   SlackGroupGatewayResolver
}

// SlackGroupGatewayResolver selects the bot installation assigned to a group.
type SlackGroupGatewayResolver interface {
	ForGroup(context.Context, uuid.UUID) (SlackGateway, error)
}

// ExternalOperationRecorder persists external operation logs.
type ExternalOperationRecorder interface {
	RecordExternalOperation(ctx context.Context, op ExternalOperation) error
}

// NewSlackChannelsService builds the provisioning service.
func NewSlackChannelsService(gateway SlackGateway, resources SlackResourceRepository, recorder ExternalOperationRecorder, configured bool) *SlackChannelsService {
	return &SlackChannelsService{gateway: gateway, resources: resources, operations: recorder, configured: configured}
}

// Configured reports whether Slack channel operations are possible.
func (s *SlackChannelsService) Configured() bool { return s.configured }

func (s *SlackChannelsService) SetGatewayResolver(resolver SlackGroupGatewayResolver) {
	s.resolver = resolver
	if resolver != nil {
		s.configured = true
	}
}

func (s *SlackChannelsService) gatewayFor(ctx context.Context, groupID uuid.UUID) (SlackGateway, error) {
	if s.resolver != nil {
		return s.resolver.ForGroup(ctx, groupID)
	}
	if s.gateway == nil {
		return nil, &core.AppError{Code: core.CodeNotReady, Message: "Slack is not configured"}
	}
	return s.gateway, nil
}

func (s *SlackChannelsService) record(ctx context.Context, groupID uuid.UUID, operation, errCategory string, started time.Time) {
	_ = s.operations.RecordExternalOperation(ctx, ExternalOperation{ //nolint:errcheck — op-log failures never mask results
		GroupID: &groupID, Provider: ProviderSlack, Operation: operation, Error: errCategory, DurationMS: time.Since(started).Milliseconds(),
	})
}

// Provision ensures the four PRD channels exist for the group, invites the
// given members to them, and posts one welcome message per channel — all
// idempotently. Per-item failures are reported, never dropped.
func (s *SlackChannelsService) Provision(ctx context.Context, in ProvisionInput) (ProvisionResult, error) {
	if !s.configured {
		return ProvisionResult{}, &core.AppError{Code: core.CodeNotReady, Message: "slack is not configured on the server"}
	}
	gateway, err := s.gatewayFor(ctx, in.GroupID)
	if err != nil {
		return ProvisionResult{}, err
	}

	result := ProvisionResult{
		Channels: make([]ChannelResult, 0, len(DefaultChannelSuffixes)),
		Invites:  []InviteResult{},
		Welcomes: []WelcomeResult{},
	}

	// ── Phase 1: resolve channel names → registry → Slack ─────────────
	type resolved struct {
		suffix    string
		name      string
		channelID string
	}
	pending := make([]resolved, 0, len(DefaultChannelSuffixes))
	var missing []string

	for _, suffix := range DefaultChannelSuffixes {
		name, err := ChannelName(in.GroupName, suffix)
		if err != nil {
			result.Channels = append(result.Channels, ChannelResult{Name: suffix, Status: ChannelFailed, Error: "invalid group name for channel slug"})
			continue
		}
		// Fast path: registry already knows this group's channel.
		if res, ok, _ := s.resources.GetResource(ctx, in.GroupID, ProviderSlack, ResourceTypeChannel, name); ok && res.Status == ResourceStatusActive && res.ExternalID != "" {
			result.Channels = append(result.Channels, ChannelResult{Name: name, ChannelID: res.ExternalID, Status: ChannelReused})
			pending = append(pending, resolved{suffix: suffix, name: name, channelID: res.ExternalID})
			continue
		}
		missing = append(missing, name)
		pending = append(pending, resolved{suffix: suffix, name: name})
	}

	// One paginated lookup covers every missing channel (and pre-existing
	// same-name channels anywhere in the workspace).
	var existing map[string]string
	if len(missing) > 0 {
		started := time.Now()
		var err error
		existing, err = gateway.LookupChannelIDs(ctx, missing)
		s.record(ctx, in.GroupID, OpSlackListChannels, errorCategory(err), started)
		if err != nil {
			for _, p := range pending {
				if p.channelID == "" {
					result.Channels = append(result.Channels, ChannelResult{Name: p.name, Status: ChannelFailed, Error: "lookup failed"})
				}
			}
			return result, nil // transient total failure: report and stop
		}
	}

	for i, p := range pending {
		if p.channelID != "" {
			continue // already resolved via registry
		}
		if id, ok := existing[p.name]; ok {
			// Reuse a pre-existing workspace channel with the same name.
			s.claimChannel(ctx, in.GroupID, p.name, id)
			pending[i].channelID = id
			result.Channels = append(result.Channels, ChannelResult{Name: p.name, ChannelID: id, Status: ChannelReused})
			continue
		}
		started := time.Now()
		id, err := gateway.CreateChannel(ctx, p.name)
		s.record(ctx, in.GroupID, OpSlackCreateChan, errorCategory(err), started)
		switch {
		case err == nil:
			s.claimChannel(ctx, in.GroupID, p.name, id)
			pending[i].channelID = id
			result.Channels = append(result.Channels, ChannelResult{Name: p.name, ChannelID: id, Status: ChannelCreated})
		case err == ErrSlackChannelNameTaken: //nolint:errorlint — sentinel compared by identity
			// A concurrent execution created it between lookup and create.
			retry, lookupErr := gateway.LookupChannelIDs(ctx, []string{p.name})
			if lookupErr == nil && retry[p.name] != "" {
				s.claimChannel(ctx, in.GroupID, p.name, retry[p.name])
				pending[i].channelID = retry[p.name]
				result.Channels = append(result.Channels, ChannelResult{Name: p.name, ChannelID: retry[p.name], Status: ChannelReused})
			} else {
				result.Channels = append(result.Channels, ChannelResult{Name: p.name, Status: ChannelFailed, Error: "name taken but lookup failed"})
			}
		default:
			result.Channels = append(result.Channels, ChannelResult{Name: p.name, Status: ChannelFailed, Error: "create failed"})
		}
	}

	// ── Phase 2: welcome messages (one per provisioned channel, once) ──
	for _, p := range pending {
		if p.channelID == "" {
			continue
		}
		result.Welcomes = append(result.Welcomes, s.ensureWelcome(ctx, gateway, in, p.name, p.channelID))
	}

	result.Invites = s.inviteToChannels(ctx, gateway, in.GroupID, in.InviteEmails, result.Channels)
	return result, nil
}

// InviteExisting invites members only to registered channels for this group.
func (s *SlackChannelsService) InviteExisting(ctx context.Context, groupID uuid.UUID, groupName string, emails []string) (ProvisionResult, error) {
	if !s.configured {
		return ProvisionResult{}, &core.AppError{Code: core.CodeNotReady, Message: "Slack is not configured"}
	}
	gateway, err := s.gatewayFor(ctx, groupID)
	if err != nil {
		return ProvisionResult{}, err
	}
	result := ProvisionResult{Channels: []ChannelResult{}, Welcomes: []WelcomeResult{}}
	for _, suffix := range DefaultChannelSuffixes {
		name, err := ChannelName(groupName, suffix)
		if err != nil {
			return ProvisionResult{}, err
		}
		resource, found, err := s.resources.GetResource(ctx, groupID, ProviderSlack, ResourceTypeChannel, name)
		if err != nil {
			return ProvisionResult{}, err
		}
		channel := ChannelResult{Name: name, Status: ChannelFailed, Error: "channel_not_provisioned"}
		if found && resource.Status == ResourceStatusActive && resource.ExternalID != "" {
			channel = ChannelResult{Name: name, ChannelID: resource.ExternalID, Status: ChannelReused}
		}
		result.Channels = append(result.Channels, channel)
	}
	result.Invites = s.inviteToChannels(ctx, gateway, groupID, emails, result.Channels)
	return result, nil
}

func (s *SlackChannelsService) RemoveExisting(ctx context.Context, groupID uuid.UUID, groupName string, emails []string) error {
	if !s.configured {
		return &core.AppError{Code: core.CodeNotReady, Message: "Slack is not configured"}
	}
	gateway, err := s.gatewayFor(ctx, groupID)
	if err != nil {
		return err
	}
	var failures []error
	for _, suffix := range DefaultChannelSuffixes {
		name, err := ChannelName(groupName, suffix)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		resource, found, err := s.resources.GetResource(ctx, groupID, ProviderSlack, ResourceTypeChannel, name)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !found || resource.Status != ResourceStatusActive || resource.ExternalID == "" {
			continue
		}
		for _, email := range emails {
			userID, err := gateway.LookupUserByEmail(ctx, NormalizeEmail(email))
			if err != nil {
				if errors.Is(err, ErrSlackUserNotFound) {
					continue
				}
				failures = append(failures, err)
				continue
			}
			if err := gateway.RemoveUserFromChannel(ctx, resource.ExternalID, userID); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (s *SlackChannelsService) inviteToChannels(ctx context.Context, gateway SlackGateway, groupID uuid.UUID, emails []string, channels []ChannelResult) []InviteResult {
	invites := []InviteResult{}
	userIDs := make(map[string]string, len(emails))
	lookupErrors := make(map[string]string, len(emails))
	for _, channel := range channels {
		if channel.ChannelID == "" {
			continue
		}
		for _, email := range emails {
			userID, lookedUp := userIDs[email]
			if !lookedUp {
				started := time.Now()
				var err error
				userID, err = gateway.LookupUserByEmail(ctx, email)
				category := errorCategory(err)
				s.record(ctx, groupID, OpSlackLookupUser, category, started)
				userIDs[email] = userID
				lookupErrors[email] = category
			}
			result := InviteResult{Email: email, ChannelName: channel.Name, Status: InviteFailed}
			if userID == "" {
				result.Error = lookupErrors[email]
				if result.Error == "" {
					result.Error = "user_not_found"
				}
			} else {
				started := time.Now()
				outcome, err := gateway.InviteUserToChannel(ctx, channel.ChannelID, userID)
				result.Error = errorCategory(err)
				s.record(ctx, groupID, OpSlackInvite, result.Error, started)
				if err == nil {
					result.Status = string(InviteOutcomeAlreadyMember)
					if outcome == InviteOutcomeInvited {
						result.Status = string(outcome)
					}
					s.markJoined(ctx, groupID, email)
				}
			}
			invites = append(invites, result)
		}
	}
	return invites
}

// markJoined reconciles onboarding for a successfully invited (or
// already-member) email: unambiguously matched roster members are marked
// joined_slack. Best-effort by design — failures never fail provisioning.
func (s *SlackChannelsService) markJoined(ctx context.Context, groupID uuid.UUID, email string) {
	if s.Joins == nil {
		return
	}
	ids, err := s.Joins.FindUserIDsByEmail(ctx, NormalizeEmail(email))
	if err != nil || len(ids) != 1 {
		return // zero or ambiguous matches: the Events flow remains authoritative
	}
	memberID, status, found, err := s.Joins.FindGroupMember(ctx, groupID, ids[0])
	if err != nil || !found {
		return
	}
	if status == "joined_slack" || status == "complete" {
		return
	}
	_ = s.Joins.MarkGroupMemberJoinedSlack(ctx, memberID) //nolint:errcheck — best effort
}

// claimChannel records the channel in the idempotency registry (active).
func (s *SlackChannelsService) claimChannel(ctx context.Context, groupID uuid.UUID, name, channelID string) {
	res := GroupResource{Provider: ProviderSlack, Type: ResourceTypeChannel, Name: name, ExternalID: channelID, Status: ResourceStatusActive}
	inserted, err := s.resources.InsertResource(ctx, groupID, res)
	if err != nil || !inserted {
		_ = s.resources.UpdateResourceResult(ctx, groupID, res) //nolint:errcheck — row exists from a previous run
	}
}

// ensureWelcome posts the welcome message exactly once per (group, channel):
// the registry claim (pending → active) is the dedup mutex; error rows are
// retryable on the next run.
func (s *SlackChannelsService) ensureWelcome(ctx context.Context, gateway SlackGateway, in ProvisionInput, channelName, channelID string) WelcomeResult {
	if res, ok, _ := s.resources.GetResource(ctx, in.GroupID, ProviderSlack, ResourceTypeWelcomeMessage, channelName); ok && res.Status == ResourceStatusActive {
		return WelcomeResult{ChannelName: channelName, Status: WelcomeSkipped}
	}
	claimed, err := s.resources.InsertResource(ctx, in.GroupID, GroupResource{
		Provider: ProviderSlack, Type: ResourceTypeWelcomeMessage, Name: channelName, Status: ResourceStatusPending,
	})
	if err != nil {
		return WelcomeResult{ChannelName: channelName, Status: WelcomeFailed, Error: "registry unavailable"}
	}
	if !claimed {
		// Another execution claimed it concurrently — treat as already posted.
		return WelcomeResult{ChannelName: channelName, Status: WelcomeSkipped}
	}

	started := time.Now()
	ts, err := gateway.PostMessage(ctx, channelID, s.welcomeText(in, channelName))
	s.record(ctx, in.GroupID, OpSlackPostMessage, errorCategory(err), started)
	if err != nil {
		// Mark error so a later run retries (error rows are not skipped).
		_ = s.resources.UpdateResourceResult(ctx, in.GroupID, GroupResource{ //nolint:errcheck
			Provider: ProviderSlack, Type: ResourceTypeWelcomeMessage, Name: channelName, Status: ResourceStatusError,
		})
		return WelcomeResult{ChannelName: channelName, Status: WelcomeFailed, Error: errorCategory(err)}
	}
	_ = s.resources.UpdateResourceResult(ctx, in.GroupID, GroupResource{ //nolint:errcheck
		Provider: ProviderSlack, Type: ResourceTypeWelcomeMessage, Name: channelName, ExternalID: ts, Status: ResourceStatusActive,
	})
	return WelcomeResult{ChannelName: channelName, Status: WelcomePosted}
}

// welcomeText renders the default welcome message (template overridable via
// input; group data comes from the database, never invented).
func (s *SlackChannelsService) welcomeText(in ProvisionInput, channelName string) string {
	if in.WelcomeTemplate != "" {
		return in.WelcomeTemplate
	}
	start := ""
	if in.StartDate != "" {
		start = fmt.Sprintf(" Group starts %s.", in.StartDate)
	}
	return fmt.Sprintf(":wave: Welcome to %s — %s! You're in the #%s channel.%s", in.GroupName, in.CourseName, channelName, start)
}

// errorCategory maps any error to a short, safe op-log category.
func errorCategory(err error) string {
	switch {
	case err == nil:
		return ""
	case err == ErrSlackAuthRejected: //nolint:errorlint — sentinel identity
		return "auth_rejected"
	case err == ErrSlackChannelNameTaken: //nolint:errorlint
		return "name_taken"
	case err == ErrSlackUserNotFound: //nolint:errorlint
		return "user_not_found"
	case err == ErrSlackUnavailable || err == nil: //nolint:errorlint
		return "unreachable"
	default:
		return "api_error"
	}
}
