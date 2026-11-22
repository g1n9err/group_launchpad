package transport_test

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"launchpad/internal/core/integrations"
	"launchpad/internal/jobs"
	"launchpad/internal/store"
	"launchpad/internal/transport"
)

type rosterDrive struct {
	fakeDrive
	mu      sync.Mutex
	grants  []string
	revoked []string
}

func (d *rosterDrive) GrantAccess(_ context.Context, _ string, folder string, email string, role string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.grants = append(d.grants, folder+"/"+email+"/"+role)
	return nil
}
func (d *rosterDrive) RevokeAccess(_ context.Context, _, folder, email string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.revoked = append(d.revoked, folder+"/"+email)
	return nil
}

type rosterClassroom struct {
	fakeClassroom
	mu       sync.Mutex
	teachers []string
	removed  []string
}

func (g *rosterClassroom) AssignTeacher(_ context.Context, _, course, email string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.teachers = append(g.teachers, course+"/"+email)
	return nil
}
func (g *rosterClassroom) RemoveTeacher(_ context.Context, _, course, email string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removed = append(g.removed, course+"/"+email)
	return nil
}
func (g *rosterClassroom) RemoveStudent(_ context.Context, _, course, email string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removed = append(g.removed, course+"/"+email)
	return nil
}

func TestRosterUpdateSyncsExistingGoogleResources(t *testing.T) {
	pool := newTestPool(t)
	truncate(t, pool, "groups", "users", "river_job")
	repo := store.New(pool)
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	drive := &rosterDrive{}
	classroom := &rosterClassroom{}
	app := transport.New(testConfig(), discardLogger(), newGroupsService(pool), googleServiceForTest(pool),
		slackServiceForTest(pool), slackChannelsServiceForTest(pool), slackEventsServiceForTest(pool), nil, pool.Ping,
		transport.GroupProvisioning{
			GroupRoster: repo, GroupStaff: repo,
			Drive: &integrations.DriveService{Tokens: drive, Gateway: drive, Store: repo, Operations: repo},
			Classroom: &integrations.ClassroomService{
				Tokens: classroom, Gateway: classroom, Store: repo, Operations: repo,
				QueueJoinEmails: jobs.QueueClassroomJoinEmails(repo, client),
			},
		})
	srv := httptestNew(app, t)
	code, group := doJSON(t, srv, http.MethodPost, "/api/groups",
		`{"name":"Roster Sync","courseName":"Go","startDate":"2026-12-01","staffEmails":["ta@example.test"],"lecturerEmails":["old@example.test"],"studentEmails":["student@example.test"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	id := group["id"].(string)
	if _, err := pool.Exec(t.Context(), `INSERT INTO group_resources (group_id,provider,resource_type,external_resource_id,resource_name)
		VALUES ($1,'google_drive','folder','drive-root','root'),
		($1,'google_drive','folder','drive-general','root/General'),
		($1,'google_classroom','course','class-1','p:launchpad-' || $1::uuid::text)`, id); err != nil {
		t.Fatal(err)
	}
	code, result := doJSON(t, srv, http.MethodPost, "/api/groups/"+id+"/roster",
		`{"lecturerEmails":["lecturer@example.test"]}`)
	if code != http.StatusOK || result["driveAccess"] != "ready" || result["classroomAccess"] != "ready" || result["classroomEmail"] != "ready" {
		t.Fatalf("roster sync: %d %v", code, result)
	}
	drive.mu.Lock()
	grants := slices.Clone(drive.grants)
	drive.mu.Unlock()
	wantGrants := []string{"drive-root/lecturer@example.test/writer", "drive-general/lecturer@example.test/writer", "drive-root/ta@example.test/writer", "drive-general/ta@example.test/writer", "drive-general/student@example.test/reader"}
	if !slices.Equal(grants, wantGrants) {
		t.Fatalf("Drive grants: %v", grants)
	}
	classroom.mu.Lock()
	teachers := slices.Clone(classroom.teachers)
	classroom.mu.Unlock()
	if !slices.Equal(teachers, []string{"class-1/lecturer@example.test", "class-1/ta@example.test"}) || classroom.requests.Load() != 0 {
		t.Fatalf("Classroom teachers: %v, create requests: %d", teachers, classroom.requests.Load())
	}
	var recipients []string
	err = pool.QueryRow(t.Context(), "SELECT array_agg(recipient ORDER BY recipient) FROM classroom_join_deliveries WHERE group_id=$1", id).Scan(&recipients)
	if err != nil || !slices.Equal(recipients, []string{"lecturer@example.test", "student@example.test", "ta@example.test"}) {
		t.Fatalf("queued recipients = %v, err=%v", recipients, err)
	}
	code, result = doJSON(t, srv, http.MethodPost, "/api/groups/"+id+"/roster", `{"studentEmails":[],"lecturerEmails":[],"taEmails":[]}`)
	if code != http.StatusOK || result["driveRemoval"] != "ready" || result["classroomRemoval"] != "ready" || result["slackRemoval"] != "ready" {
		t.Fatalf("clear roster and revoke access: %d %v", code, result)
	}
	report, err := newGroupsService(pool).Onboarding(t.Context(), id)
	if err != nil || len(report.Students) != 0 {
		t.Fatalf("students after clear = %d, err=%v", len(report.Students), err)
	}
	staff, err := repo.GroupStaff(t.Context(), uuid.MustParse(id))
	if err != nil || len(staff) != 0 {
		t.Fatalf("staff after clear = %v, err=%v", staff, err)
	}
	var cancelled int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM classroom_join_deliveries WHERE group_id=$1 AND status='cancelled'`, id).Scan(&cancelled); err != nil || cancelled != 3 {
		t.Fatalf("cancelled Classroom join-code emails = %d, err=%v; want 3", cancelled, err)
	}
	classroom.mu.Lock()
	removedMembers := slices.Clone(classroom.removed)
	classroom.mu.Unlock()
	if !slices.Equal(removedMembers, []string{"class-1/old@example.test", "class-1/student@example.test", "class-1/lecturer@example.test", "class-1/ta@example.test"}) {
		t.Fatalf("Classroom removals = %v", removedMembers)
	}
}
