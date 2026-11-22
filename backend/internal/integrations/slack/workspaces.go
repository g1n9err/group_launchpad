package slack

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	coreintegrations "launchpad/internal/core/integrations"
	"launchpad/internal/jobs"
)

// WorkspaceResolver builds a Slack API client from the encrypted installation
// assigned to a group. The environment token remains the legacy fallback for
// groups that have not selected a workspace.
type WorkspaceResolver struct {
	repo     coreintegrations.SlackWorkspaceRepository
	cipher   coreintegrations.TokenCipher
	fallback coreintegrations.SlackGateway
}

func NewWorkspaceResolver(repo coreintegrations.SlackWorkspaceRepository, cipher coreintegrations.TokenCipher, fallback coreintegrations.SlackGateway) *WorkspaceResolver {
	return &WorkspaceResolver{repo: repo, cipher: cipher, fallback: fallback}
}

func (r *WorkspaceResolver) ForGroup(ctx context.Context, groupID uuid.UUID) (coreintegrations.SlackGateway, error) {
	id, assigned, err := r.repo.GroupSlackWorkspace(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if !assigned {
		if scoped, ok := r.repo.(interface {
			GroupHasWorkspaceOwner(context.Context, uuid.UUID) (bool, error)
		}); ok {
			owned, err := scoped.GroupHasWorkspaceOwner(ctx, groupID)
			if err != nil {
				return nil, err
			}
			if owned {
				return nil, fmt.Errorf("no Slack workspace assigned to this account's group")
			}
		}
		if r.fallback == nil {
			return nil, fmt.Errorf("no Slack workspace assigned to group")
		}
		return r.fallback, nil
	}
	if r.cipher == nil {
		return nil, fmt.Errorf("Slack credential encryption is unavailable")
	}
	credential, err := r.repo.SlackWorkspaceCredential(ctx, id)
	if err != nil {
		return nil, err
	}
	token, err := r.cipher.Decrypt(credential.TokenEncrypted)
	if err != nil {
		return nil, fmt.Errorf("Slack workspace credential could not be decrypted")
	}
	return NewGateway(token), nil
}

func (r *WorkspaceResolver) ForTeam(ctx context.Context, teamID string) (coreintegrations.SlackGateway, error) {
	workspaces, err := r.repo.ListSlackWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	for _, workspace := range workspaces {
		if workspace.TeamID != teamID {
			continue
		}
		if len(workspace.Scopes) == 0 && r.fallback != nil {
			return r.fallback, nil
		}
		if r.cipher == nil {
			return nil, fmt.Errorf("Slack credential encryption is unavailable")
		}
		credential, err := r.repo.SlackWorkspaceCredential(ctx, workspace.ID)
		if err != nil {
			return nil, err
		}
		token, err := r.cipher.Decrypt(credential.TokenEncrypted)
		if err != nil {
			return nil, fmt.Errorf("Slack workspace credential could not be decrypted")
		}
		return NewGateway(token), nil
	}
	if r.fallback != nil {
		return r.fallback, nil
	}
	return nil, fmt.Errorf("Slack workspace is not installed")
}

type AnnouncementResolver struct{ Workspaces *WorkspaceResolver }

func (r AnnouncementResolver) ForGroup(ctx context.Context, groupID uuid.UUID) (jobs.AnnouncementPublisher, error) {
	gateway, err := r.Workspaces.ForGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	publisher, ok := gateway.(interface {
		PostAnnouncement(context.Context, string, string) (string, bool, error)
	})
	if !ok {
		return nil, fmt.Errorf("Slack workspace does not support announcements")
	}
	return publisher, nil
}
