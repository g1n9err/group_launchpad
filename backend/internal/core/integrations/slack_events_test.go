package integrations

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

// ── fakes ──────────────────────────────────────────────────────────────────

type fakeVerifier struct{ accept bool }

func (f fakeVerifier) Verify(timestamp, rawBody, signature string) bool {
	return f.accept
}

type fakeEventRepo struct {
	mu        sync.Mutex
	events    map[string]string // event_id → final status ("" while pending)
	dups      int
	members   map[uuid.UUID]string // memberID → onboarding status
	markCalls int

	botID       string
	channels    map[string]uuid.UUID       // slack channel ID → group
	users       map[string]uuid.UUID       // slack user ID → user
	byEmail     map[string][]uuid.UUID     // normalized email → user IDs
	linkedUsers map[uuid.UUID]bool         // users linked during THIS test run
	membership  map[[2]uuid.UUID]uuid.UUID // (group,user) → member
}

func newFakeEventRepo() *fakeEventRepo {
	return &fakeEventRepo{
		events:      map[string]string{},
		members:     map[uuid.UUID]string{},
		channels:    map[string]uuid.UUID{},
		users:       map[string]uuid.UUID{},
		byEmail:     map[string][]uuid.UUID{},
		linkedUsers: map[uuid.UUID]bool{},
		membership:  map[[2]uuid.UUID]uuid.UUID{},
	}
}

func (r *fakeEventRepo) InsertSlackEvent(_ context.Context, ev RecordedSlackEvent) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, seen := r.events[ev.EventID]; seen {
		r.dups++
		return false, nil
	}
	r.events[ev.EventID] = ""
	return true, nil
}

func (r *fakeEventRepo) CompleteSlackEvent(_ context.Context, eventID, status, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[eventID] = status + reason
	return nil
}

func (r *fakeEventRepo) SlackBotUserID(context.Context) (string, error) { return r.botID, nil }

func (r *fakeEventRepo) FindGroupIDBySlackChannel(_ context.Context, channelID string) (uuid.UUID, bool, error) {
	g, ok := r.channels[channelID]
	return g, ok, nil
}

func (r *fakeEventRepo) FindUserIDBySlack(_ context.Context, slackUserID string) (uuid.UUID, bool, error) {
	u, ok := r.users[slackUserID]
	return u, ok, nil
}

func (r *fakeEventRepo) FindGroupMember(_ context.Context, groupID, userID uuid.UUID) (uuid.UUID, string, bool, error) {
	m, ok := r.membership[[2]uuid.UUID{groupID, userID}]
	if !ok {
		return uuid.Nil, "", false, nil
	}
	return m, r.members[m], true, nil
}

func (r *fakeEventRepo) MarkGroupMemberJoinedSlack(_ context.Context, memberID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markCalls++
	r.members[memberID] = "joined_slack"
	return nil
}

// ── SlackIdentityRepository (for linking tests) ────────────────────────────

// emails: normalized email → user IDs (case variants allowed for ambiguity)
func (r *fakeEventRepo) FindUserIDsByEmail(_ context.Context, email string) ([]uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byEmail[email], nil
}

func (r *fakeEventRepo) LinkSlackUser(_ context.Context, userID uuid.UUID, slackUserID string) (LinkState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Unique-index simulation: this Slack ID already owned by another user.
	if owner, taken := r.users[slackUserID]; taken && owner != userID {
		return LinkStateTaken, nil
	}
	// Guarded update: user with an existing mapping never gets overwritten.
	for sid, owner := range r.users {
		if owner == userID {
			if sid == slackUserID {
				return LinkStateAlready, nil
			}
			return LinkStateMismatch, nil
		}
	}
	r.users[slackUserID] = userID
	r.linkedUsers[userID] = true
	return LinkStateLinked, nil
}

var fixedClock = func() time.Time { return time.Unix(1700000100, 0) } // 100s after ts 1700000000

func newEventsService(repo *fakeEventRepo, accept bool) *SlackEventsService {
	return NewSlackEventsService(fakeVerifier{accept: accept}, repo, nil, fixedClock, true)
}

func signedReq(body string) SlackEventRequest {
	return SlackEventRequest{Timestamp: "1700000000", Signature: "v0=fixture", RawBody: body}
}

func memberJoinBody(eventID, userID, channelID string) string {
	return fmt.Sprintf(`{"type":"event_callback","event_id":%q,"team_id":"T1","event":{"type":"member_joined_channel","user":%q,"channel":%q,"channel_type":"C"}}`, eventID, userID, channelID)
}

func seedMember(t *testing.T, r *fakeEventRepo) (groupID, userID, memberID uuid.UUID, channelID string) {
	t.Helper()
	groupID, userID, memberID = uuid.New(), uuid.New(), uuid.New()
	channelID = "CCHAN1"
	r.channels[channelID] = groupID
	r.users["USTU1"] = userID
	r.membership[[2]uuid.UUID{groupID, userID}] = memberID
	r.members[memberID] = "pending"
	return
}

// ── signature / replay gate ────────────────────────────────────────────────

func TestEventsRejectsInvalidOrMissingSignatureBeforeParsing(t *testing.T) {
	repo := newFakeEventRepo()
	svc := newEventsService(repo, false) // verifier denies

	// Even a perfectly-formed member event must not be recorded when the
	// signature fails — proving verification precedes parsing.
	req := signedReq(memberJoinBody("EvX", "USTU1", "CCHAN1"))
	_, err := svc.Handle(context.Background(), req)
	appErr, ok := err.(*core.AppError)
	if !ok || appErr.Code != core.CodeUnauthorized {
		t.Fatalf("want UNAUTHORIZED, got %v", err)
	}
	if len(repo.events) != 0 {
		t.Error("event recorded despite invalid signature")
	}

	// Missing headers.
	_, err = svc.Handle(context.Background(), SlackEventRequest{RawBody: "{}"})
	if appErr, ok := err.(*core.AppError); !ok || appErr.Code != core.CodeUnauthorized {
		t.Fatalf("missing headers: %v", err)
	}
	// Malformed timestamp.
	_, err = svc.Handle(context.Background(), SlackEventRequest{Timestamp: "not-a-number", Signature: "v0=x", RawBody: "{}"})
	if appErr, ok := err.(*core.AppError); !ok || appErr.Code != core.CodeUnauthorized {
		t.Fatalf("malformed ts: %v", err)
	}
}

func TestEventsRejectsStaleTimestamp(t *testing.T) {
	repo := newFakeEventRepo()
	svc := NewSlackEventsService(fakeVerifier{accept: true}, repo, nil, fixedClock, true)

	stale := SlackEventRequest{Timestamp: "1700000000", Signature: "v0=x", RawBody: "{}"}
	stale.Timestamp = fmt.Sprint(fixedClock().Unix() - 400) // > 300s old
	if _, err := svc.Handle(context.Background(), stale); err == nil {
		t.Fatal("stale request accepted")
	} else if appErr, ok := err.(*core.AppError); !ok || appErr.Code != core.CodeUnauthorized {
		t.Fatalf("stale: %v", err)
	}

	future := stale
	future.Timestamp = fmt.Sprint(fixedClock().Unix() + 400)
	if _, err := svc.Handle(context.Background(), future); err == nil {
		t.Fatal("future-dated request accepted")
	}
}

// ── url_verification ───────────────────────────────────────────────────────

func TestEventsURLVerificationHandshake(t *testing.T) {
	repo := newFakeEventRepo()
	svc := newEventsService(repo, true)

	ack, err := svc.Handle(context.Background(), signedReq(`{"type":"url_verification","challenge":"abc123-challenge"}`))
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if ack.Kind != "challenge" || ack.Challenge != "abc123-challenge" {
		t.Errorf("ack = %+v", ack)
	}
	if len(repo.events) != 0 {
		t.Error("challenge must never be persisted as an event")
	}
}

func TestEventsURLVerificationRequiresSignature(t *testing.T) {
	repo := newFakeEventRepo()
	svc := newEventsService(repo, false)
	_, err := svc.Handle(context.Background(), signedReq(`{"type":"url_verification","challenge":"x"}`))
	if appErr, ok := err.(*core.AppError); !ok || appErr.Code != core.CodeUnauthorized {
		t.Fatalf("unsigned handshake must 401, got %v", err)
	}
}

// ── dedup ──────────────────────────────────────────────────────────────────

func TestEventsDuplicateDeliveryAcksWithoutReprocessing(t *testing.T) {
	repo := newFakeEventRepo()
	seedMember(t, repo)
	svc := newEventsService(repo, true)
	body := memberJoinBody("EvDUP", "USTU1", "CCHAN1")

	if ack, _ := svc.Handle(context.Background(), signedReq(body)); ack.Kind != "processed" {
		t.Fatalf("first delivery: %+v", ack)
	}
	ack, err := svc.Handle(context.Background(), signedReq(body))
	if err != nil || ack.Kind != "duplicate" {
		t.Fatalf("duplicate delivery: %+v err=%v", ack, err)
	}
	if repo.markCalls != 1 {
		t.Errorf("member marked %d times, want exactly 1", repo.markCalls)
	}
}

func TestEventsConcurrentDuplicateDeliveries(t *testing.T) {
	repo := newFakeEventRepo()
	seedMember(t, repo)
	svc := newEventsService(repo, true)
	body := memberJoinBody("EvRACE", "USTU1", "CCHAN1")

	var wg sync.WaitGroup
	kinds := make([]string, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ack, err := svc.Handle(context.Background(), signedReq(body))
			if err != nil {
				kinds[i] = "err:" + err.Error()
				return
			}
			kinds[i] = ack.Kind
		}()
	}
	wg.Wait()

	processed, duplicated := 0, 0
	for _, k := range kinds {
		switch k {
		case "processed":
			processed++
		case "duplicate":
			duplicated++
		default:
			t.Errorf("unexpected outcome: %q", k)
		}
	}
	if processed != 1 || duplicated != 7 {
		t.Errorf("processed=%d duplicate=%d, want exactly 1/7", processed, duplicated)
	}
	if repo.markCalls != 1 {
		t.Errorf("member marked %d times under concurrency, want 1", repo.markCalls)
	}
}

// ── member_joined_channel processing ──────────────────────────────────────

func TestEventsMemberJoinMarksJoinedSlack(t *testing.T) {
	repo := newFakeEventRepo()
	_, _, memberID, _ := seedMember(t, repo)
	svc := newEventsService(repo, true)

	ack, err := svc.Handle(context.Background(), signedReq(memberJoinBody("EvOK", "USTU1", "CCHAN1")))
	if err != nil || ack.Kind != "processed" {
		t.Fatalf("ack=%+v err=%v", ack, err)
	}
	if repo.members[memberID] != "joined_slack" {
		t.Errorf("member status = %q", repo.members[memberID])
	}
	if status := repo.events["EvOK"]; !strings.HasPrefix(status, "processed") {
		t.Errorf("event status = %q", status)
	}
}

func TestEventsBotJoinNeverMarksMember(t *testing.T) {
	repo := newFakeEventRepo()
	_, _, memberID, _ := seedMember(t, repo)
	repo.botID = "UBOT1"
	// The bot joins the channel — Slack fires member_joined_channel for it.
	repo.users["UBOT1"] = uuid.New() // even if resolvable, it must classify as bot
	svc := newEventsService(repo, true)

	ack, err := svc.Handle(context.Background(), signedReq(memberJoinBody("EvBOT", "UBOT1", "CCHAN1")))
	if err != nil || ack.Kind != "bot" {
		t.Fatalf("ack=%+v err=%v", ack, err)
	}
	if repo.members[memberID] != "pending" {
		t.Error("bot join must not transition any member")
	}
	if repo.markCalls != 0 {
		t.Errorf("markCalls=%d, want 0", repo.markCalls)
	}
}

func TestEventsUnresolvableMappingsAreRecordedNotGuessed(t *testing.T) {
	tests := []struct {
		name   string
		user   string
		chann  string
		reason string
	}{
		{"unknown channel", "USTU1", "CUNKNOWN", "channel_not_linked"},
		{"unknown user", "UGHOST", "CCHAN1", "user_not_linked"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeEventRepo()
			seedMember(t, repo)
			svc := newEventsService(repo, true)

			ack, err := svc.Handle(context.Background(), signedReq(memberJoinBody("EvU1", tc.user, tc.chann)))
			if err != nil || ack.Kind != "unresolved" {
				t.Fatalf("ack=%+v err=%v", ack, err)
			}
			if status := repo.events["EvU1"]; status != "unresolved"+tc.reason {
				t.Errorf("status=%q want unresolved/%s", status, tc.reason)
			}
		})
	}

	t.Run("user not a member of that group", func(t *testing.T) {
		repo := newFakeEventRepo()
		seedMember(t, repo)
		repo.users["UOTHER"] = uuid.New() // known user, different group membership
		svc := newEventsService(repo, true)

		ack, err := svc.Handle(context.Background(), signedReq(memberJoinBody("EvU2", "UOTHER", "CCHAN1")))
		if err != nil || ack.Kind != "unresolved" {
			t.Fatalf("ack=%+v err=%v", ack, err)
		}
		if status := repo.events["EvU2"]; status != "unresolvedmember_not_in_group" {
			t.Errorf("status=%q", status)
		}
	})
}

func TestEventsUnknownEventTypeIgnoredButRecorded(t *testing.T) {
	repo := newFakeEventRepo()
	svc := newEventsService(repo, true)
	ack, err := svc.Handle(context.Background(), signedReq(
		`{"type":"event_callback","event_id":"EvIGN","event":{"type":"reaction_added","user":"U1"}}`))
	if err != nil || ack.Kind != "ignored" {
		t.Fatalf("ack=%+v err=%v", ack, err)
	}
	if status := repo.events["EvIGN"]; !strings.HasPrefix(status, "ignored") {
		t.Errorf("status=%q", status)
	}
}

func TestEventsUnconfiguredRefuses(t *testing.T) {
	svc := NewSlackEventsService(nil, nil, nil, fixedClock, false)
	if _, err := svc.Handle(context.Background(), signedReq("{}")); err == nil {
		t.Fatal("expected NOT_READY")
	}
}

// ── identity linking through the event pipeline (Stage 3A) ────────────────

// identityGateway is a SlackGateway fake whose only live method is UserEmail.
type identityGateway struct{ emails map[string]string }

func (identityGateway) AuthTest(context.Context) (SlackWorkspace, error) {
	return SlackWorkspace{}, nil
}
func (identityGateway) LookupChannelIDs(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (identityGateway) CreateChannel(context.Context, string) (string, error)     { return "", nil }
func (identityGateway) LookupUserByEmail(context.Context, string) (string, error) { return "", nil }
func (identityGateway) InviteUserToChannel(context.Context, string, string) (InviteOutcome, error) {
	return "", nil
}
func (identityGateway) PostMessage(context.Context, string, string) (string, error) { return "", nil }
func (g identityGateway) UserEmail(_ context.Context, slackUserID string) (string, error) {
	if email, ok := g.emails[slackUserID]; ok {
		return email, nil
	}
	return "", ErrSlackUserNotFound
}

// seedUnlinkedMember creates group+channel+member where the member has an
// email on file but NO Slack ID yet.
func seedUnlinkedMember(t *testing.T, r *fakeEventRepo, email string) (channelID, slackUserID string) {
	t.Helper()
	groupID, userID, memberID := uuid.New(), uuid.New(), uuid.New()
	channelID, slackUserID = "CNEW1", "UNEW1"
	r.channels[channelID] = groupID
	r.byEmail[NormalizeEmail(email)] = []uuid.UUID{userID}
	r.membership[[2]uuid.UUID{groupID, userID}] = memberID
	r.members[memberID] = "pending"
	return
}

func TestEventsLinkUnlinkedUserViaEmailThenProcess(t *testing.T) {
	repo := newFakeEventRepo()
	channelID, slackUserID := seedUnlinkedMember(t, repo, "New.Student@X.Example")
	linker := NewSlackIdentityLinker(identityGateway{emails: map[string]string{
		slackUserID: "new.student@x.example", // different case: normalization must bridge
	}}, repo)
	svc := NewSlackEventsService(fakeVerifier{accept: true}, repo, linker, fixedClock, true)

	ack, err := svc.Handle(context.Background(), signedReq(memberJoinBody("EvLINK1", slackUserID, channelID)))
	if err != nil || ack.Kind != "processed" {
		t.Fatalf("ack=%+v err=%v — linking did not complete the pipeline", ack, err)
	}
	// The member must now be joined_slack (find member by linked user).
	var status string
	for m, s := range repo.members {
		_ = m
		status = s
	}
	if status != "joined_slack" {
		t.Errorf("member status = %q, want joined_slack", status)
	}
	if got := repo.events["EvLINK1"]; got != "processed" {
		t.Errorf("event status = %q", got)
	}
}

func TestEventsLinkFailureReasonsAreDurableAndSpecific(t *testing.T) {
	tests := []struct {
		name       string
		email      string // what Slack returns for the user
		ambiguous  bool
		wantReason string
	}{
		{"no internal match", "stranger@x.example", false, LinkReasonNoMatch},
		{"ambiguous duplicates", "dup@x.example", true, LinkReasonAmbiguous},
		{"no email exposed", "", false, LinkReasonEmailUnavailable},
		{"slack unreachable", "☠transient", false, LinkReasonLookupFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeEventRepo()
			channelID, slackUserID := seedUnlinkedMember(t, repo, "seed@x.example")
			if tc.ambiguous {
				// A second case-variant duplicate for the Slack-reported email.
				repo.byEmail["dup@x.example"] = []uuid.UUID{uuid.New(), uuid.New()}
			}
			gw := identityGateway{emails: map[string]string{slackUserID: tc.email}}
			linker := NewSlackIdentityLinker(gw, repo)
			if tc.wantReason == LinkReasonLookupFailed {
				linker = NewSlackIdentityLinker(transientGateway{}, repo)
			}
			svc := NewSlackEventsService(fakeVerifier{accept: true}, repo, linker, fixedClock, true)

			ack, err := svc.Handle(context.Background(), signedReq(memberJoinBody("EvR1", slackUserID, channelID)))
			if err != nil || ack.Kind != "unresolved" {
				t.Fatalf("ack=%+v err=%v", ack, err)
			}
			if got := repo.events["EvR1"]; got != "unresolved"+tc.wantReason {
				t.Errorf("ledger = %q, want unresolved/%s", got, tc.wantReason)
			}
		})
	}
}

type transientGateway struct{ identityGateway }

func (transientGateway) UserEmail(context.Context, string) (string, error) {
	return "", ErrSlackUnavailable
}

func TestEventsNilLinkerPreservesLegacyBehavior(t *testing.T) {
	repo := newFakeEventRepo()
	channelID, slackUserID := seedUnlinkedMember(t, repo, "seed@x.example")
	svc := NewSlackEventsService(fakeVerifier{accept: true}, repo, nil, fixedClock, true)

	ack, err := svc.Handle(context.Background(), signedReq(memberJoinBody("EvLEG", slackUserID, channelID)))
	if err != nil || ack.Kind != "unresolved" {
		t.Fatalf("ack=%+v err=%v", ack, err)
	}
	if got := repo.events["EvLEG"]; got != "unresolveduser_not_linked" {
		t.Errorf("legacy reason = %q", got)
	}
}

func TestEventsConcurrentJoinsLinkUserExactlyOnce(t *testing.T) {
	repo := newFakeEventRepo()
	// Two channels of the same group; the same unlinked user joins both.
	groupID, userID, memberID := uuid.New(), uuid.New(), uuid.New()
	repo.channels["CA"] = groupID
	repo.channels["CB"] = groupID
	repo.byEmail["racer@x.example"] = []uuid.UUID{userID}
	repo.membership[[2]uuid.UUID{groupID, userID}] = memberID
	repo.members[memberID] = "pending"

	linker := NewSlackIdentityLinker(identityGateway{emails: map[string]string{"URACE": "Racer@X.example"}}, repo)
	svc := NewSlackEventsService(fakeVerifier{accept: true}, repo, linker, fixedClock, true)

	bodyA := memberJoinBody("EvRA", "URACE", "CA")
	bodyB := memberJoinBody("EvRB", "URACE", "CB")

	var wg sync.WaitGroup
	kinds := make([]string, 2)
	for i, body := range []string{bodyA, bodyB} {
		wg.Add(1)
		go func(i int, body string) {
			defer wg.Done()
			ack, err := svc.Handle(context.Background(), signedReq(body))
			if err != nil {
				kinds[i] = "err"
				return
			}
			kinds[i] = ack.Kind
		}(i, body)
	}
	wg.Wait()

	for i, k := range kinds {
		if k != "processed" {
			t.Errorf("concurrent event %d outcome = %q, want processed", i, k)
		}
	}
	// Exactly one Slack mapping for the user despite the race.
	linked := repo.users["URACE"]
	if linked == uuid.Nil {
		t.Fatal("user was never linked")
	}
	count := 0
	for _, owner := range repo.users {
		if owner == userID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("user has %d slack mappings, want exactly 1", count)
	}
}

func TestEventsErrorsNeverLeakSecretMaterial(t *testing.T) {
	repo := newFakeEventRepo()
	svc := newEventsService(repo, false)
	_, err := svc.Handle(context.Background(), signedReq(`{"type":"url_verification"}`))
	msg := err.Error()
	for _, forbidden := range []string{"signing", "secret", "xoxb", "token"} {
		if strings.Contains(strings.ToLower(msg), forbidden) {
			t.Errorf("error leaks %q: %s", forbidden, msg)
		}
	}
}
