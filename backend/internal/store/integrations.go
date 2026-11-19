package store

// Integration connection persistence — implements
// core/integrations.GoogleAuthRepository over the sqlc-generated queries.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"launchpad/internal/core"
	coreintegrations "launchpad/internal/core/integrations"
	"launchpad/internal/store/gen"
)

// Compile-time proof the port is satisfied.
var _ coreintegrations.GoogleAuthRepository = (*Repo)(nil)

func (r *Repo) CreateState(ctx context.Context, stateHash, verifierEnc string, expiresAt time.Time) error {
	return r.q.CreateOAuthState(ctx, gen.CreateOAuthStateParams{
		StateHash:       stateHash,
		CodeVerifierEnc: verifierEnc,
		Provider:        coreintegrations.ProviderGoogle,
		ExpiresAt:       expiresAt,
	})
}

func (r *Repo) ConsumeState(ctx context.Context, stateHash string) (string, error) {
	verifierEnc, err := r.q.ConsumeOAuthState(ctx, stateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Unknown, expired, or already consumed — indistinguishable on purpose.
			return "", core.Validation("oauth state is invalid, expired, or already used", nil)
		}
		return "", fmt.Errorf("store: consume oauth state: %w", err)
	}
	return verifierEnc, nil
}

// SaveConnection upserts the integration row and replaces the active
// credential atomically in one transaction.
func (r *Repo) SaveConnection(ctx context.Context, accountEmail string, scopes []string, refreshTokenEnc, keyID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: save connection tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — no-op on commit

	qtx := r.q.WithTx(tx)
	row, err := qtx.UpsertGoogleIntegration(ctx, accountEmail)
	if err != nil {
		return fmt.Errorf("store: upsert integration: %w", err)
	}
	if _, err := qtx.RevokeActiveIntegrationCredentials(ctx, row.ID); err != nil {
		return fmt.Errorf("store: revoke old credentials: %w", err)
	}
	if err := qtx.InsertIntegrationCredential(ctx, gen.InsertIntegrationCredentialParams{
		IntegrationID:   row.ID,
		RefreshTokenEnc: refreshTokenEnc,
		KeyID:           keyID,
		Scopes:          scopes,
	}); err != nil {
		return fmt.Errorf("store: insert credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: save connection commit: %w", err)
	}
	return nil
}

func (r *Repo) GoogleConnection(ctx context.Context) (coreintegrations.StoredGoogleConnection, error) {
	row, err := r.q.GetGoogleConnection(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return coreintegrations.StoredGoogleConnection{
				Status: coreintegrations.GoogleStatus{Status: coreintegrations.StatusDisconnected},
			}, nil
		}
		return coreintegrations.StoredGoogleConnection{}, fmt.Errorf("store: get google connection: %w", err)
	}

	status := coreintegrations.GoogleStatus{
		AccountEmail: row.AccountIdentifier,
		ConnectedAt:  row.CreatedAt,
	}
	switch {
	case !row.CredentialID.Valid:
		status.Status = coreintegrations.StatusDisconnected
	case row.ConnectionStatus == coreintegrations.StatusError:
		status.Status = coreintegrations.StatusError
		status.Scopes = derefScopes(row.CredentialScopes)
	default:
		status.Status = coreintegrations.StatusConnected
		status.Scopes = derefScopes(row.CredentialScopes)
	}

	var rtEnc, keyID string
	if row.CredentialID.Valid {
		rtEnc, keyID = derefString(row.RefreshTokenEnc), derefString(row.KeyID)
	}
	return coreintegrations.StoredGoogleConnection{
		Status:          status,
		RefreshTokenEnc: rtEnc,
		KeyID:           keyID,
	}, nil
}

func (r *Repo) MarkGoogleError(ctx context.Context) error {
	if err := r.q.MarkGoogleIntegrationError(ctx); err != nil {
		return fmt.Errorf("store: mark google error: %w", err)
	}
	return nil
}

// ── SlackRepository (core/integrations) ───────────────────────────────────

// Compile-time proof the port is satisfied.
var _ coreintegrations.SlackRepository = (*Repo)(nil)

func (r *Repo) UpsertSlackConnection(ctx context.Context, teamID, botUserID string) (coreintegrations.SlackStatus, error) {
	row, err := r.q.UpsertSlackIntegration(ctx, gen.UpsertSlackIntegrationParams{
		AccountIdentifier: teamID,
		BotUserID:         &botUserID,
	})
	if err != nil {
		return coreintegrations.SlackStatus{}, fmt.Errorf("store: upsert slack integration: %w", err)
	}
	return coreintegrations.SlackStatus{
		Status:      coreintegrations.StatusConnected,
		TeamID:      row.AccountIdentifier,
		ConnectedAt: row.CreatedAt,
	}, nil
}

func (r *Repo) MarkSlackError(ctx context.Context) error {
	if err := r.q.MarkSlackIntegrationError(ctx); err != nil {
		return fmt.Errorf("store: mark slack error: %w", err)
	}
	return nil
}

func (r *Repo) RecordExternalOperation(ctx context.Context, op coreintegrations.ExternalOperation) error {
	var errCategory *string
	if op.Error != "" {
		errCategory = &op.Error
	}
	dur := int32(op.DurationMS)
	if err := r.q.InsertExternalOperationLog(ctx, gen.InsertExternalOperationLogParams{
		GroupID:    uuidToPg(op.GroupID),
		Provider:   op.Provider,
		Operation:  op.Operation,
		Error:      errCategory,
		DurationMs: &dur,
	}); err != nil {
		return fmt.Errorf("store: insert operation log: %w", err)
	}
	return nil
}

// uuidToPg converts an optional uuid to the pgtype sqlc expects for nullable
// columns.
func uuidToPg(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

// ── SlackResourceRepository (core/integrations) ────────────────────────────

// Compile-time proof the port is satisfied.
var _ coreintegrations.SlackResourceRepository = (*Repo)(nil)

func (r *Repo) GetResource(ctx context.Context, groupID uuid.UUID, provider, resourceType, name string) (coreintegrations.GroupResource, bool, error) {
	row, err := r.q.GetGroupResource(ctx, gen.GetGroupResourceParams{
		GroupID:      groupID,
		Provider:     provider,
		ResourceType: resourceType,
		ResourceName: name,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return coreintegrations.GroupResource{}, false, nil
		}
		return coreintegrations.GroupResource{}, false, fmt.Errorf("store: get group resource: %w", err)
	}
	return coreintegrations.GroupResource{
		Provider:   row.Provider,
		Type:       row.ResourceType,
		Name:       row.ResourceName,
		ExternalID: row.ExternalResourceID,
		Status:     row.Status,
	}, true, nil
}

func (r *Repo) InsertResource(ctx context.Context, groupID uuid.UUID, res coreintegrations.GroupResource) (bool, error) {
	n, err := r.q.InsertGroupResource(ctx, gen.InsertGroupResourceParams{
		GroupID:            groupID,
		Provider:           res.Provider,
		ResourceType:       res.Type,
		ExternalResourceID: res.ExternalID,
		ResourceName:       res.Name,
		Status:             res.Status,
	})
	if err != nil {
		return false, fmt.Errorf("store: insert group resource: %w", err)
	}
	return n > 0, nil
}

func (r *Repo) UpdateResourceResult(ctx context.Context, groupID uuid.UUID, res coreintegrations.GroupResource) error {
	if err := r.q.UpdateGroupResourceResult(ctx, gen.UpdateGroupResourceResultParams{
		GroupID:            groupID,
		Provider:           res.Provider,
		ResourceType:       res.Type,
		ExternalResourceID: res.ExternalID,
		ResourceName:       res.Name,
		Status:             res.Status,
	}); err != nil {
		return fmt.Errorf("store: update group resource: %w", err)
	}
	return nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefScopes(s []string) []string {
	if s == nil {
		return nil
	}
	return s
}

// ── SlackEventRepository (core/integrations) ───────────────────────────────

// Compile-time proof the port is satisfied.
var _ coreintegrations.SlackEventRepository = (*Repo)(nil)

func (r *Repo) InsertSlackEvent(ctx context.Context, ev coreintegrations.RecordedSlackEvent) (bool, error) {
	n, err := r.q.InsertSlackEvent(ctx, gen.InsertSlackEventParams{
		EventID:   ev.EventID,
		EventType: ev.EventType,
		TeamID:    &ev.TeamID,
		UserID:    &ev.UserID,
		ChannelID: &ev.ChannelID,
		Payload:   ev.Payload,
	})
	if err != nil {
		return false, fmt.Errorf("store: insert slack event: %w", err)
	}
	return n > 0, nil
}

func (r *Repo) CompleteSlackEvent(ctx context.Context, eventID, status, unresolvedReason string) error {
	var reason *string
	if unresolvedReason != "" {
		reason = &unresolvedReason
	}
	if err := r.q.CompleteSlackEvent(ctx, gen.CompleteSlackEventParams{
		EventID:          eventID,
		ProcessStatus:    status,
		UnresolvedReason: reason,
	}); err != nil {
		return fmt.Errorf("store: complete slack event: %w", err)
	}
	return nil
}

func (r *Repo) SlackBotUserID(ctx context.Context) (string, error) {
	id, err := r.q.GetSlackBotUserID(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("store: get slack bot user id: %w", err)
	}
	if id == nil {
		return "", nil
	}
	return *id, nil
}

func (r *Repo) FindGroupIDBySlackChannel(ctx context.Context, channelID string) (uuid.UUID, bool, error) {
	id, err := r.q.FindGroupIDBySlackChannel(ctx, channelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, fmt.Errorf("store: find group by slack channel: %w", err)
	}
	return id, true, nil
}

func (r *Repo) FindUserIDBySlack(ctx context.Context, slackUserID string) (uuid.UUID, bool, error) {
	id, err := r.q.FindUserIDBySlackID(ctx, &slackUserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, fmt.Errorf("store: find user by slack id: %w", err)
	}
	return id, true, nil
}

func (r *Repo) FindGroupMember(ctx context.Context, groupID, userID uuid.UUID) (uuid.UUID, string, bool, error) {
	row, err := r.q.FindGroupMemberByUser(ctx, gen.FindGroupMemberByUserParams{GroupID: groupID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", false, nil
		}
		return uuid.Nil, "", false, fmt.Errorf("store: find group member: %w", err)
	}
	return row.ID, row.OnboardingStatus, true, nil
}

func (r *Repo) MarkGroupMemberJoinedSlack(ctx context.Context, memberID uuid.UUID) error {
	if err := r.q.MarkGroupMemberJoinedSlack(ctx, memberID); err != nil {
		return fmt.Errorf("store: mark member joined_slack: %w", err)
	}
	return nil
}

// ── SlackIdentityRepository (core/integrations) ────────────────────────────

// Compile-time proof the port is satisfied.
var _ coreintegrations.SlackIdentityRepository = (*Repo)(nil)

func (r *Repo) FindUserIDsByEmail(ctx context.Context, normalizedEmail string) ([]uuid.UUID, error) {
	ids, err := r.q.FindUserIDsByEmail(ctx, normalizedEmail)
	if err != nil {
		return nil, fmt.Errorf("store: find users by email: %w", err)
	}
	return ids, nil
}

// LinkSlackUser links via a guarded UPDATE so concurrent linkers race safely:
// the loser gets zero rows, re-reads, and reports already/taken/mismatch.
// A unique-index violation means another user owns this Slack ID.
func (r *Repo) LinkSlackUser(ctx context.Context, userID uuid.UUID, slackUserID string) (coreintegrations.LinkState, error) {
	n, err := r.q.LinkUserSlackID(ctx, gen.LinkUserSlackIDParams{SlackUserID: &slackUserID, ID: userID})
	if err != nil {
		if isUniqueViolation(err) {
			return coreintegrations.LinkStateTaken, nil
		}
		return "", fmt.Errorf("store: link user slack id: %w", err)
	}
	if n > 0 {
		return coreintegrations.LinkStateLinked, nil
	}
	// Lost the race (or the user was linked meanwhile): inspect the winner.
	current, err := r.q.GetUserSlackID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("store: get user slack id: %w", err)
	}
	if current != nil && *current == slackUserID {
		return coreintegrations.LinkStateAlready, nil
	}
	return coreintegrations.LinkStateMismatch, nil
}
