package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
)

type SlackLaunchArgs struct {
	RunID   uuid.UUID `json:"run_id"`
	GroupID uuid.UUID `json:"group_id"`
}

func (SlackLaunchArgs) Kind() string { return "slack_launch" }

type SlackLaunchStore interface {
	StartSlackLaunch(context.Context, uuid.UUID, int) (bool, error)
	FinishSlackLaunchAttempt(context.Context, uuid.UUID, int, string, string, bool) error
	GetByID(context.Context, uuid.UUID) (groups.Group, error)
	StudentRoster(context.Context, uuid.UUID) ([]groups.Student, error)
}

type SlackProvisioner interface {
	Provision(context.Context, integrations.ProvisionInput) (integrations.ProvisionResult, error)
}

type SlackLaunchWorker struct {
	river.WorkerDefaults[SlackLaunchArgs]
	Store SlackLaunchStore
	Slack SlackProvisioner
}

func (w *SlackLaunchWorker) Work(ctx context.Context, job *river.Job[SlackLaunchArgs]) error {
	run, err := w.Store.StartSlackLaunch(ctx, job.Args.RunID, job.Attempt)
	if err != nil || !run {
		return err
	}
	status, message, workErr := w.provision(ctx, job.Args.GroupID)
	terminal := workErr == nil || job.Attempt >= job.MaxAttempts
	if !terminal {
		status = "pending"
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := w.Store.FinishSlackLaunchAttempt(writeCtx, job.Args.RunID, job.Attempt, status, message, terminal); err != nil {
		return errors.Join(workErr, err)
	}
	return workErr
}

func (w *SlackLaunchWorker) provision(ctx context.Context, groupID uuid.UUID) (string, string, error) {
	group, err := w.Store.GetByID(ctx, groupID)
	if err != nil {
		return "failed", "could not load the group", err
	}
	students, err := w.Store.StudentRoster(ctx, groupID)
	if err != nil {
		return "failed", "could not load the student roster", err
	}
	emails := make([]string, 0, len(students))
	for _, student := range students {
		emails = append(emails, student.Email)
	}
	result, err := w.Slack.Provision(ctx, integrations.ProvisionInput{
		GroupID: group.ID, GroupName: group.Name, CourseName: group.CourseName,
		StartDate: group.StartDate.Format(time.DateOnly), InviteEmails: emails,
	})
	if err != nil {
		return "failed", "Slack provisioning request failed", err
	}
	failed := 0
	for _, channel := range result.Channels {
		if channel.Status == integrations.ChannelFailed {
			failed++
		}
	}
	for _, invite := range result.Invites {
		if invite.Status == integrations.InviteFailed {
			failed++
		}
	}
	for _, welcome := range result.Welcomes {
		if welcome.Status == integrations.WelcomeFailed {
			failed++
		}
	}
	if failed != 0 {
		message := fmt.Sprintf("%d Slack operations failed", failed)
		status := "partially_failed"
		if failed == len(result.Channels)+len(result.Invites)+len(result.Welcomes) {
			status = "failed"
		}
		return status, message, errors.New(message)
	}
	return "succeeded", "", nil
}

type SlackLaunchQueue interface {
	QueueSlackLaunch(context.Context, uuid.UUID, func(pgx.Tx, uuid.UUID) error) (groups.SlackLaunch, error)
	SlackLaunch(context.Context, uuid.UUID) (groups.SlackLaunch, error)
}

type SlackLaunchService struct {
	Queue SlackLaunchQueue
	River *river.Client[pgx.Tx]
}

func (s *SlackLaunchService) QueueLaunch(ctx context.Context, groupID uuid.UUID) (groups.SlackLaunch, error) {
	return s.Queue.QueueSlackLaunch(ctx, groupID, func(tx pgx.Tx, runID uuid.UUID) error {
		_, err := s.River.InsertTx(ctx, tx, SlackLaunchArgs{RunID: runID, GroupID: groupID}, &river.InsertOpts{MaxAttempts: 5})
		return err
	})
}

func (s *SlackLaunchService) Status(ctx context.Context, groupID uuid.UUID) (groups.SlackLaunch, error) {
	return s.Queue.SlackLaunch(ctx, groupID)
}
