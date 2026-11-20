package jobs_test

// Integration test for GroupLaunchWorker.Work() — one complete orchestration
// run against a real River queue and PostgreSQL, with FAKE platform gateways
// (no live Slack or Google APIs called, no external resources created).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
	"launchpad/internal/jobs"
	"launchpad/internal/store"
)

// ── Fake gateways (no external calls) ─────────────────────────────────────

type fakeSlackGW struct{}

func (f *fakeSlackGW) Provision(_ context.Context, _ integrations.ProvisionInput) (integrations.ProvisionResult, error) {
	return integrations.ProvisionResult{
		Channels: []integrations.ChannelResult{
			{Name: "test-general", ChannelID: "C1", Status: "created"},
		},
	}, nil
}

type fakeClassroomGW struct{}

func (f *fakeClassroomGW) EnsureCourse(_ context.Context, _ string, _ integrations.ClassroomCourseInput) (string, error) {
	return "fake-course-123", nil
}

func (f *fakeClassroomGW) EnrollStudent(_ context.Context, _, _, email string) error {
	if email == "bad@test.example" {
		return errors.New("enrollment rejected")
	}
	return nil
}

func (f *fakeClassroomGW) AssignTeacher(_ context.Context, _, _, _ string) error { return nil }

type fakeDriveGW struct{}

func (f *fakeDriveGW) GenerateFolderID(_ context.Context, _ string) (string, error) {
	return uuid.NewString(), nil
}
func (f *fakeDriveGW) EnsureFolder(_ context.Context, _, _, _, _ string) error { return nil }
func (f *fakeDriveGW) GrantAccess(_ context.Context, _, _, email, _ string) error {
	if email == "bad@test.example" {
		return errors.New("drive permission denied")
	}
	return nil
}

type fakeFormsGW struct{}

func (f *fakeFormsGW) CreateDraft(_ context.Context, _, _, _ string) (string, bool, error) {
	return "fake-form-id", false, nil
}
func (f *fakeFormsGW) FindDraft(_ context.Context, _, _ string) (string, error) {
	return "fake-form-id", nil
}
func (f *fakeFormsGW) EnsureQuestions(_ context.Context, _, _ string, _ []integrations.IntakeQuestion) error {
	return nil
}
func (f *fakeFormsGW) EnableVerifiedEmail(_ context.Context, _, _ string) error   { return nil }
func (f *fakeFormsGW) VerifiedEmail(_ context.Context, _, _ string) (bool, error) { return true, nil }
func (f *fakeFormsGW) ListResponses(_ context.Context, _, _ string, _ time.Time, _ string) ([]integrations.IntakeResponse, string, error) {
	return nil, "", nil
}
func (f *fakeFormsGW) Publish(_ context.Context, _, _ string) (string, error) {
	return "https://forms.example/fake", nil
}
func (f *fakeFormsGW) FormURL(_ context.Context, _, _ string) (string, error) {
	return "https://forms.example/fake", nil
}

type fakeTokenSource struct{}

func (fakeTokenSource) AccessToken(context.Context) (string, error) { return "fake-token", nil }

type noopRecorder struct{}

func (noopRecorder) RecordExternalOperation(context.Context, integrations.ExternalOperation) error {
	return nil
}

// ── Test ───────────────────────────────────────────────────────────────────

func TestGroupLaunchWorkOrchestration(t *testing.T) {
	pool := testPool(t)

	repo := store.New(pool)
	groupsSvc := groups.New(repo)

	// Create a group with a mix of good and bad students.
	group, err := groupsSvc.Create(context.Background(), groups.CreateInput{
		Name: "Orchestration Test", CourseName: "Test Course", StartDate: "2026-12-01",
		StudentEmails:  []string{"good@test.example", "bad@test.example"},
		StaffEmails:    []string{"staff@test.example"},
		LecturerEmails: []string{"lecturer@test.example"},
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Fake-backed services.
	classroomSvc := &integrations.ClassroomService{
		Tokens: fakeTokenSource{}, Gateway: &fakeClassroomGW{},
		Store: repo, Operations: noopRecorder{},
	}
	driveSvc := &integrations.DriveService{
		Tokens: fakeTokenSource{}, Gateway: &fakeDriveGW{},
		Store: repo, Operations: noopRecorder{},
	}
	formsSvc := &integrations.FormsService{
		Tokens: fakeTokenSource{}, Gateway: &fakeFormsGW{},
		Store: repo, Operations: noopRecorder{},
	}

	// Worker with fakes + real store for result persistence.
	worker := &jobs.GroupLaunchWorker{
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		SlackProvisioner: &fakeSlackGW{},
		SlackConfigured:  true,
		ClassroomService: classroomSvc,
		DriveService:     driveSvc,
		FormsService:     formsSvc,
		GroupsService:    groupsSvc,
		ResultStore:      repo,
	}

	// Real River client.
	workers := river.NewWorkers()
	river.AddWorker(workers, worker)
	client, err := river.NewClient(
		riverpgxv5.New(pool),
		&river.Config{
			Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}},
			Workers: workers,
		},
	)
	if err != nil {
		t.Fatalf("river client: %v", err)
	}

	// Start River with a bounded context.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("river start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = client.Stop(stopCtx)
	})

	// Enqueue the launch job.
	_, err = client.Insert(ctx, jobs.GroupLaunchArgs{GroupID: group.ID}, nil)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}

	// Wait for a terminal state (bounded).
	deadline := time.Now().Add(20 * time.Second)
	var resultStatus string
	for time.Now().Before(deadline) {
		status, _, found, err := repo.GetLaunchResult(ctx, group.ID)
		if err != nil {
			t.Fatalf("get launch result: %v", err)
		}
		if found && status != "running" {
			resultStatus = status
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if resultStatus == "" {
		t.Fatal("launch did not reach a terminal state within 20s")
	}

	// ── Assertions on the PERSISTED result ────────────────────────────
	if resultStatus != "partially_failed" {
		t.Errorf("overall status = %q, want partially_failed", resultStatus)
	}

	// Read steps from the DB (not in-memory).
	var stepsRaw []byte
	err = pool.QueryRow(ctx,
		`SELECT steps FROM launch_results WHERE group_id = $1`, group.ID,
	).Scan(&stepsRaw)
	if err != nil {
		t.Fatalf("read steps: %v", err)
	}
	stepsByPlatform := parseSteps(t, stepsRaw)

	// Slack: succeeded.
	if s, ok := stepsByPlatform["slack"]; !ok || s != "succeeded" {
		t.Errorf("slack: got %q (found=%t), want succeeded", s, ok)
	}
	// Forms: succeeded.
	if s, ok := stepsByPlatform["forms"]; !ok || s != "succeeded" {
		t.Errorf("forms: got %q (found=%t), want succeeded", s, ok)
	}
	// Classroom: partially_failed (bad student enrollment).
	if s, ok := stepsByPlatform["classroom"]; !ok || s != "partially_failed" {
		t.Errorf("classroom: got %q (found=%t), want partially_failed", s, ok)
	}
	// Drive: partially_failed (bad student permission).
	if s, ok := stepsByPlatform["drive"]; !ok || s != "partially_failed" {
		t.Errorf("drive: got %q (found=%t), want partially_failed", s, ok)
	}

	// ── Verify the River job completed (not errored) ──────────────────
	// Wait briefly for River to finalize the job state after Work() returns.
	var jobState string
	jobDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(jobDeadline) {
		err = pool.QueryRow(ctx,
			`SELECT state FROM river_job WHERE kind = 'group_launch' ORDER BY id DESC LIMIT 1`,
		).Scan(&jobState)
		if err != nil {
			t.Fatalf("query river job: %v", err)
		}
		if jobState == "completed" || jobState == "errored" || jobState == "discarded" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if jobState != "completed" {
		t.Errorf("river job state = %q, want completed (partial failures are results, not errors)", jobState)
	}
}

// parseSteps extracts platform→status from the JSONB steps column.
func parseSteps(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	var steps []struct {
		Platform string `json:"platform"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(raw, &steps); err != nil {
		t.Fatalf("parse steps: %v", err)
	}
	out := make(map[string]string, len(steps))
	for _, s := range steps {
		out[s.Platform] = s.Status
	}
	return out
}

// Ensure the pool type matches what testPool returns.
var _ *pgxpool.Pool = (*pgxpool.Pool)(nil)
