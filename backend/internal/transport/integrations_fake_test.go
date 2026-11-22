package transport_test

// Google OAuth endpoint tests: full HTTP surface against a real PostgreSQL
// test database with a FAKE gateway — no network calls to Google, clearly
// labeled as fake per the project's "nothing simulated in prod" rule.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	slackgw "launchpad/internal/integrations/slack"

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

// fakeSlackGateway replaces Slack for endpoint tests (NOT a real client) —
// an in-memory workspace with stateful channels, membership, and messages so
// idempotency is observable end-to-end through real HTTP + PostgreSQL.
type fakeSlackGateway struct {
	workspace integrations.SlackWorkspace
	err       error

	channels map[string]string // name → ID
	nextID   int
	members  map[string]bool // "channelID|userID" → member
	emails   map[string]string
	posts    map[string]int // channelID → message count
}

func newFakeSlackState() *fakeSlackGateway {
	return &fakeSlackGateway{
		workspace: integrations.SlackWorkspace{TeamID: "TTESTWS", TeamName: "Fixture Workspace"},
		channels:  map[string]string{}, members: map[string]bool{},
		emails: map[string]string{
			"staff@x.example":  "U1",
			"leader@x.example": "U2",
		},
		posts: map[string]int{},
	}
}

func (f *fakeSlackGateway) AuthTest(context.Context) (integrations.SlackWorkspace, error) {
	return f.workspace, f.err
}

func (f *fakeSlackGateway) LookupChannelIDs(_ context.Context, names []string) (map[string]string, error) {
	out := map[string]string{}
	for _, n := range names {
		if id, ok := f.channels[n]; ok {
			out[n] = id
		}
	}
	return out, nil
}

func (f *fakeSlackGateway) CreateChannel(_ context.Context, name string) (string, error) {
	if _, exists := f.channels[name]; exists {
		return "", integrations.ErrSlackChannelNameTaken
	}
	f.nextID++
	id := fmt.Sprintf("CFAKE%03d", f.nextID)
	f.channels[name] = id
	return id, nil
}

func (f *fakeSlackGateway) LookupUserByEmail(_ context.Context, email string) (string, error) {
	if id, ok := f.emails[email]; ok {
		return id, nil
	}
	return "", integrations.ErrSlackUserNotFound
}

func (f *fakeSlackGateway) UserEmail(_ context.Context, userID string) (string, error) {
	for email, id := range f.emails {
		if id == userID {
			return email, nil
		}
	}
	return "", integrations.ErrSlackUserNotFound
}

func (f *fakeSlackGateway) InviteUserToChannel(_ context.Context, channelID, userID string) (integrations.InviteOutcome, error) {
	key := channelID + "|" + userID
	if f.members[key] {
		return integrations.InviteOutcomeAlreadyMember, nil
	}
	f.members[key] = true
	return integrations.InviteOutcomeInvited, nil
}
func (f *fakeSlackGateway) RemoveUserFromChannel(_ context.Context, channelID, userID string) error {
	delete(f.members, channelID+"|"+userID)
	return nil
}

func (f *fakeSlackGateway) PostMessage(_ context.Context, channelID, text string) (string, error) {
	f.posts[channelID]++
	return "1700000000.000100", nil
}

// slackServiceForTest builds a configured Slack service over the fake gateway
// so endpoint tests exercise persistence without contacting Slack.
func slackServiceForTest(pool *pgxpool.Pool) *integrations.SlackAuthService {
	return integrations.NewSlackAuthService(store.New(pool), newFakeSlackState(), true)
}

// slackChannelsServiceForTest builds the provisioning service over the same
// fake Slack state so channels/invites/welcomes are observable across calls.
func slackChannelsServiceForTest(pool *pgxpool.Pool) *integrations.SlackChannelsService {
	repo := store.New(pool)
	return integrations.NewSlackChannelsService(newFakeSlackState(), repo, repo, true)
}

// testSigningSecret signs webhook requests in tests (fixture only).
const testSigningSecret = "fixture-signing-secret-not-real"

// testClock is fixed so replay-window behavior is deterministic.
var testClock = func() time.Time { return time.Unix(1700000000, 0) }

// slackEventsServiceForTest uses the REAL HMAC verifier (crypto exercised
// end-to-end) with the fixture secret and fixed clock. Identity linking is
// attached, backed by the same fake Slack state (UserEmail reverses emails).
func slackEventsServiceForTest(pool *pgxpool.Pool) *integrations.SlackEventsService {
	repo := store.New(pool)
	gw := newFakeSlackState()
	linker := integrations.NewSlackIdentityLinker(gw, repo)
	return integrations.NewSlackEventsService(slackgw.NewRequestVerifier(testSigningSecret), repo, linker, testClock, true)
}
