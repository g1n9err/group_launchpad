package integrations

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"launchpad/internal/core"
)

// ── test doubles ───────────────────────────────────────────────────────────

type fakeSlackGateway struct {
	workspace coreWorkspace
	err       error
	calls     int
}

type coreWorkspace = SlackWorkspace

var fakeConnectTime = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// fakeSlackGateway replaces Slack for connection tests (NOT a real client).
// Channel-operation methods are stubs: they are exercised by the separate
// fakeChannelGateway in slack_channels_test.go.
func (g fakeSlackGateway) LookupChannelIDs(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (g fakeSlackGateway) CreateChannel(context.Context, string) (string, error) {
	return "", ErrSlackUnavailable
}
func (g fakeSlackGateway) LookupUserByEmail(context.Context, string) (string, error) {
	return "", ErrSlackUserNotFound
}
func (g fakeSlackGateway) InviteUserToChannel(context.Context, string, string) (InviteOutcome, error) {
	return "", ErrSlackUnavailable
}
func (g fakeSlackGateway) RemoveUserFromChannel(context.Context, string, string) error { return nil }
func (g fakeSlackGateway) PostMessage(context.Context, string, string) (string, error) {
	return "", ErrSlackUnavailable
}

func (g fakeSlackGateway) UserEmail(context.Context, string) (string, error) {
	return "", ErrSlackUserNotFound
}

func (g *fakeSlackGateway) AuthTest(context.Context) (SlackWorkspace, error) {
	g.calls++
	return g.workspace, g.err
}

type fakeSlackRepo struct {
	mu         sync.Mutex
	upserts    []string // team_ids
	botIDs     []string
	markedErr  bool
	operations []ExternalOperation
}

func (r *fakeSlackRepo) UpsertSlackConnection(_ context.Context, teamID, botUserID string) (SlackStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upserts = append(r.upserts, teamID)
	r.botIDs = append(r.botIDs, botUserID)
	return SlackStatus{Status: StatusConnected, TeamID: teamID, ConnectedAt: fakeConnectTime}, nil
}

func (r *fakeSlackRepo) MarkSlackError(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markedErr = true
	return nil
}

func (r *fakeSlackRepo) RecordExternalOperation(_ context.Context, op ExternalOperation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.operations = append(r.operations, op)
	return nil
}

// ── tests ──────────────────────────────────────────────────────────────────

func TestSlackNotConfiguredRefuses(t *testing.T) {
	svc := NewSlackAuthService(nil, nil, false)
	_, err := svc.CheckConnection(context.Background())
	appErr, ok := err.(*core.AppError)
	if !ok || appErr.Code != core.CodeNotReady {
		t.Fatalf("expected NOT_READY, got %v", err)
	}
}

func TestSlackCheckSuccessPersistsAndReports(t *testing.T) {
	repo := &fakeSlackRepo{}
	gw := &fakeSlackGateway{workspace: SlackWorkspace{TeamID: "T999", TeamName: "Launchpad WS"}}
	svc := NewSlackAuthService(repo, gw, true)

	st, err := svc.CheckConnection(context.Background())
	if err != nil {
		t.Fatalf("CheckConnection: %v", err)
	}
	if st.Status != StatusConnected || st.TeamID != "T999" || st.TeamName != "Launchpad WS" {
		t.Errorf("status = %+v", st)
	}
	if len(repo.upserts) != 1 || repo.upserts[0] != "T999" {
		t.Errorf("upserts = %v", repo.upserts)
	}
	if len(repo.operations) != 1 {
		t.Fatalf("op-log entries = %d, want 1", len(repo.operations))
	}
	if op := repo.operations[0]; op.Provider != ProviderSlack || op.Operation != SlackOperationAuthTest || op.Error != "" {
		t.Errorf("op = %+v", op)
	}
}

func TestSlackCheckIsIdempotent(t *testing.T) {
	repo := &fakeSlackRepo{}
	gw := &fakeSlackGateway{workspace: SlackWorkspace{TeamID: "T999", TeamName: "WS"}}
	svc := NewSlackAuthService(repo, gw, true)

	for i := 1; i <= 3; i++ {
		st, err := svc.CheckConnection(context.Background())
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if st.Status != StatusConnected {
			t.Fatalf("check %d status = %q", i, st.Status)
		}
	}
	if len(repo.upserts) != 3 {
		t.Errorf("expected 3 safe re-upserts, got %d", len(repo.upserts))
	}
	if len(repo.operations) != 3 {
		t.Errorf("each check logs one operation, got %d", len(repo.operations))
	}
}

func TestSlackAuthRejectedPersistsError(t *testing.T) {
	repo := &fakeSlackRepo{}
	gw := &fakeSlackGateway{err: ErrSlackAuthRejected}
	svc := NewSlackAuthService(repo, gw, true)

	st, err := svc.CheckConnection(context.Background())
	if err != nil {
		t.Fatalf("auth rejection is a status, not an error: %v", err)
	}
	if st.Status != StatusError {
		t.Errorf("status = %q, want error", st.Status)
	}
	if !repo.markedErr {
		t.Error("auth rejection must be persisted as error")
	}
	if len(repo.upserts) != 0 {
		t.Errorf("no upsert on rejection, got %v", repo.upserts)
	}
	if op := repo.operations[0]; op.Error != "auth_rejected" {
		t.Errorf("op category = %q", op.Error)
	}
}

func TestSlackNetworkFailureIsTransientOnly(t *testing.T) {
	repo := &fakeSlackRepo{}
	gw := &fakeSlackGateway{err: ErrSlackUnavailable}
	svc := NewSlackAuthService(repo, gw, true)

	st, err := svc.CheckConnection(context.Background())
	if err != nil {
		t.Fatalf("transient failure is a status, not an error: %v", err)
	}
	if st.Status != StatusError {
		t.Errorf("status = %q, want error", st.Status)
	}
	if repo.markedErr {
		t.Error("network failure must NOT persist an error state")
	}
	if op := repo.operations[0]; op.Error != "unreachable" {
		t.Errorf("op category = %q, want unreachable", op.Error)
	}
}

func TestSlackStatusNeverCarriesSecretMaterial(t *testing.T) {
	// The service struct is built without any token: the only token in the
	// system lives inside the adapter. Assert the status type carries only
	// known-safe fields by round-tripping through its JSON shape.
	repo := &fakeSlackRepo{}
	gw := &fakeSlackGateway{workspace: SlackWorkspace{TeamID: "T1", TeamName: "WS"}}
	svc := NewSlackAuthService(repo, gw, true)
	st, _ := svc.CheckConnection(context.Background())

	b, err := jsonMarshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"xoxb", "token", "secret"} {
		if strings.Contains(strings.ToLower(string(b)), forbidden) {
			t.Errorf("status JSON contains %q: %s", forbidden, b)
		}
	}
	if !strings.Contains(string(b), "T1") {
		t.Errorf("teamId should be present: %s", b)
	}
}

// jsonMarshal keeps the no-leak assertion explicit about what it inspects.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
