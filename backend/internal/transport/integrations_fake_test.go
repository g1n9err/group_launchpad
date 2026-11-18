package transport_test

// Google OAuth endpoint tests: full HTTP surface against a real PostgreSQL
// test database with a FAKE gateway — no network calls to Google, clearly
// labeled as fake per the project's "nothing simulated in prod" rule.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"launchpad/internal/core/integrations"
	"launchpad/internal/secrets"
	"launchpad/internal/store"
)

// fakeGateway replaces Google for endpoint tests (NOT a real client).
type fakeGateway struct{}

func (fakeGateway) AuthCodeURL(state, challenge string) string {
	return "https://accounts.google.com/o/oauth2/v2/auth?state=" + state + "&code_challenge=" + challenge
}

func (fakeGateway) Exchange(_ context.Context, code, verifier string) (integrations.GoogleTokenBundle, error) {
	return integrations.GoogleTokenBundle{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		Expiry:       time.Now().Add(time.Hour),
		AccountEmail: "coordinator@test.example",
	}, nil
}

func (fakeGateway) Refresh(_ context.Context, refreshToken string) (string, time.Time, error) {
	return "refreshed-access-token", time.Now().Add(time.Hour), nil
}

// testCipherKey encrypts credential material in tests only.
const testCipherKey = "4WQk62dNntLF+s4Sy8G68UsmsV66POqZf9RpuxQc6B4=" // base64, 32 bytes

// googleServiceForTest builds the service with a REAL cipher (so AES-GCM is
// exercised end-to-end through Postgres) and the fake gateway.
func googleServiceForTest(pool *pgxpool.Pool) *integrations.GoogleAuthService {
	cipher, err := secrets.New(testCipherKey)
	if err != nil {
		panic("bad test cipher key: " + err.Error())
	}
	return integrations.NewGoogleAuthService(store.New(pool), fakeGateway{}, cipher, true)
}
