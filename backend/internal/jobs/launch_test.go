package jobs_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"launchpad/internal/core/integrations"
	"launchpad/internal/jobs"
)

type fakeSlackProvisioner struct {
	Calls  int
	Err    error
	Result integrations.ProvisionResult
}

func (f *fakeSlackProvisioner) Provision(ctx context.Context, in integrations.ProvisionInput) (integrations.ProvisionResult, error) {
	f.Calls++
	return f.Result, f.Err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testData() jobs.GroupData {
	return jobs.GroupData{
		ID: uuid.New(), Name: "Test Group", CourseName: "Test Course",
		StartDate: "2026-12-01", StudentEmails: []string{"s1@x.example"},
	}
}

func TestLaunchSlackSucceeds(t *testing.T) {
	slack := &fakeSlackProvisioner{
		Result: integrations.ProvisionResult{
			Channels: []integrations.ChannelResult{{Name: "test", Status: "created"}},
		},
	}
	worker := &jobs.GroupLaunchWorker{Logger: testLogger(), SlackProvisioner: slack, SlackConfigured: true}
	step := worker.ProvisionSlackForTest(context.Background(), testData())
	if step.Status != jobs.StepSucceeded {
		t.Errorf("slack step: %+v", step)
	}
	if slack.Calls != 1 {
		t.Errorf("calls = %d", slack.Calls)
	}
}

func TestLaunchSlackSkippedWhenNil(t *testing.T) {
	worker := &jobs.GroupLaunchWorker{Logger: testLogger()}
	step := worker.ProvisionSlackForTest(context.Background(), testData())
	if step.Status != jobs.StepSkipped {
		t.Errorf("want skipped, got %+v", step)
	}
	if step.Detail != "not configured" {
		t.Errorf("detail = %q", step.Detail)
	}
}

func TestLaunchSlackReportsInviteFailures(t *testing.T) {
	slack := &fakeSlackProvisioner{
		Result: integrations.ProvisionResult{
			Channels: []integrations.ChannelResult{{Name: "c", Status: "created"}},
			Invites: []integrations.InviteResult{
				{Email: "a@x", Status: "invited"},
				{Email: "b@x", Status: "failed"},
			},
		},
	}
	worker := &jobs.GroupLaunchWorker{Logger: testLogger(), SlackProvisioner: slack, SlackConfigured: true}
	step := worker.ProvisionSlackForTest(context.Background(), testData())
	if step.Status != jobs.StepPartiallyFailed {
		t.Errorf("want partially_failed, got %+v", step)
	}
	if step.Detail == "" {
		t.Error("expected non-empty detail about invite failures")
	}
}

func TestLaunchClassroomSkippedWhenNil(t *testing.T) {
	worker := &jobs.GroupLaunchWorker{Logger: testLogger()}
	step := worker.ProvisionClassroomForTest(context.Background(), testData())
	if step.Status != jobs.StepSkipped {
		t.Errorf("want skipped, got %+v", step)
	}
}

func TestLaunchDriveSkippedWhenNil(t *testing.T) {
	worker := &jobs.GroupLaunchWorker{Logger: testLogger()}
	step := worker.ProvisionDriveForTest(context.Background(), testData())
	if step.Status != jobs.StepSkipped {
		t.Errorf("want skipped, got %+v", step)
	}
}

func TestLaunchFormsSkippedWhenNil(t *testing.T) {
	worker := &jobs.GroupLaunchWorker{Logger: testLogger()}
	step := worker.ProvisionFormsForTest(context.Background(), testData())
	if step.Status != jobs.StepSkipped {
		t.Errorf("want skipped, got %+v", step)
	}
}
