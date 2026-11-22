package jobs_test

// Per-platform failure notification tests (PRD §4.9): Slack invitation
// failures, Classroom/Drive/Forms provisioning failures create coordinator
// notifications with job-scoped dedupe keys, so River retries never
// duplicate rows. All gateways are fakes — no external calls.

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
	"launchpad/internal/jobs"
	"launchpad/internal/store"
)

type notifSlackGW struct{ inviteFails int }

func (f *notifSlackGW) Provision(_ context.Context, in integrations.ProvisionInput) (integrations.ProvisionResult, error) {
	result := integrations.ProvisionResult{
		Channels: []integrations.ChannelResult{{Name: "n-general", ChannelID: "C1", Status: "created"}},
	}
	for i, email := range in.InviteEmails {
		status := string(integrations.InviteOutcomeInvited)
		if i < f.inviteFails {
			status = integrations.InviteFailed
		}
		result.Invites = append(result.Invites, integrations.InviteResult{
			Email: email, ChannelName: "n-general", Status: status,
		})
	}
	return result, nil
}

type failingClassroomGW struct{}

func (failingClassroomGW) EnsureCourse(context.Context, string, integrations.ClassroomCourseInput) (string, error) {
	return "", integrations.ErrSlackUnavailable // any typed error
}
func (failingClassroomGW) ActivateCourse(context.Context, string, string) error        { return nil }
func (failingClassroomGW) EnrollStudent(context.Context, string, string, string) error { return nil }
func (failingClassroomGW) AssignTeacher(context.Context, string, string, string) error { return nil }
func (failingClassroomGW) RemoveStudent(context.Context, string, string, string) error { return nil }
func (failingClassroomGW) RemoveTeacher(context.Context, string, string, string) error { return nil }

type failingDriveGW struct{}

func (failingDriveGW) GenerateFolderID(context.Context, string) (string, error) {
	return uuid.NewString(), nil
}
func (failingDriveGW) EnsureFolder(_ context.Context, _ string, _ string, _, _ string) error {
	return nil
}
func (failingDriveGW) GrantAccess(context.Context, string, string, string, string) error {
	return nil
}
func (failingDriveGW) RevokeAccess(context.Context, string, string, string) error { return nil }

type failingFormsGW struct{}

func (failingFormsGW) CreateDraft(context.Context, string, string, string) (string, bool, error) {
	return "", false, integrations.ErrSlackUnavailable
}
func (failingFormsGW) FindDraft(_ context.Context, _, _ string) (string, error) {
	return "", integrations.ErrSlackUnavailable
}
func (failingFormsGW) EnsureQuestions(context.Context, string, string, []integrations.IntakeQuestion, bool) error {
	return nil
}
func (failingFormsGW) EnableVerifiedEmail(context.Context, string, string) error   { return nil }
func (failingFormsGW) VerifiedEmail(context.Context, string, string) (bool, error) { return true, nil }
func (failingFormsGW) ListResponses(context.Context, string, string, time.Time, string) ([]integrations.IntakeResponse, string, error) {
	return nil, "", nil
}
func (failingFormsGW) Publish(context.Context, string, string) (string, error) {
	return "https://forms.example/fake", nil
}

func TestLaunchEmitsPlatformFailureNotifications(t *testing.T) {
	pool := testPool(t)
	repo := store.New(pool)
	groupsSvc := groups.New(repo)

	group, err := groupsSvc.Create(context.Background(), groups.CreateInput{
		Name: "Notify Failures", CourseName: "Course", StartDate: "2026-12-01",
		StudentEmails:  []string{"student1@test.example", "student2@test.example"},
		StaffEmails:    []string{"staff@test.example"},
		LecturerEmails: []string{"lecturer@test.example"},
	})
	if err != nil {
		t.Fatal(err)
	}

	classroomSvc := &integrations.ClassroomService{
		Tokens: fakeTokenSource{}, Gateway: failingClassroomGW{}, Store: repo, Operations: noopRecorder{},
	}
	driveSvc := &integrations.DriveService{
		Tokens: fakeTokenSource{}, Gateway: failingDriveGW{}, Store: repo, Operations: noopRecorder{},
	}
	formsSvc := &integrations.FormsService{
		Tokens: fakeTokenSource{}, Gateway: failingFormsGW{}, Store: repo, Operations: noopRecorder{},
	}
	worker := &jobs.GroupLaunchWorker{
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		SlackProvisioner: &notifSlackGW{inviteFails: 1},
		SlackConfigured:  true,
		ClassroomService: classroomSvc,
		DriveService:     driveSvc,
		FormsService:     formsSvc,
		GroupsService:    groupsSvc,
		StaffAssignments: repo,
		ResultStore:      repo,
		Notifications:    repo,
	}
	job := &river.Job[jobs.GroupLaunchArgs]{
		JobRow: &rivertype.JobRow{ID: 4242, Kind: "group_launch", State: rivertype.JobStateRunning},
		Args:   jobs.GroupLaunchArgs{GroupID: group.ID},
	}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("Work: %v", err)
	}

	kinds := map[string]string{}
	rows, err := pool.Query(context.Background(),
		`SELECT kind, COALESCE(dedupe_key,'') FROM notifications WHERE group_id = $1`, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, key string
		if err := rows.Scan(&kind, &key); err != nil {
			t.Fatal(err)
		}
		kinds[kind] = key
	}
	// Expected: overall result + one per failed platform.
	for _, kind := range []string{
		"launch_partially_failed", // overall outcome
		"invite_failure",          // 1 Slack invitation failed
		"classroom_failure",       // course provisioning failed
		"forms_failure",           // intake form provisioning failed
	} {
		if _, ok := kinds[kind]; !ok {
			t.Errorf("missing notification kind %q; got %v", kind, kinds)
		}
	}
	// Dedupe keys are stable and job-scoped.
	if key := kinds["invite_failure"]; key != "group_launch:4242:invite_failure" {
		t.Errorf("invite_failure dedupe key = %q", key)
	}
	if key := kinds["launch_partially_failed"]; key != "group_launch:4242" {
		t.Errorf("launch dedupe key = %q", key)
	}
	// No PII: notification details must not contain any email address.
	var detail string
	if err := pool.QueryRow(context.Background(),
		`SELECT detail FROM notifications WHERE group_id = $1 AND kind = 'invite_failure'`, group.ID).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail == "" || containsAny(detail, "student1@test.example", "staff@test.example", "lecturer@test.example") {
		t.Errorf("invite_failure detail leaks PII or is empty: %q", detail)
	}

	// Simulating a River retry of the SAME job must not duplicate rows.
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("Work retry: %v", err)
	}
	var total int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM notifications WHERE group_id = $1`, group.ID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != len(kinds) {
		t.Errorf("notifications after retry = %d, want %d (deduped)", total, len(kinds))
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
