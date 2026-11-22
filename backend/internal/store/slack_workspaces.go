package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"launchpad/internal/core"
	coreintegrations "launchpad/internal/core/integrations"
)

var _ coreintegrations.SlackWorkspaceRepository = (*Repo)(nil)
var _ coreintegrations.GoogleWorkspaceRepository = (*Repo)(nil)

func (r *Repo) CreateSlackOAuthState(ctx context.Context, stateHash string, expiresAt time.Time) error {
	owner, _ := core.WorkspaceOwner(ctx)
	var ownerValue any
	if owner != uuid.Nil {
		ownerValue = owner
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO oauth_states (state_hash, code_verifier_enc, provider, expires_at, workspace_owner_id) VALUES ($1,'slack-oauth',$2,$3,$4)`, stateHash, "slack", expiresAt, ownerValue)
	if err != nil {
		return fmt.Errorf("store: create slack oauth state: %w", err)
	}
	return nil
}

func (r *Repo) ConsumeSlackOAuthState(ctx context.Context, stateHash string) error {
	_, err := r.ConsumeSlackOAuthStateOwner(ctx, stateHash)
	return err
}

func (r *Repo) ConsumeSlackOAuthStateOwner(ctx context.Context, stateHash string) (uuid.UUID, error) {
	var owner uuid.NullUUID
	var consumed string
	err := r.pool.QueryRow(ctx, `UPDATE oauth_states SET consumed_at=now() WHERE state_hash=$1 AND provider='slack' AND consumed_at IS NULL AND expires_at>now() RETURNING state_hash,workspace_owner_id`, stateHash).Scan(&consumed, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("slack oauth state is invalid or expired")
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: consume slack oauth state: %w", err)
	}
	if owner.Valid {
		return owner.UUID, nil
	}
	return uuid.Nil, nil
}

func (r *Repo) ListSlackWorkspaces(ctx context.Context) ([]coreintegrations.SlackWorkspaceConnection, error) {
	owner, scoped := core.WorkspaceOwner(ctx)
	rows, err := r.pool.Query(ctx, `SELECT i.id, i.account_identifier, COALESCE(i.team_name,''), COALESCE(i.bot_user_id,''),
		i.connection_status, COALESCE(c.scopes, ARRAY[]::text[]), i.created_at, i.google_integration_id, COALESCE(g.account_identifier,'')
		FROM integrations i LEFT JOIN integration_credentials c ON c.integration_id=i.id AND c.revoked_at IS NULL
		LEFT JOIN integrations g ON g.id=i.google_integration_id AND g.provider='google'
		WHERE i.provider='slack' AND ($1::uuid IS NULL OR i.workspace_owner_id=$1) ORDER BY i.created_at, i.account_identifier`, nullableOwner(owner, scoped))
	if err != nil {
		return nil, fmt.Errorf("store: list slack workspaces: %w", err)
	}
	defer rows.Close()
	items := make([]coreintegrations.SlackWorkspaceConnection, 0)
	for rows.Next() {
		var item coreintegrations.SlackWorkspaceConnection
		if err := rows.Scan(&item.ID, &item.TeamID, &item.TeamName, &item.BotUserID, &item.Status, &item.Scopes, &item.ConnectedAt, &item.GoogleAccountID, &item.GoogleAccountEmail); err != nil {
			return nil, fmt.Errorf("store: scan slack workspace: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate slack workspaces: %w", err)
	}
	return items, nil
}

func (r *Repo) SaveSlackWorkspace(ctx context.Context, workspace coreintegrations.SlackWorkspace, scopes []string, tokenEncrypted, keyID string) (coreintegrations.SlackWorkspaceConnection, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return coreintegrations.SlackWorkspaceConnection{}, fmt.Errorf("store: begin slack install: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var id uuid.UUID
	var createdAt time.Time
	owner, scoped := core.WorkspaceOwner(ctx)
	var ownerValue any
	if scoped {
		ownerValue = owner
	}
	err = tx.QueryRow(ctx, `INSERT INTO integrations (provider, account_identifier, team_name, bot_user_id, connection_status, workspace_owner_id)
		VALUES ('slack',$1,$2,$3,'connected',$4) ON CONFLICT (provider,workspace_owner_id,account_identifier) DO UPDATE
		SET team_name=EXCLUDED.team_name, bot_user_id=EXCLUDED.bot_user_id, connection_status='connected'
		RETURNING id, created_at`, workspace.TeamID, workspace.TeamName, workspace.BotUserID, ownerValue).Scan(&id, &createdAt)
	if err != nil {
		return coreintegrations.SlackWorkspaceConnection{}, fmt.Errorf("store: upsert slack install: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE integration_credentials SET revoked_at=now() WHERE integration_id=$1 AND revoked_at IS NULL`, id); err != nil {
		return coreintegrations.SlackWorkspaceConnection{}, fmt.Errorf("store: rotate slack credential: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO integration_credentials (integration_id, refresh_token_enc, key_id, scopes) VALUES ($1,$2,$3,$4)`, id, tokenEncrypted, keyID, scopes); err != nil {
		return coreintegrations.SlackWorkspaceConnection{}, fmt.Errorf("store: save slack credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return coreintegrations.SlackWorkspaceConnection{}, fmt.Errorf("store: commit slack install: %w", err)
	}
	return coreintegrations.SlackWorkspaceConnection{ID: id, TeamID: workspace.TeamID, TeamName: workspace.TeamName, BotUserID: workspace.BotUserID, Status: coreintegrations.StatusConnected, Scopes: scopes, ConnectedAt: createdAt}, nil
}

func (r *Repo) SlackWorkspaceCredential(ctx context.Context, id uuid.UUID) (coreintegrations.SlackWorkspaceCredential, error) {
	var item coreintegrations.SlackWorkspaceCredential
	err := r.pool.QueryRow(ctx, `SELECT i.id, i.account_identifier, COALESCE(i.team_name,''), COALESCE(i.bot_user_id,''),
		i.connection_status, c.scopes, i.created_at, c.refresh_token_enc, c.key_id
		FROM integrations i JOIN integration_credentials c ON c.integration_id=i.id AND c.revoked_at IS NULL
		WHERE i.id=$1 AND i.provider='slack' AND ($2::uuid IS NULL OR i.workspace_owner_id=$2)`, id, nullableOwnerFromContext(ctx)).Scan(&item.ID, &item.TeamID, &item.TeamName, &item.BotUserID, &item.Status, &item.Scopes, &item.ConnectedAt, &item.TokenEncrypted, &item.KeyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, fmt.Errorf("slack workspace credential not found")
	}
	if err != nil {
		return item, fmt.Errorf("store: read slack credential: %w", err)
	}
	return item, nil
}

func (r *Repo) GroupSlackWorkspace(ctx context.Context, groupID uuid.UUID) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT g.slack_integration_id FROM groups g
		WHERE g.id=$1 AND (g.slack_integration_id IS NULL OR EXISTS (
			SELECT 1 FROM integrations i WHERE i.id=g.slack_integration_id AND i.provider='slack'
			AND i.workspace_owner_id IS NOT DISTINCT FROM g.workspace_owner_id))`, groupID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("store: get group slack workspace: %w", err)
	}
	return id, id != uuid.Nil, nil
}

func (r *Repo) GroupHasWorkspaceOwner(ctx context.Context, groupID uuid.UUID) (bool, error) {
	var owned bool
	err := r.pool.QueryRow(ctx, `SELECT workspace_owner_id IS NOT NULL FROM groups WHERE id=$1`, groupID).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("group not found")
	}
	return owned, err
}

func (r *Repo) ListGroupSlackWorkspaceAssignments(ctx context.Context) ([]coreintegrations.GroupSlackWorkspaceAssignment, error) {
	owner, scoped := core.WorkspaceOwner(ctx)
	rows, err := r.pool.Query(ctx, `SELECT g.id, g.slack_integration_id, COALESCE(NULLIF(i.team_name,''),i.account_identifier,'Default Slack workspace')
		FROM groups g LEFT JOIN integrations i ON i.id=g.slack_integration_id
		WHERE $1::uuid IS NULL OR g.workspace_owner_id=$1 ORDER BY g.name`, nullableOwner(owner, scoped))
	if err != nil {
		return nil, fmt.Errorf("store: list group Slack workspaces: %w", err)
	}
	defer rows.Close()
	items := make([]coreintegrations.GroupSlackWorkspaceAssignment, 0)
	for rows.Next() {
		var item coreintegrations.GroupSlackWorkspaceAssignment
		if err := rows.Scan(&item.GroupID, &item.WorkspaceID, &item.WorkspaceName); err != nil {
			return nil, fmt.Errorf("store: scan group Slack workspace: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repo) SetGroupSlackWorkspace(ctx context.Context, groupID, integrationID uuid.UUID) error {
	var current uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(slack_integration_id,'00000000-0000-0000-0000-000000000000'::uuid) FROM groups WHERE id=$1`, groupID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("store: group not found")
	}
	if err != nil {
		return fmt.Errorf("store: read current group Slack workspace: %w", err)
	}
	if current == integrationID {
		return nil
	}
	var provisioned bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM group_resources WHERE group_id=$1 AND provider='slack' AND resource_type='channel' AND status='active')`, groupID).Scan(&provisioned); err != nil {
		return fmt.Errorf("store: check existing Slack channels: %w", err)
	}
	if provisioned {
		return &core.AppError{Code: core.CodeConflict, Message: "Slack workspace cannot be changed after channels have been created for this group"}
	}
	if integrationID == uuid.Nil {
		result, err := r.pool.Exec(ctx, `UPDATE groups SET slack_integration_id=NULL WHERE id=$1`, groupID)
		if err != nil {
			return fmt.Errorf("store: clear group slack workspace: %w", err)
		}
		if result.RowsAffected() == 0 {
			return fmt.Errorf("store: group not found")
		}
		return nil
	}
	owner, scoped := core.WorkspaceOwner(ctx)
	result, err := r.pool.Exec(ctx, `UPDATE groups g SET slack_integration_id=$2
		WHERE g.id=$1 AND ($3::uuid IS NULL OR g.workspace_owner_id=$3) AND EXISTS (SELECT 1 FROM integrations i WHERE i.id=$2 AND i.provider='slack' AND i.connection_status='connected' AND i.workspace_owner_id IS NOT DISTINCT FROM g.workspace_owner_id)`, groupID, integrationID, nullableOwner(owner, scoped))
	if err != nil {
		return fmt.Errorf("store: assign slack workspace: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("store: group or connected slack workspace not found")
	}
	return nil
}

func (r *Repo) SetSlackWorkspaceGoogleAccount(ctx context.Context, slackID, googleID uuid.UUID) error {
	var result pgconn.CommandTag
	var err error
	if googleID == uuid.Nil {
		result, err = r.pool.Exec(ctx, `UPDATE integrations SET google_integration_id=NULL WHERE id=$1 AND provider='slack' AND ($2::uuid IS NULL OR workspace_owner_id=$2)`, slackID, nullableOwnerFromContext(ctx))
	} else {
		result, err = r.pool.Exec(ctx, `UPDATE integrations s SET google_integration_id=$2 WHERE s.id=$1 AND s.provider='slack' AND ($3::uuid IS NULL OR s.workspace_owner_id=$3) AND EXISTS (SELECT 1 FROM integrations g JOIN integration_credentials c ON c.integration_id=g.id AND c.revoked_at IS NULL WHERE g.id=$2 AND g.provider='google' AND g.connection_status='connected' AND g.workspace_owner_id IS NOT DISTINCT FROM s.workspace_owner_id)`, slackID, googleID, nullableOwnerFromContext(ctx))
	}
	if err != nil {
		return fmt.Errorf("store: link Slack workspace to Google account: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("store: Slack workspace or connected Google account not found")
	}
	return nil
}

func (r *Repo) ListGoogleWorkspaces(ctx context.Context) ([]coreintegrations.GoogleWorkspaceConnection, error) {
	owner, scoped := core.WorkspaceOwner(ctx)
	rows, err := r.pool.Query(ctx, `SELECT i.id, i.account_identifier, i.connection_status, COALESCE(c.scopes, ARRAY[]::text[])
		FROM integrations i LEFT JOIN integration_credentials c ON c.integration_id=i.id AND c.revoked_at IS NULL
		WHERE i.provider='google' AND ($1::uuid IS NULL OR i.workspace_owner_id=$1) ORDER BY i.created_at, i.account_identifier`, nullableOwner(owner, scoped))
	if err != nil {
		return nil, fmt.Errorf("store: list Google accounts: %w", err)
	}
	defer rows.Close()
	items := make([]coreintegrations.GoogleWorkspaceConnection, 0)
	for rows.Next() {
		var item coreintegrations.GoogleWorkspaceConnection
		if err := rows.Scan(&item.ID, &item.AccountEmail, &item.Status, &item.Scopes); err != nil {
			return nil, fmt.Errorf("store: scan Google account: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repo) GoogleWorkspaceCredential(ctx context.Context, id uuid.UUID) (coreintegrations.GoogleWorkspaceCredential, error) {
	var item coreintegrations.GoogleWorkspaceCredential
	err := r.pool.QueryRow(ctx, `SELECT i.id,i.account_identifier,i.connection_status,c.scopes,c.refresh_token_enc,c.key_id FROM integrations i JOIN integration_credentials c ON c.integration_id=i.id AND c.revoked_at IS NULL WHERE i.id=$1 AND i.provider='google' AND ($2::uuid IS NULL OR i.workspace_owner_id=$2)`, id, nullableOwnerFromContext(ctx)).Scan(&item.ID, &item.AccountEmail, &item.Status, &item.Scopes, &item.RefreshTokenEncrypted, &item.KeyID)
	if err != nil {
		return item, fmt.Errorf("store: read Google account credential: %w", err)
	}
	return item, nil
}

func nullableOwner(owner uuid.UUID, scoped bool) any {
	if scoped {
		return owner
	}
	return nil
}

func nullableOwnerFromContext(ctx context.Context) any {
	owner, scoped := core.WorkspaceOwner(ctx)
	return nullableOwner(owner, scoped)
}

func (r *Repo) GroupGoogleWorkspace(ctx context.Context, groupID uuid.UUID) (uuid.UUID, bool, error) {
	var id pgtype.UUID
	var owner pgtype.UUID
	err := r.pool.QueryRow(ctx, `SELECT g.workspace_owner_id,
		COALESCE(s.google_integration_id, (
			SELECT gi.id FROM integrations gi WHERE gi.provider='google'
			AND gi.workspace_owner_id IS NOT DISTINCT FROM g.workspace_owner_id
			ORDER BY gi.created_at DESC LIMIT 1))
		FROM groups g LEFT JOIN integrations s ON s.id=g.slack_integration_id AND s.workspace_owner_id IS NOT DISTINCT FROM g.workspace_owner_id
		WHERE g.id=$1`, groupID).Scan(&owner, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("store: get group Google account: %w", err)
	}
	if owner.Valid && !id.Valid {
		return uuid.Nil, false, fmt.Errorf("no Google account is connected to this coordinator workspace")
	}
	if !id.Valid {
		return uuid.Nil, false, nil // legacy unowned group may use the legacy global connection
	}
	return uuid.UUID(id.Bytes), true, nil
}
