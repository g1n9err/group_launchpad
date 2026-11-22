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
	"strings"
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

func LaunchStatusForSteps(steps []LaunchStep) string {
	failed, partial := 0, 0
	for _, step := range steps {
		switch step.Status {
		case StepFailed:
			failed++
		case StepPartiallyFailed:
			partial++
		}
	}
	switch {
	case failed == 0 && partial == 0:
		return "completed"
	case failed == len(steps):
		return "failed"
	default:
		return "partially_failed"
	}
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

// StaffAssignmentReader reads per-group staff assignments (PRD §4.1).
type StaffAssignmentReader interface {
	StaffEmailsFor(ctx context.Context, groupID uuid.UUID) ([]string, error)
}

// GroupLaunchWorker coordinates all platforms for one group.
type GroupLaunchWorker struct {
	river.WorkerDefaults[GroupLaunchArgs]

	SlackProvisioner SlackProvisioner
	SlackConfigured  bool
	ClassroomService *integrations.ClassroomService
	DriveService     *integrations.DriveService
	FormsService     *integrations.FormsService
	GroupsService    *groups.Service
	StaffEmails      []string              // global fallback (STAFF_EMAILS env)
	StaffAssignments StaffAssignmentReader // per-group staff (overrides global)
	ResultStore      LaunchResultStore
	Notifications    NotificationCreator // creates coordinator notifications (PRD §4.9)
	Logger           *slog.Logger
}

// NotificationCreator creates persistent coordinator notifications.
type NotificationCreator interface {
	InsertNotificationOnce(ctx context.Context, groupID *uuid.UUID, kind, severity, title, detail, dedupeKey string) error
}

// GroupData carries the group info needed by each provisioning step.
type GroupData struct {
	ID            uuid.UUID
	Name          string
	CourseName    string
	StartDate     string
	StudentEmails []string // from roster
	StaffEmails   []string // per-group staff+lecturers+TAs (PRD §4.1)
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

	// Build GroupData: staff defaults to the worker's global STAFF_EMAILS;
	// per-group staff_assignments override when present (PRD §4.1).
	data := GroupData{
		ID: groupID, Name: group.Name, CourseName: group.CourseName,
		StartDate:     group.StartDate.Format("2006-01-02"),
		StudentEmails: studentEmails,
		StaffEmails:   w.StaffEmails,
	}
	// Load per-group staff assignments (PRD §4.1). If reading fails, the
	// launch fails — we do NOT silently fall back to global STAFF_EMAILS,
	// because that could grant Drive/Classroom access to the wrong people.
	if w.StaffAssignments != nil {
		staff, err := w.StaffAssignments.StaffEmailsFor(ctx, groupID)
		if err != nil {
			logger.Error("group_launch: staff assignment read failed", "err", err.Error())
			if w.ResultStore != nil {
				_ = w.ResultStore.SaveLaunchResult(ctx, groupID, "failed", []LaunchStep{
					{Platform: "staff", Status: StepFailed, Detail: "failed to read staff assignments: " + err.Error()},
				})
			}
			return err // River retries
		}
		if len(staff) > 0 {
			data.StaffEmails = staff
		}
		// If len(staff) == 0, keep the worker's global fallback — this is
		// the intended legacy path when no per-group staff are configured.
	}

	result := LaunchResult{GroupID: groupID, Status: "running", Steps: make([]LaunchStep, 0, 4)}
	failed := 0

	// Persist "running" so the coordinator sees the launch is in progress.
	if w.ResultStore != nil {
		_ = w.ResultStore.SaveLaunchResult(ctx, groupID, "running", nil)
	}

	// ── Steps in order: Slack → Classroom → Drive → Forms ─────────────
	outcomes := make(map[string]stepOutcome, 4)
	for _, s := range launchSteps(w) {
		outcome := s.fn(ctx, data)
		outcomes[s.platform] = outcome
		result.Steps = append(result.Steps, outcome.step)
		if outcome.step.Status == StepFailed {
			failed++
		}
	}

	result.Status = LaunchStatusForSteps(result.Steps)

	logger.Info("group_launch finished", "status", result.Status, "failed_steps", failed)

	// Persist the outcome for the coordinator's status endpoint.
	if w.ResultStore != nil {
		if err := w.ResultStore.SaveLaunchResult(ctx, groupID, result.Status, result.Steps); err != nil {
			logger.Error("group_launch: failed to persist result", "err", err.Error())
			return err
		}
	}

	// Create a coordinator notification (PRD §4.9).
	if w.Notifications != nil {
		kind := "launch_" + result.Status
		if result.Status == "completed" {
			kind = "launch_completed"
		} else if result.Status == "partially_failed" {
			kind = "launch_partially_failed"
		} else {
			kind = "launch_failed"
		}
		severity := "info"
		if result.Status == "partially_failed" {
			severity = "warning"
		} else if result.Status == "failed" {
			severity = "error"
		}
		title := fmt.Sprintf("Group launch %s", result.Status)
		detail := ""
		for _, s := range result.Steps {
			if s.Status == StepFailed || s.Status == StepPartiallyFailed {
				detail += s.Platform + ": " + s.Detail + "; "
			}
		}
		dedupeKey := fmt.Sprintf("group_launch:%d", job.ID)
		if err := w.Notifications.InsertNotificationOnce(ctx, &groupID, kind, severity, title, detail, dedupeKey); err != nil {
			logger.Error("group_launch: failed to create coordinator notification", "err", err.Error())
			return err // River retries instead of silently losing the notification.
		}

		// Per-platform failure notifications (PRD §4.9: "Slack invitation
		// failure", "Classroom or Drive creation failure"). Details carry
		// counts only — never student or staff email addresses. Dedupe keys
		// are scoped to this River job, so retries never duplicate rows.
		for _, note := range platformFailureNotifications(outcomes) {
			key := fmt.Sprintf("group_launch:%d:%s", job.ID, note.kind)
			if err := w.Notifications.InsertNotificationOnce(ctx, &groupID, note.kind, note.severity, note.title, note.detail, key); err != nil {
				logger.Error("group_launch: failed to create platform notification", "kind", note.kind, "err", err.Error())
				return err
			}
		}
	}

	return nil
}

// stepOutcome pairs a launch step with the extra breakdown its notification
// needs (currently only Slack invite failures).
type stepOutcome struct {
	step        LaunchStep
	inviteFails int
}

// launchStepRunner runs one platform step and records its platform name.
type launchStepRunner struct {
	platform string
	fn       func(context.Context, GroupData) stepOutcome
}

func launchSteps(w *GroupLaunchWorker) []launchStepRunner {
	return []launchStepRunner{
		{"slack", func(ctx context.Context, d GroupData) stepOutcome {
			step, invites := w.provisionSlackStepDetailed(ctx, d)
			return stepOutcome{step: step, inviteFails: invites}
		}},
		{"classroom", func(ctx context.Context, d GroupData) stepOutcome {
			return stepOutcome{step: w.provisionClassroomStep(ctx, d)}
		}},
		{"drive", func(ctx context.Context, d GroupData) stepOutcome {
			return stepOutcome{step: w.provisionDriveStep(ctx, d)}
		}},
		{"forms", func(ctx context.Context, d GroupData) stepOutcome {
			return stepOutcome{step: w.provisionFormsStep(ctx, d)}
		}},
	}
}

// platformFailureNotifications maps failed steps to PRD §4.9 notification
// kinds. Slack invitation failures are reported separately from channel
// creation problems; everything is PII-safe (counts, no addresses).
type platformNotification struct {
	kind, severity, title, detail string
}

func platformFailureNotifications(outcomes map[string]stepOutcome) []platformNotification {
	var notes []platformNotification
	slack, ok := outcomes["slack"]
	if ok && slack.inviteFails > 0 {
		severity := "warning"
		if slack.step.Status == StepFailed {
			severity = "error"
		}
		notes = append(notes, platformNotification{
			kind: "invite_failure", severity: severity,
			title:  "Slack invitation failure",
			detail: fmt.Sprintf("%d Slack invitation(s) failed during group launch", slack.inviteFails),
		})
	}
	classroom, ok := outcomes["classroom"]
	if ok && (classroom.step.Status == StepFailed || classroom.step.Status == StepPartiallyFailed) {
		notes = append(notes, platformNotification{
			kind: "classroom_failure", severity: severityOf(classroom.step),
			title: "Classroom provisioning failure", detail: classroom.step.Detail,
		})
	}
	drive, ok := outcomes["drive"]
	if ok && (drive.step.Status == StepFailed || drive.step.Status == StepPartiallyFailed) {
		notes = append(notes, platformNotification{
			kind: "drive_failure", severity: severityOf(drive.step),
			title: "Drive provisioning failure", detail: drive.step.Detail,
		})
	}
	forms, ok := outcomes["forms"]
	if ok && forms.step.Status == StepFailed {
		notes = append(notes, platformNotification{
			kind: "forms_failure", severity: "error",
			title: "Intake form provisioning failure", detail: forms.step.Detail,
		})
	}
	return notes
}

func severityOf(step LaunchStep) string {
	if step.Status == StepFailed {
		return "error"
	}
	return "warning"
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
	step, _ := w.provisionSlackStepDetailed(ctx, data)
	return step
}

// provisionSlackStepDetailed additionally reports the invitation failure
// count so the notification layer can emit invite_failure (PRD §4.9).
func (w *GroupLaunchWorker) provisionSlackStepDetailed(ctx context.Context, data GroupData) (LaunchStep, int) {
	if w.SlackProvisioner == nil || !w.SlackConfigured {
		return LaunchStep{Platform: "slack", Status: StepSkipped, Detail: "not configured"}, 0
	}
	result, err := w.SlackProvisioner.Provision(ctx, integrations.ProvisionInput{
		GroupID:      data.ID,
		GroupName:    data.Name,
		CourseName:   data.CourseName,
		StartDate:    data.StartDate,
		InviteEmails: launchInviteEmails(w.effectiveStaff(data), data.StudentEmails),
	})
	if err != nil {
		return LaunchStep{Platform: "slack", Status: StepFailed, Detail: err.Error()}, 0
	}
	var channelFailures, inviteFailures, welcomeFailures int
	for _, channel := range result.Channels {
		if channel.Status == integrations.ChannelFailed {
			channelFailures++
		}
	}
	failedRecipients := make(map[string]bool)
	missingAccounts := make(map[string]bool)
	for _, inv := range result.Invites {
		if inv.Status == integrations.InviteFailed {
			inviteFailures++
			failedRecipients[inv.Email] = true
			if inv.Error == "user_not_found" {
				missingAccounts[inv.Email] = true
			}
		}
	}
	for _, welcome := range result.Welcomes {
		if welcome.Status == integrations.WelcomeFailed {
			welcomeFailures++
		}
	}
	if len(result.Channels) == 0 || channelFailures == len(result.Channels) {
		return LaunchStep{Platform: "slack", Status: StepFailed, Detail: "all Slack channels failed"}, inviteFailures
	}
	if channelFailures+inviteFailures+welcomeFailures > 0 {
		detail := fmt.Sprintf("%d channel and %d welcome failures", channelFailures, welcomeFailures)
		if inviteFailures > 0 {
			detail = fmt.Sprintf("%d unsuccessful channel invitations for %d addresses. %d Slack accounts not found. %s",
				inviteFailures, len(failedRecipients), len(missingAccounts), detail)
		}
		return LaunchStep{Platform: "slack", Status: StepPartiallyFailed, Detail: detail}, inviteFailures
	}
	return LaunchStep{Platform: "slack", Status: StepSucceeded}, 0
}

// effectiveStaff returns per-group staff when available, falling back to
// the worker's global STAFF_EMAILS. Used by every step so the fallback
// is applied consistently regardless of how the step is invoked.
func (w *GroupLaunchWorker) effectiveStaff(data GroupData) []string {
	if len(data.StaffEmails) > 0 {
		return data.StaffEmails
	}
	return w.StaffEmails
}

func launchInviteEmails(staff, students []string) []string {
	seen := make(map[string]struct{}, len(staff)+len(students))
	combined := make([]string, 0, len(staff)+len(students))
	for _, email := range append(append([]string(nil), staff...), students...) {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" {
			continue
		}
		if _, exists := seen[email]; exists {
			continue
		}
		seen[email] = struct{}{}
		combined = append(combined, email)
	}
	return combined
}

func (w *GroupLaunchWorker) provisionClassroomStep(ctx context.Context, data GroupData) LaunchStep {
	if w.ClassroomService == nil {
		return LaunchStep{Platform: "classroom", Status: StepSkipped, Detail: "not configured"}
	}
	courseID, err := w.ClassroomService.Provision(ctx, data.ID, data.CourseName, data.Name, time.Now())
	if err != nil {
		return LaunchStep{Platform: "classroom", Status: StepFailed, Detail: err.Error()}
	}
	// Both phases run even when the other has failures — a partially failed
	// enrollment must never leave the course without its teachers (and vice
	// versa). Details aggregate.
	var enrollmentFails, teacherFails int
	if len(data.StudentEmails) > 0 {
		for _, r := range w.ClassroomService.EnrollAll(ctx, data.ID, courseID, data.StudentEmails) {
			if r.Err != nil {
				enrollmentFails++
			}
		}
	}
	if staff := w.effectiveStaff(data); len(staff) > 0 {
		for _, r := range w.ClassroomService.AssignTeachers(ctx, data.ID, courseID, staff) {
			if r.Err != nil {
				teacherFails++
			}
		}
	}
	switch {
	case enrollmentFails > 0 && teacherFails > 0:
		return LaunchStep{Platform: "classroom", Status: StepPartiallyFailed,
			Detail: fmt.Sprintf("course created, %d enrollment and %d teacher assignment failures", enrollmentFails, teacherFails)}
	case enrollmentFails > 0:
		return LaunchStep{Platform: "classroom", Status: StepPartiallyFailed,
			Detail: fmt.Sprintf("course created, %d enrollment failures", enrollmentFails)}
	case teacherFails > 0:
		return LaunchStep{Platform: "classroom", Status: StepPartiallyFailed,
			Detail: fmt.Sprintf("course created, %d teacher assignment failures", teacherFails)}
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
		StaffEmails:   w.effectiveStaff(data),
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
