package integrations

import (
	"context"
	"errors"
	"time"

	"launchpad/internal/core"
)

const (
	// ProviderSlack is the integrations.provider value for the Slack
	// connection row (single-workspace bot token model).
	ProviderSlack = "slack"

	// SlackOperationAuthTest is the operation label recorded for auth.test.
	SlackOperationAuthTest = "slack.auth.test"
)

// SlackAuthService manages the workspace connection state. Slack has no
// browser OAuth flow in this architecture: the bot token is server-side env
// configuration, so "connecting" means verifying it (auth.test) and
// persisting the workspace identity. Credential-gated like the Google
// service: unconfigured ⇒ NOT_READY, nothing simulated.
type SlackAuthService struct {
	repo       SlackRepository
	gateway    SlackGateway
	configured bool
}

// NewSlackAuthService builds the service. gateway/repo may be nil when
// configured=false.
func NewSlackAuthService(repo SlackRepository, gateway SlackGateway, configured bool) *SlackAuthService {
	return &SlackAuthService{repo: repo, gateway: gateway, configured: configured}
}

// Configured reports whether the Slack bot token is present.
func (s *SlackAuthService) Configured() bool { return s.configured }

// CheckConnection verifies the bot token against Slack (auth.test), records
// the operation, and upserts the integration row:
//
//	success            → provider='slack', account_identifier=team_id, status 'connected'
//	auth rejected      → persisted status 'error' (permanent: token invalid/revoked)
//	network failure    → status 'error' reported but NOT persisted (transient)
//
// The operation is idempotent: repeating it re-verifies and re-upserts safely;
// created_at stays at the first successful connection.
func (s *SlackAuthService) CheckConnection(ctx context.Context) (SlackStatus, error) {
	if !s.configured {
		return SlackStatus{}, &core.AppError{Code: core.CodeNotReady, Message: "slack is not configured on the server"}
	}

	start := time.Now()
	workspace, err := s.gateway.AuthTest(ctx)
	durationMS := time.Since(start).Milliseconds()

	if err != nil {
		category := "api_error"
		switch {
		case errors.Is(err, ErrSlackAuthRejected):
			category = "auth_rejected"
		case errors.Is(err, ErrSlackUnavailable):
			category = "unreachable"
		}
		_ = s.repo.RecordExternalOperation(ctx, ExternalOperation{ //nolint:errcheck — op-log failures must not mask the status
			Provider: ProviderSlack, Operation: SlackOperationAuthTest, Error: category, DurationMS: durationMS,
		})
		if errors.Is(err, ErrSlackAuthRejected) {
			if markErr := s.repo.MarkSlackError(ctx); markErr != nil {
				return SlackStatus{}, markErr
			}
		}
		return SlackStatus{Status: StatusError}, nil
	}

	if err := s.repo.RecordExternalOperation(ctx, ExternalOperation{
		Provider: ProviderSlack, Operation: SlackOperationAuthTest, DurationMS: durationMS,
	}); err != nil {
		return SlackStatus{}, err
	}

	persisted, err := s.repo.UpsertSlackConnection(ctx, workspace.TeamID, workspace.BotUserID)
	if err != nil {
		return SlackStatus{}, err
	}
	persisted.TeamName = workspace.TeamName // live metadata — not persisted, safe to display
	return persisted, nil
}
