package audit_test

// Membership-audit aggregation tests (PRD §4.7): without a live member
// checker the audit never reports a false "ok"; an empty student roster
// surfaces as unlinked.

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"launchpad/internal/core/audit"
)

type fakeInvStore struct {
	inventory audit.Inventory
	saved     []audit.Snapshot
}

func (s *fakeInvStore) AuditInventory(context.Context, uuid.UUID) (audit.Inventory, error) {
	return s.inventory, nil
}

func (s *fakeInvStore) SaveAudit(_ context.Context, _ int64, snapshot audit.Snapshot, _ []audit.Resource, _ bool) (uuid.UUID, error) {
	s.saved = append(s.saved, snapshot)
	return uuid.Nil, nil
}

func runAudit(t *testing.T, svc *audit.Service, groupID uuid.UUID, jobID int64) audit.Snapshot {
	t.Helper()
	if _, err := svc.Run(context.Background(), groupID, jobID, "daily_sync", false); err != nil {
		t.Fatalf("run: %v", err)
	}
	store := svc.Store.(*fakeInvStore)
	if len(store.saved) != 1 {
		t.Fatalf("snapshots saved = %d, want 1", len(store.saved))
	}
	return store.saved[0]
}

func resultStatus(snapshot audit.Snapshot, kind, key string) string {
	for _, r := range snapshot.Results {
		if r.Kind == kind && r.Key == key {
			return r.Status
		}
	}
	return ""
}

func TestMembershipAuditWithoutCheckerFailsClosed(t *testing.T) {
	store := &fakeInvStore{inventory: audit.Inventory{
		GroupName: "Fail Closed Group",
		Resources: []audit.Resource{{Provider: "slack", Kind: "channel", Key: "g-general", ID: "C1"}},
		Members:   []audit.ExpectedMember{{Role: "teaching_staff", Email: "staff@test.example"}},
		Roster:    audit.RosterSummary{Expected: 3, Joined: 1},
	}}
	svc := &audit.Service{Store: store} // no checkers, no member checker
	snapshot := runAudit(t, svc, uuid.New(), 1)
	if got := resultStatus(snapshot, "membership", "teaching_staff"); got != audit.StatusUnavailable {
		t.Errorf("membership without checker = %s, want unavailable", got)
	}
	if got := resultStatus(snapshot, "roster", "students"); got != audit.StatusOK {
		t.Errorf("roster with students = %s, want ok", got)
	}
}

func TestMembershipAuditEmptyRosterIsUnlinked(t *testing.T) {
	store := &fakeInvStore{inventory: audit.Inventory{GroupName: "No Students"}}
	svc := &audit.Service{Store: store}
	snapshot := runAudit(t, svc, uuid.New(), 2)
	if got := resultStatus(snapshot, "roster", "students"); got != audit.StatusUnlinked {
		t.Errorf("empty roster status = %s, want unlinked", got)
	}
}

func TestMembershipAuditWithoutChannelsIsUnlinked(t *testing.T) {
	store := &fakeInvStore{inventory: audit.Inventory{
		GroupName: "No Channels",
		Members:   []audit.ExpectedMember{{Role: "lecturer", Email: "lecturer@test.example"}},
	}}
	svc := &audit.Service{Store: store, Members: fakeMembers{ok: true}}
	snapshot := runAudit(t, svc, uuid.New(), 3)
	if got := resultStatus(snapshot, "membership", "lecturer"); got != audit.StatusUnlinked {
		t.Errorf("membership without channels = %s, want unlinked", got)
	}
}

type fakeMembers struct{ ok bool }

func (f fakeMembers) CheckMembers(context.Context, []audit.Resource, []audit.ExpectedMember) []audit.Result {
	status := audit.StatusMissingOrInaccessible
	if f.ok {
		status = audit.StatusOK
	}
	return []audit.Result{{Resource: audit.Resource{Provider: "slack", Kind: "membership", Key: "lecturer"}, Status: status}}
}
