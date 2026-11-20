// Package jobs — unified launch coordination (PRD §5, §8).
// Orchestrates all platform provisioning in sequence: Slack → Classroom →
// Drive → Forms. Each step is idempotent (reuses existing resources), and
// per-step outcomes are recorded so the coordinator sees exactly which
// platform succeeded and which needs attention.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
)

// ── Launch step model ──────────────────────────────────────────────────────

// LaunchStepStatus is the outcome of one platform's provisioning.
type LaunchStepStatus string

const (
	StepSkipped         LaunchStepStatus = "skipped"
	StepSucceeded       LaunchStepStatus = "succeeded"
	StepPartiallyFailed LaunchStepStatus = "partially_failed"
	StepFailed          LaunchStepStatus = "failed"
)

// LaunchStep reports one platform's outcome.
type LaunchStep struct {
	Platform string           `json:"platform"` // slack, classroom, drive, forms
	Status   LaunchStepStatus `json:"status"`
	Detail   string           `json:"detail,omitempty"`
}

// LaunchResult is the complete unified launch outcome.
type LaunchResult struct {
	GroupID uuid.UUID    `json:"groupId"`
	Status  string       `json:"status"` // running, completed, partially_failed, failed
	Steps   []LaunchStep `json:"steps"`
}

// ── River worker ───────────────────────────────────────────────────────────

// GroupLaunchArgs triggers the unified multi-platform launch.
type GroupLaunchArgs struct {
	GroupID uuid.UUID `json:"group_id"`
}

func (GroupLaunchArgs) Kind() string { return "group_launch" }

// GroupLaunchWorker coordinates all platforms for one group.
// LaunchResultStore persists and retrieves unified launch outcomes.
type LaunchResultStore interface {
	SaveLaunchResult(ctx context.Context, groupID uuid.UUID, status string, steps []LaunchStep) error
}

type GroupLaunchWorker struct {
	river.WorkerDefaults[GroupLaunchArgs]

	SlackProvisioner SlackProvisioner
	SlackConfigured  bool // true when SLACK_BOT_TOKEN is set
	ClassroomService *integrations.ClassroomService
	DriveService     *integrations.DriveService
	FormsService     *integrations.FormsService
	GroupsService    *groups.Service
	StaffEmails      []string
	ResultStore      LaunchResultStore
	Logger           *slog.Logger
}

// GroupData carries the group info needed by each provisioning step.
type GroupData struct {
	ID            uuid.UUID
	Name          string
	CourseName    string
	StartDate     string
	StudentEmails []string // from roster
}

// GroupDataSource provides group info to the launch worker.
type GroupDataSource interface {
	GetGroupData(ctx context.Context, groupID uuid.UUID) (GroupData, error)
}

func (w *GroupLaunchWorker) Work(ctx context.Context, job *river.Job[GroupLaunchArgs]) error {
	groupID := job.Args.GroupID
	logger := w.Logger.With("job_id", job.ID, "group_id", groupID)
	logger.Info("group_launch started", "attempt", job.Attempt)

	// Fetch group data once for all steps.
	group, err := w.GroupsService.Get(ctx, groupID.String())
	if err != nil {
		logger.Error("group_launch: group not found", "err", err.Error())
		return err // River will retry
	}
	roster, rosterErr := w.GroupsService.Onboarding(ctx, groupID.String())
	if rosterErr != nil {
		logger.Error("group_launch: roster read failed", "err", rosterErr.Error())
		// Persist the failure so the coordinator sees it.
		if w.ResultStore != nil {
			_ = w.ResultStore.SaveLaunchResult(ctx, groupID, "failed", []LaunchStep{
				{Platform: "roster", Status: StepFailed, Detail: rosterErr.Error()},
			})
		}
		return rosterErr // River will retry
	}
	studentEmails := make([]string, 0, len(roster.Students))
	for _, s := range roster.Students {
		studentEmails = append(studentEmails, s.Email)
	}
	data := GroupData{
		ID: groupID, Name: group.Name, CourseName: group.CourseName,
		StartDate: group.StartDate.Format("2006-01-02"), StudentEmails: studentEmails,
	}

	result := LaunchResult{GroupID: groupID, Status: "running", Steps: make([]LaunchStep, 0, 4)}
	failed := 0

	// Persist "running" so the coordinator sees the launch is in progress.
	if w.ResultStore != nil {
		_ = w.ResultStore.SaveLaunchResult(ctx, groupID, "running", nil)
	}

	// ── Steps in order: Slack → Classroom → Drive → Forms ─────────────
	steps := []struct {
		name string
		fn   func(context.Context, GroupData) LaunchStep
	}{
		{"slack", w.provisionSlackStep},
		{"classroom", w.provisionClassroomStep},
		{"drive", w.provisionDriveStep},
		{"forms", w.provisionFormsStep},
	}
	for _, s := range steps {
		step := s.fn(ctx, data)
		result.Steps = append(result.Steps, step)
		if step.Status == StepFailed {
			failed++
		}
	}

	// Determine overall status.
	partial := 0
	for _, s := range result.Steps {
		if s.Status == StepPartiallyFailed {
			partial++
		}
	}
	switch {
	case failed == 0 && partial == 0:
		result.Status = "completed"
	case failed == len(result.Steps):
		result.Status = "failed"
	default:
		result.Status = "partially_failed"
	}

	logger.Info("group_launch finished", "status", result.Status, "failed_steps", failed)

	// Persist the outcome for the coordinator's status endpoint.
	// A persistence failure IS a job failure — River retries, ensuring
	// the coordinator never sees a stale or missing status.
	if w.ResultStore != nil {
		if err := w.ResultStore.SaveLaunchResult(ctx, groupID, result.Status, result.Steps); err != nil {
			logger.Error("group_launch: failed to persist result", "err", err.Error())
			return err // River will retry the job
		}
	}
	return nil
}

// ProvisionSlackForTest exposes the Slack step for unit testing.
func (w *GroupLaunchWorker) ProvisionSlackForTest(ctx context.Context, data GroupData) LaunchStep {
	return w.provisionSlackStep(ctx, data)
}

// ProvisionClassroomForTest exposes the Classroom step for unit testing.
func (w *GroupLaunchWorker) ProvisionClassroomForTest(ctx context.Context, data GroupData) LaunchStep {
	return w.provisionClassroomStep(ctx, data)
}

// ProvisionDriveForTest exposes the Drive step for unit testing.
func (w *GroupLaunchWorker) ProvisionDriveForTest(ctx context.Context, data GroupData) LaunchStep {
	return w.provisionDriveStep(ctx, data)
}

// ProvisionFormsForTest exposes the Forms step for unit testing.
func (w *GroupLaunchWorker) ProvisionFormsForTest(ctx context.Context, data GroupData) LaunchStep {
	return w.provisionFormsStep(ctx, data)
}

// ── Per-platform provisioning steps ────────────────────────────────────────

func (w *GroupLaunchWorker) provisionSlackStep(ctx context.Context, data GroupData) LaunchStep {
	if w.SlackProvisioner == nil || !w.SlackConfigured {
		return LaunchStep{Platform: "slack", Status: StepSkipped, Detail: "not configured"}
	}
	result, err := w.SlackProvisioner.Provision(ctx, integrations.ProvisionInput{
		GroupID:    data.ID,
		GroupName:  data.Name,
		CourseName: data.CourseName,
		StartDate:  data.StartDate,
	})
	if err != nil {
		return LaunchStep{Platform: "slack", Status: StepFailed, Detail: err.Error()}
	}
	var failCount int
	for _, inv := range result.Invites {
		if inv.Status == "failed" {
			failCount++
		}
	}
	if failCount > 0 {
		return LaunchStep{Platform: "slack", Status: StepPartiallyFailed,
			Detail: fmt.Sprintf("channels ok, %d invite failures", failCount)}
	}
	return LaunchStep{Platform: "slack", Status: StepSucceeded}
}

func (w *GroupLaunchWorker) provisionClassroomStep(ctx context.Context, data GroupData) LaunchStep {
	if w.ClassroomService == nil {
		return LaunchStep{Platform: "classroom", Status: StepSkipped, Detail: "not configured"}
	}
	courseID, err := w.ClassroomService.Provision(ctx, data.ID, data.CourseName, data.Name, time.Now())
	if err != nil {
		return LaunchStep{Platform: "classroom", Status: StepFailed, Detail: err.Error()}
	}
	if len(data.StudentEmails) > 0 {
		results := w.ClassroomService.EnrollAll(ctx, data.ID, courseID, data.StudentEmails)
		var failCount int
		for _, r := range results {
			if r.Err != nil {
				failCount++
			}
		}
		if failCount > 0 {
			return LaunchStep{Platform: "classroom", Status: StepPartiallyFailed,
				Detail: fmt.Sprintf("course created, %d enrollment failures", failCount)}
		}
	}
	// Assign teachers/staff (PRD §4.3).
	if len(w.StaffEmails) > 0 {
		teacherResults := w.ClassroomService.AssignTeachers(ctx, data.ID, courseID, w.StaffEmails)
		var teacherFail int
		for _, r := range teacherResults {
			if r.Err != nil {
				teacherFail++
			}
		}
		if teacherFail > 0 {
			return LaunchStep{Platform: "classroom", Status: StepPartiallyFailed,
				Detail: fmt.Sprintf("course created, %d teacher assignment failures", teacherFail)}
		}
	}
	return LaunchStep{Platform: "classroom", Status: StepSucceeded}
}

func (w *GroupLaunchWorker) provisionDriveStep(ctx context.Context, data GroupData) LaunchStep {
	if w.DriveService == nil {
		return LaunchStep{Platform: "drive", Status: StepSkipped, Detail: "not configured"}
	}
	folders, err := w.DriveService.Provision(ctx, data.ID, data.Name)
	if err != nil {
		return LaunchStep{Platform: "drive", Status: StepFailed, Detail: err.Error()}
	}
	if err := w.DriveService.ApplyAccessRules(ctx, data.ID, folders, integrations.DriveAccessRules{
		StaffEmails:   w.StaffEmails,
		StudentEmails: data.StudentEmails,
	}); err != nil {
		return LaunchStep{Platform: "drive", Status: StepPartiallyFailed,
			Detail: "folders created, permissions partial: " + err.Error()}
	}
	return LaunchStep{Platform: "drive", Status: StepSucceeded}
}

func (w *GroupLaunchWorker) provisionFormsStep(ctx context.Context, data GroupData) LaunchStep {
	if w.FormsService == nil {
		return LaunchStep{Platform: "forms", Status: StepSkipped, Detail: "not configured"}
	}
	if _, err := w.FormsService.Provision(ctx, data.ID, data.Name, nil); err != nil {
		return LaunchStep{Platform: "forms", Status: StepFailed, Detail: err.Error()}
	}
	return LaunchStep{Platform: "forms", Status: StepSucceeded}
}
