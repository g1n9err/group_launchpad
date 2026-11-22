package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"launchpad/internal/core"
)

// Slack replay window: Slack-signed requests older/newer than this are
// rejected (Slack's own recommendation is five minutes).
const SlackEventMaxAge = 300 * time.Second

// Clock is injectable time (deterministic replay tests).
type Clock func() time.Time

// SlackEventsService handles the Slack Events API webhook: strict signature
// verification before ANY parsing, replay protection, the url_verification
// handshake, durable dedup, and member_joined_channel resolution. Every
// verified event is acknowledged quickly and persisted durably — nothing is
// lost after ack.
type SlackEventsService struct {
	verifier     SlackEventVerifier
	repo         SlackEventRepository
	linker       *SlackIdentityLinker // nil ⇒ linking disabled (legacy behavior)
	teamResolver SlackTeamGatewayResolver
	identityRepo SlackIdentityRepository
	now          Clock
	configured   bool
}

// NewSlackEventsService builds the webhook service. configured requires the
// signing secret to be present. linker may be nil.
func NewSlackEventsService(verifier SlackEventVerifier, repo SlackEventRepository, linker *SlackIdentityLinker, now Clock, configured bool) *SlackEventsService {
	return &SlackEventsService{verifier: verifier, repo: repo, linker: linker, now: now, configured: configured}
}

// Configured reports whether the webhook can operate.
func (s *SlackEventsService) Configured() bool { return s.configured }

func (s *SlackEventsService) SetTeamGatewayResolver(resolver SlackTeamGatewayResolver, identities SlackIdentityRepository) {
	s.teamResolver, s.identityRepo = resolver, identities
}

// slackEnvelope is the Events API outer payload.
type slackEnvelope struct {
	Type      string          `json:"type"`
	EventID   string          `json:"event_id"`
	TeamID    string          `json:"team_id"`
	Challenge string          `json:"challenge"`
	Event     json.RawMessage `json:"event"`
}

// slackEvent is the inner event object (fields relevant to us).
type slackEvent struct {
	Type      string `json:"type"`
	User      string `json:"user"`
	Channel   string `json:"channel"`
	ChannelID string `json:"channel_id"`
}

// Handle processes one webhook request. Error returns are sanitized
// AppErrors (401 unauthorized, 400 malformed) — never raw parsing output.
func (s *SlackEventsService) Handle(ctx context.Context, req SlackEventRequest) (SlackEventAck, error) {
	if !s.configured {
		return SlackEventAck{}, &core.AppError{Code: core.CodeNotReady, Message: "slack events are not configured on the server"}
	}

	// ── 1. Signature verification BEFORE any parsing (rule: never process
	// unverified bytes). Missing/malformed/invalid → 401.
	if req.Timestamp == "" || req.Signature == "" {
		return SlackEventAck{}, unauthorized("missing signature headers")
	}
	if _, err := strconv.ParseInt(req.Timestamp, 10, 64); err != nil {
		return SlackEventAck{}, unauthorized("malformed signature timestamp")
	}
	if !s.verifier.Verify(req.Timestamp, req.RawBody, req.Signature) {
		return SlackEventAck{}, unauthorized("signature verification failed")
	}

	// ── 2. Replay protection (clock injectable for tests).
	ts, _ := strconv.ParseInt(req.Timestamp, 10, 64)
	age := s.now().Sub(time.Unix(ts, 0))
	if age > SlackEventMaxAge || age < -SlackEventMaxAge {
		return SlackEventAck{}, unauthorized("request timestamp outside the replay window")
	}

	// ── 3. Parse the envelope (signature is already proven over these bytes).
	var env slackEnvelope
	if err := json.Unmarshal([]byte(req.RawBody), &env); err != nil {
		return SlackEventAck{}, &core.AppError{Code: core.CodeValidation, Message: "malformed event payload"}
	}

	// ── 4. URL verification handshake: verified above, never persisted.
	if env.Type == "url_verification" {
		return SlackEventAck{Kind: "challenge", Challenge: env.Challenge}, nil
	}

	// ── 5. Dedup: atomic insert-on-conflict on the event_id primary key.
	if env.EventID == "" {
		return SlackEventAck{}, &core.AppError{Code: core.CodeValidation, Message: "event missing event_id"}
	}
	var inner slackEvent
	_ = json.Unmarshal(env.Event, &inner) //nolint:errcheck — unknown types still dedup+record
	ev := RecordedSlackEvent{
		EventID:   env.EventID,
		EventType: inner.Type,
		TeamID:    env.TeamID,
		UserID:    inner.User,
		ChannelID: inner.Channel,
		Payload:   []byte(req.RawBody),
	}
	inserted, err := s.repo.InsertSlackEvent(ctx, ev)
	if err != nil {
		return SlackEventAck{}, fmt.Errorf("slack events: record: %w", err)
	}
	if !inserted {
		return SlackEventAck{Kind: "duplicate"}, nil // already seen — ack, never reprocess
	}

	// ── 6. Process by type.
	if ev.EventType != "member_joined_channel" {
		_ = s.repo.CompleteSlackEvent(ctx, ev.EventID, EventStatusIgnored, "") //nolint:errcheck
		return SlackEventAck{Kind: "ignored"}, nil
	}

	// Bot's own join: record as bot, never a member transition.
	var eventGateway SlackGateway
	var botID string
	if s.teamResolver != nil && ev.TeamID != "" {
		if gateway, resolveErr := s.teamResolver.ForTeam(ctx, ev.TeamID); resolveErr == nil {
			eventGateway = gateway
			if workspace, authErr := gateway.AuthTest(ctx); authErr == nil {
				botID = workspace.BotUserID
			}
		}
	}
	if botID == "" {
		botID, err = s.repo.SlackBotUserID(ctx)
	}
	if err == nil && botID != "" && ev.UserID == botID {
		_ = s.repo.CompleteSlackEvent(ctx, ev.EventID, EventStatusBot, "") //nolint:errcheck
		return SlackEventAck{Kind: "bot"}, nil
	}

	// Resolve channel → group, user → member. No guessing: any miss is
	// durably recorded as unresolved for investigation.
	groupID, ok, err := s.repo.FindGroupIDBySlackChannel(ctx, ev.ChannelID)
	if err != nil {
		return SlackEventAck{}, fmt.Errorf("slack events: resolve channel: %w", err)
	}
	if !ok {
		_ = s.repo.CompleteSlackEvent(ctx, ev.EventID, EventStatusUnresolved, "channel_not_linked") //nolint:errcheck
		return SlackEventAck{Kind: "unresolved"}, nil
	}
	userID, ok, err := s.repo.FindUserIDBySlack(ctx, ev.UserID)
	if err != nil {
		return SlackEventAck{}, fmt.Errorf("slack events: resolve user: %w", err)
	}
	if !ok {
		// Identity linking (Stage 3A): resolve the Slack user by verified
		// email and link — only on an unambiguous match. Reasons are durable
		// codes; the email itself is never persisted in the ledger.
		linker := s.linker
		if eventGateway != nil {
			identityRepo := s.identityRepo
			if linker != nil {
				identityRepo = linker.repo
			}
			if identityRepo != nil {
				linker = NewSlackIdentityLinker(eventGateway, identityRepo)
			}
		}
		if linker == nil {
			_ = s.repo.CompleteSlackEvent(ctx, ev.EventID, EventStatusUnresolved, "user_not_linked") //nolint:errcheck
			return SlackEventAck{Kind: "unresolved"}, nil
		}
		linkedID, reason, linkErr := linker.Link(ctx, ev.UserID)
		if linkErr != nil {
			return SlackEventAck{}, fmt.Errorf("slack events: identity link: %w", linkErr)
		}
		if reason != "" {
			_ = s.repo.CompleteSlackEvent(ctx, ev.EventID, EventStatusUnresolved, reason) //nolint:errcheck
			return SlackEventAck{Kind: "unresolved"}, nil
		}
		userID = linkedID
	}
	memberID, _, found, err := s.repo.FindGroupMember(ctx, groupID, userID)
	if err != nil {
		return SlackEventAck{}, fmt.Errorf("slack events: resolve member: %w", err)
	}
	if !found {
		_ = s.repo.CompleteSlackEvent(ctx, ev.EventID, EventStatusUnresolved, "member_not_in_group") //nolint:errcheck
		return SlackEventAck{Kind: "unresolved"}, nil
	}

	if err := s.repo.MarkGroupMemberJoinedSlack(ctx, memberID); err != nil {
		return SlackEventAck{}, fmt.Errorf("slack events: mark joined: %w", err)
	}
	if err := s.repo.CompleteSlackEvent(ctx, ev.EventID, EventStatusProcessed, ""); err != nil {
		return SlackEventAck{}, fmt.Errorf("slack events: complete: %w", err)
	}
	return SlackEventAck{Kind: "processed"}, nil
}

func unauthorized(message string) error {
	return &core.AppError{Code: core.CodeUnauthorized, Message: message}
}
