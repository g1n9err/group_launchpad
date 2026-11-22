package integrations

import (
	"context"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

const SlackOAuthStateTTL = 10 * time.Minute

var SlackBotScopes = []string{"channels:manage", "channels:read", "channels:write.invites", "chat:write", "users:read", "users:read.email"}

type SlackOAuthService struct {
	repo       SlackWorkspaceRepository
	gateway    SlackOAuthGateway
	cipher     TokenCipher
	configured bool
}

func NewSlackOAuthService(repo SlackWorkspaceRepository, gateway SlackOAuthGateway, cipher TokenCipher, configured bool) *SlackOAuthService {
	return &SlackOAuthService{repo: repo, gateway: gateway, cipher: cipher, configured: configured}
}

func (s *SlackOAuthService) Configured() bool { return s.configured }

func (s *SlackOAuthService) ConnectURL(ctx context.Context) (string, error) {
	if !s.configured {
		return "", slackOAuthNotReady()
	}
	state, err := randomToken(32)
	if err != nil {
		return "", &core.AppError{Code: core.CodeInternal, Message: "could not create Slack authorization state"}
	}
	if err := s.repo.CreateSlackOAuthState(ctx, hashHex(state), time.Now().Add(SlackOAuthStateTTL)); err != nil {
		return "", err
	}
	return s.gateway.AuthCodeURL(state), nil
}

func (s *SlackOAuthService) Callback(ctx context.Context, code, state string) (SlackWorkspaceConnection, error) {
	if !s.configured {
		return SlackWorkspaceConnection{}, slackOAuthNotReady()
	}
	if code == "" || state == "" {
		return SlackWorkspaceConnection{}, core.Validation("missing Slack authorization response", nil)
	}
	if scopedRepo, ok := s.repo.(SlackWorkspaceStateOwnerRepository); ok {
		owner, err := scopedRepo.ConsumeSlackOAuthStateOwner(ctx, hashHex(state))
		if err != nil { return SlackWorkspaceConnection{}, core.Validation("Slack authorization state is invalid, expired, or already used", nil) }
		if owner != uuid.Nil { ctx = core.WithWorkspaceOwner(ctx, owner) }
	} else if err := s.repo.ConsumeSlackOAuthState(ctx, hashHex(state)); err != nil {
		return SlackWorkspaceConnection{}, core.Validation("Slack authorization state is invalid, expired, or already used", nil)
	}
	bundle, err := s.gateway.ExchangeOAuth(ctx, code)
	if err != nil {
		return SlackWorkspaceConnection{}, &core.AppError{Code: core.CodeOAuthExchange, Message: "Slack authorization exchange failed"}
	}
	if bundle.BotToken == "" || bundle.Workspace.TeamID == "" || bundle.Workspace.TeamName == "" {
		return SlackWorkspaceConnection{}, &core.AppError{Code: core.CodeOAuthExchange, Message: "Slack did not return a valid workspace installation"}
	}
	if bundle.Scopes == nil {
		bundle.Scopes = SlackBotScopes
	}
	secret, err := s.cipher.Encrypt(bundle.BotToken)
	if err != nil {
		return SlackWorkspaceConnection{}, &core.AppError{Code: core.CodeInternal, Message: "could not encrypt Slack installation"}
	}
	return s.repo.SaveSlackWorkspace(ctx, bundle.Workspace, bundle.Scopes, secret, s.cipher.KeyID())
}

func (s *SlackOAuthService) Workspaces(ctx context.Context) ([]SlackWorkspaceConnection, error) {
	if !s.configured {
		return nil, slackOAuthNotReady()
	}
	return s.repo.ListSlackWorkspaces(ctx)
}

func slackOAuthNotReady() error {
	return &core.AppError{Code: core.CodeNotReady, Message: "Slack multi-workspace OAuth is not configured"}
}
