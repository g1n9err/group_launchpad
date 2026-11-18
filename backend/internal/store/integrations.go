package store

// Integration connection persistence — implements
// core/integrations.GoogleAuthRepository over the sqlc-generated queries.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

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
