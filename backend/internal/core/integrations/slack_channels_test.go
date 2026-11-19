package integrations

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// ── fakes ──────────────────────────────────────────────────────────────────

type fakeChannelGateway struct {
	mu            sync.Mutex
	existing      map[string]string // name → channelID (workspace state)
	existingLater map[string]string // returned by lookups INSTEAD of existing (race simulation)
	created       map[string]string // name → channelID (created by calls)
	createCalls   int
	posts         map[string][]string // channelID → message texts
	postsByChan   map[string]int
	inviteCalls   map[string]int    // channelID → count
	inviteMode    map[string]string // userID → "ok" | "already" | "fail"
	members       map[string]bool   // "channelID|userID" → in channel (Slack-like)
	users         map[string]string // email → userID
	lookups       int
	failList      bool
}

func newFakeChannelGateway() *fakeChannelGateway {
	return &fakeChannelGateway{
		existing: map[string]string{}, created: map[string]string{},
		posts: map[string][]string{}, postsByChan: map[string]int{},
		inviteCalls: map[string]int{}, inviteMode: map[string]string{}, users: map[string]string{},
	}
}

func (f *fakeChannelGateway) AuthTest(context.Context) (SlackWorkspace, error) {
	return SlackWorkspace{}, nil
}

func (f *fakeChannelGateway) LookupChannelIDs(_ context.Context, names []string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	source := f.existing
	if f.existingLater != nil {
		source = f.existingLater // race simulation: someone created mid-flight
	}
	out := map[string]string{}
	for _, n := range names {
		if id, ok := source[n]; ok {
			out[n] = id
		}
	}
	return out, nil
}

func (f *fakeChannelGateway) CreateChannel(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if _, exists := f.existing[name]; exists {
		return "", ErrSlackChannelNameTaken
	}
	id := "C" + name // deterministic fake channel ID
	f.existing[name] = id
	f.created[name] = id
	return id, nil
}

func (f *fakeChannelGateway) LookupUserByEmail(_ context.Context, email string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.users[email]; ok {
		return id, nil
	}
	return "", ErrSlackUserNotFound
}

func (f *fakeChannelGateway) UserEmail(_ context.Context, userID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for email, id := range f.users {
		if id == userID {
			return email, nil
		}
	}
	return "", ErrSlackUserNotFound
}

func (f *fakeChannelGateway) InviteUserToChannel(_ context.Context, channelID, userID string) (InviteOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inviteCalls[channelID]++
	// Model Slack: a user already in a channel yields already_in.
	memberKey := channelID + "|" + userID
	if f.members == nil {
		f.members = map[string]bool{}
	}
	if f.members[memberKey] {
		return InviteOutcomeAlreadyMember, nil
	}
	switch f.inviteMode[userID] {
	case "fail":
		return "", ErrSlackUnavailable
	case "already":
		f.members[memberKey] = true
		return InviteOutcomeAlreadyMember, nil
	default:
		f.members[memberKey] = true
		return InviteOutcomeInvited, nil
	}
}

func (f *fakeChannelGateway) PostMessage(_ context.Context, channelID, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postsByChan[channelID]++
	f.posts[channelID] = append(f.posts[channelID], text)
	return "1234567890.123456", nil
}

type fakeResourceRepo struct {
	mu   sync.Mutex
	rows map[string]GroupResource // key: provider|type|name
}

func newFakeResourceRepo() *fakeResourceRepo {
	return &fakeResourceRepo{rows: map[string]GroupResource{}}
}

func key(r GroupResource) string { return r.Provider + "|" + r.Type + "|" + r.Name }

func (f *fakeResourceRepo) GetResource(_ context.Context, _ uuid.UUID, provider, typ, name string) (GroupResource, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[provider+"|"+typ+"|"+name]
	return r, ok, nil
}

func (f *fakeResourceRepo) InsertResource(_ context.Context, _ uuid.UUID, r GroupResource) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(r)
	if _, exists := f.rows[k]; exists {
		return false, nil
	}
	f.rows[k] = r
	return true, nil
}

func (f *fakeResourceRepo) UpdateResourceResult(_ context.Context, _ uuid.UUID, r GroupResource) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[key(r)] = r
	return nil
}

type fakeOpRecorder struct{ ops []ExternalOperation }

func (f *fakeOpRecorder) RecordExternalOperation(_ context.Context, op ExternalOperation) error {
	f.ops = append(f.ops, op)
	return nil
}

func newChannelsService(gw *fakeChannelGateway, repo *fakeResourceRepo) *SlackChannelsService {
	return NewSlackChannelsService(gw, repo, &fakeOpRecorder{}, true)
}

func testInput(emails ...string) ProvisionInput {
	in := ProvisionInput{
		GroupID:    uuid.New(),
		GroupName:  "Python Backend 13",
		CourseName: "Full-Stack Python",
		StartDate:  "2026-12-01",
	}
	in.InviteEmails = append(in.InviteEmails, emails...)
	return in
}

// ── naming ─────────────────────────────────────────────────────────────────

func TestChannelNameConvention(t *testing.T) {
	tests := []struct{ group, suffix, want string }{
		{"Python Backend 13", "general", "python-backend-13-general"},
		{"  Data Science 7 ", "questions", "data-science-7-questions"},
		{"C++ & Go!", "resources", "c-go-resources"},
		{"Multiple    Spaces", "general", "multiple-spaces-general"},
	}
	for _, tc := range tests {
		got, err := ChannelName(tc.group, tc.suffix)
		if err != nil || got != tc.want {
			t.Errorf("ChannelName(%q,%q) = %q,%v — want %q", tc.group, tc.suffix, got, err, tc.want)
		}
	}
	if _, err := ChannelName("!!!", "general"); err == nil {
		t.Error("slug-less group name must be rejected")
	}
	long := strings.Repeat("a", 200)
	got, _ := ChannelName(long, "general")
	if len(got) != ChannelNameMaxLength {
		t.Errorf("long name not capped: len=%d", len(got))
	}
	if !strings.HasSuffix(got, "-general") {
		t.Errorf("suffix must survive truncation: %q", got)
	}
}

// ── provisioning ───────────────────────────────────────────────────────────

func TestProvisionCreatesAllFourChannelsAndWelcomes(t *testing.T) {
	gw, repo := newFakeChannelGateway(), newFakeResourceRepo()
	svc := newChannelsService(gw, repo)

	res, err := svc.Provision(context.Background(), testInput())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(res.Channels) != 4 || gw.createCalls != 4 {
		t.Fatalf("channels=%d createCalls=%d, want 4/4", len(res.Channels), gw.createCalls)
	}
	for _, c := range res.Channels {
		if c.Status != ChannelCreated || c.ChannelID == "" {
			t.Errorf("channel %+v", c)
		}
	}
	if len(res.Welcomes) != 4 {
		t.Fatalf("welcomes=%d, want 4", len(res.Welcomes))
	}
	for _, w := range res.Welcomes {
		if w.Status != WelcomePosted {
			t.Errorf("welcome %+v", w)
		}
	}
}

func TestProvisionSecondRunIsFullyIdempotent(t *testing.T) {
	gw, repo := newFakeChannelGateway(), newFakeResourceRepo()
	svc := newChannelsService(gw, repo)
	in := testInput("staff@x.example", "lecturer@x.example")
	gw.users["staff@x.example"] = "U1"
	gw.users["lecturer@x.example"] = "U2"

	if _, err := svc.Provision(context.Background(), in); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	createdFirst, invitesFirst, postsFirst := gw.createCalls, len(gw.inviteCalls), len(gw.posts)

	res, err := svc.Provision(context.Background(), in)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if gw.createCalls != createdFirst {
		t.Errorf("second run created %d extra channels", gw.createCalls-createdFirst)
	}
	if len(gw.posts) != postsFirst {
		t.Errorf("second run posted %d extra welcome messages", len(gw.posts)-postsFirst)
	}
	if len(gw.inviteCalls) != invitesFirst {
		t.Errorf("second run re-invited (already_in is fine, but count changed unexpectedly)")
	}
	for _, c := range res.Channels {
		if c.Status != ChannelReused {
			t.Errorf("second run channel status = %q, want reused", c.Status)
		}
	}
	for _, w := range res.Welcomes {
		if w.Status != WelcomeSkipped {
			t.Errorf("second run welcome status = %q, want skipped", w.Status)
		}
	}
	for _, iv := range res.Invites {
		if iv.Status != string(InviteOutcomeAlreadyMember) {
			t.Errorf("second run invite status = %q, want already_member", iv.Status)
		}
	}
}

func TestProvisionReusesPreExistingWorkspaceChannel(t *testing.T) {
	gw, repo := newFakeChannelGateway(), newFakeResourceRepo()
	gw.existing["python-backend-13-general"] = "CPREEXIST"
	svc := newChannelsService(gw, repo)

	res, _ := svc.Provision(context.Background(), testInput())
	for _, c := range res.Channels {
		if strings.HasSuffix(c.Name, "-general") {
			if c.Status != ChannelReused || c.ChannelID != "CPREEXIST" {
				t.Errorf("pre-existing channel not reused: %+v", c)
			}
		}
	}
	if gw.createCalls != 3 {
		t.Errorf("createCalls=%d, want 3 (general reused)", gw.createCalls)
	}
}

func TestProvisionNameTakenRaceIsRecovered(t *testing.T) {
	gw, repo := newFakeChannelGateway(), newFakeResourceRepo()
	// The general channel appears between the phase-1 lookup and the create
	// (a concurrent execution wins the race): initial lookup sees nothing,
	// create hits name_taken, the retry lookup finds it.
	gw.existingLater = map[string]string{"python-backend-13-general": "CRACE"}
	svc := newChannelsService(gw, repo)

	res, _ := svc.Provision(context.Background(), testInput())
	general := res.Channels[0]
	if general.Name != "python-backend-13-general" || general.Status != ChannelReused || general.ChannelID != "CRACE" {
		t.Errorf("name_taken race not recovered: %+v", general)
	}
}

func TestProvisionInvitesRecordPerUserFailures(t *testing.T) {
	gw, repo := newFakeChannelGateway(), newFakeResourceRepo()
	gw.users["known@x.example"] = "U1"
	gw.users["also@x.example"] = "U2"
	gw.inviteMode["U2"] = "fail"
	svc := newChannelsService(gw, repo)

	res, _ := svc.Provision(context.Background(), testInput("known@x.example", "missing@x.example", "also@x.example"))
	var ok, notFound, failed int
	for _, iv := range res.Invites {
		switch {
		case iv.Status == string(InviteOutcomeInvited):
			ok++
		case iv.Error == "user_not_found":
			notFound++
		case iv.Status == InviteFailed:
			failed++
		}
	}
	// known → 4 channels invited; missing → 4×user_not_found; also → 4×failed
	if ok != 4 || notFound != 4 || failed != 4 {
		t.Errorf("invites: ok=%d notFound=%d failed=%d, want 4/4/4", ok, notFound, failed)
	}
}

func TestProvisionUnconfiguredRefuses(t *testing.T) {
	svc := NewSlackChannelsService(nil, nil, nil, false)
	if _, err := svc.Provision(context.Background(), testInput()); err == nil {
		t.Fatal("expected NOT_READY")
	}
}
