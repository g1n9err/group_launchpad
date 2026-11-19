package integrations

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// ── fakes ──────────────────────────────────────────────────────────────────

type fakeIdentityGateway struct {
	emails map[string]string // slackUserID → email
	err    error             // returned for every lookup when set
}

func (f fakeIdentityGateway) AuthTest(context.Context) (SlackWorkspace, error) {
	return SlackWorkspace{}, nil
}
func (f fakeIdentityGateway) LookupChannelIDs(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (f fakeIdentityGateway) CreateChannel(context.Context, string) (string, error) {
	return "", nil
}
func (f fakeIdentityGateway) LookupUserByEmail(context.Context, string) (string, error) {
	return "", nil
}
func (f fakeIdentityGateway) InviteUserToChannel(context.Context, string, string) (InviteOutcome, error) {
	return "", nil
}
func (f fakeIdentityGateway) PostMessage(context.Context, string, string) (string, error) {
	return "", nil
}
func (f fakeIdentityGateway) UserEmail(_ context.Context, slackUserID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.emails[slackUserID], nil // "" when absent
}

type fakeIdentityRepo struct {
	mu        sync.Mutex
	byEmail   map[string][]uuid.UUID // normalized email → users
	linked    map[uuid.UUID]string   // user → slack id
	takenBy   map[string]uuid.UUID   // slack id → owning user (unique-index sim)
	linkCalls int
}

func newFakeIdentityRepo() *fakeIdentityRepo {
	return &fakeIdentityRepo{byEmail: map[string][]uuid.UUID{}, linked: map[uuid.UUID]string{}, takenBy: map[string]uuid.UUID{}}
}

func (r *fakeIdentityRepo) FindUserIDsByEmail(_ context.Context, email string) ([]uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byEmail[email], nil
}

func (r *fakeIdentityRepo) LinkSlackUser(_ context.Context, userID uuid.UUID, slackUserID string) (LinkState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.linkCalls++
	if owner, taken := r.takenBy[slackUserID]; taken && owner != userID {
		return LinkStateTaken, nil // unique index violation
	}
	if existing, has := r.linked[userID]; has {
		if existing == slackUserID {
			return LinkStateAlready, nil
		}
		return LinkStateMismatch, nil
	}
	r.linked[userID] = slackUserID
	r.takenBy[slackUserID] = userID
	return LinkStateLinked, nil
}

// ── tests ──────────────────────────────────────────────────────────────────

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  Student@X.Example  ":  "student@x.example",
		"Mixed.Case@Example.COM": "mixed.case@example.com",
		"":                       "",
	}
	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinkUniqueMatchLinksUser(t *testing.T) {
	repo := newFakeIdentityRepo()
	userID := uuid.New()
	repo.byEmail["student@x.example"] = []uuid.UUID{userID}
	gw := fakeIdentityGateway{emails: map[string]string{"U123": "Student@X.Example"}} // case difference on purpose
	linker := NewSlackIdentityLinker(gw, repo)

	linkedID, reason, err := linker.Link(context.Background(), "U123")
	if err != nil || reason != "" || linkedID != userID {
		t.Fatalf("linkedID=%v reason=%q err=%v", linkedID, reason, err)
	}
	if repo.linked[userID] != "U123" {
		t.Error("mapping not persisted")
	}
}

func TestLinkNoMatchKeepsUnresolved(t *testing.T) {
	repo := newFakeIdentityRepo()
	linker := NewSlackIdentityLinker(fakeIdentityGateway{emails: map[string]string{"U1": "ghost@x.example"}}, repo)

	_, reason, err := linker.Link(context.Background(), "U1")
	if err != nil || reason != LinkReasonNoMatch {
		t.Fatalf("reason=%q err=%v, want %q", reason, err, LinkReasonNoMatch)
	}
}

func TestLinkAmbiguousEmailNeverGuesses(t *testing.T) {
	repo := newFakeIdentityRepo()
	repo.byEmail["dup@x.example"] = []uuid.UUID{uuid.New(), uuid.New()} // case-variant duplicates
	linker := NewSlackIdentityLinker(fakeIdentityGateway{emails: map[string]string{"U1": "dup@x.example"}}, repo)

	_, reason, err := linker.Link(context.Background(), "U1")
	if err != nil || reason != LinkReasonAmbiguous {
		t.Fatalf("reason=%q err=%v, want %q", reason, err, LinkReasonAmbiguous)
	}
	if repo.linkCalls != 0 {
		t.Error("ambiguous match must not write anything")
	}
}

func TestLinkSlackEmailUnavailable(t *testing.T) {
	repo := newFakeIdentityRepo()
	// No email in profile.
	linker := NewSlackIdentityLinker(fakeIdentityGateway{emails: map[string]string{"U1": ""}}, repo)
	if _, reason, _ := linker.Link(context.Background(), "U1"); reason != LinkReasonEmailUnavailable {
		t.Errorf("empty email: reason=%q", reason)
	}
	// Slack says user gone.
	linker2 := NewSlackIdentityLinker(fakeIdentityGateway{err: ErrSlackUserNotFound}, repo)
	if _, reason, _ := linker2.Link(context.Background(), "U1"); reason != LinkReasonEmailUnavailable {
		t.Errorf("user gone: reason=%q", reason)
	}
}

func TestLinkTransientFailureIsRetryableReason(t *testing.T) {
	repo := newFakeIdentityRepo()
	linker := NewSlackIdentityLinker(fakeIdentityGateway{err: ErrSlackUnavailable}, repo)
	_, reason, err := linker.Link(context.Background(), "U1")
	if err != nil || reason != LinkReasonLookupFailed {
		t.Fatalf("reason=%q err=%v, want %q", reason, err, LinkReasonLookupFailed)
	}
}

func TestLinkDoesNotOverwriteExistingMapping(t *testing.T) {
	repo := newFakeIdentityRepo()
	userID := uuid.New()
	repo.byEmail["a@x.example"] = []uuid.UUID{userID}
	repo.linked[userID] = "UOTHER" // already bound to a different Slack account
	linker := NewSlackIdentityLinker(fakeIdentityGateway{emails: map[string]string{"U1": "a@x.example"}}, repo)

	_, reason, _ := linker.Link(context.Background(), "U1")
	if reason != LinkReasonUserMismatch {
		t.Fatalf("reason=%q, want user mismatch (no silent overwrite)", reason)
	}
	if repo.linked[userID] != "UOTHER" {
		t.Error("existing mapping was overwritten")
	}
}

func TestLinkSlackIDAlreadyOwnedByAnotherUser(t *testing.T) {
	repo := newFakeIdentityRepo()
	other, mine := uuid.New(), uuid.New()
	repo.byEmail["mine@x.example"] = []uuid.UUID{mine}
	repo.linked[other] = "U1"
	repo.takenBy["U1"] = other // unique index: U1 belongs to someone else
	linker := NewSlackIdentityLinker(fakeIdentityGateway{emails: map[string]string{"U1": "mine@x.example"}}, repo)

	_, reason, _ := linker.Link(context.Background(), "U1")
	if reason != LinkReasonIDTaken {
		t.Fatalf("reason=%q, want slack_id_taken", reason)
	}
}

func TestLinkIdempotentOnRepeat(t *testing.T) {
	repo := newFakeIdentityRepo()
	userID := uuid.New()
	repo.byEmail["s@x.example"] = []uuid.UUID{userID}
	linker := NewSlackIdentityLinker(fakeIdentityGateway{emails: map[string]string{"U9": "s@x.example"}}, repo)

	if _, reason, err := linker.Link(context.Background(), "U9"); err != nil || reason != "" {
		t.Fatalf("first: reason=%q err=%v", reason, err)
	}
	// Second link (e.g. a later duplicate event): already-linked is success.
	linkedID, reason, err := linker.Link(context.Background(), "U9")
	if err != nil || reason != "" || linkedID != userID {
		t.Fatalf("second: linkedID=%v reason=%q err=%v", linkedID, reason, err)
	}
}

func TestLinkReasonsNeverContainEmailPII(t *testing.T) {
	repo := newFakeIdentityRepo()
	linker := NewSlackIdentityLinker(fakeIdentityGateway{emails: map[string]string{"U1": "private@x.example"}}, repo)
	_, reason, err := linker.Link(context.Background(), "U1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strings.Contains(reason, "private@x.example") || strings.Contains(reason, "@") {
		t.Errorf("reason leaks PII: %q", reason)
	}
}

func TestLinkInfraErrorSurfaces(t *testing.T) {
	linker := NewSlackIdentityLinker(fakeIdentityGateway{emails: map[string]string{"U1": "x@y.z"}}, failingIdentityRepo{})
	if _, _, err := linker.Link(context.Background(), "U1"); err == nil || !errors.Is(err, errFakeRepo) {
		t.Fatalf("infra error must surface, got %v", err)
	}
}

type failingIdentityRepo struct{}

var errFakeRepo = errors.New("fake repo down")

func (failingIdentityRepo) FindUserIDsByEmail(context.Context, string) ([]uuid.UUID, error) {
	return nil, errFakeRepo
}
func (failingIdentityRepo) LinkSlackUser(context.Context, uuid.UUID, string) (LinkState, error) {
	return "", errFakeRepo
}
