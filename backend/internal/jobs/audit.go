package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"launchpad/internal/core"
	"launchpad/internal/core/audit"
)

type GroupAuditArgs struct {
	GroupID uuid.UUID `json:"group_id"`
	Trigger string    `json:"trigger"`
}

func (GroupAuditArgs) Kind() string { return "group_audit" }

type GroupAuditWorker struct {
	river.WorkerDefaults[GroupAuditArgs]
	Audit        *audit.Service
	EmailEnabled bool
}

func (w *GroupAuditWorker) Timeout(*river.Job[GroupAuditArgs]) time.Duration {
	return 3 * time.Minute
}

func (w *GroupAuditWorker) Work(ctx context.Context, job *river.Job[GroupAuditArgs]) error {
	id, err := w.Audit.Run(ctx, job.Args.GroupID, job.ID, job.Args.Trigger, w.EmailEnabled)
	var missing *core.AppError
	if errors.As(err, &missing) && missing.Code == core.CodeNotFound {
		return nil // A group may be removed before its queued audit runs.
	}
	if err != nil || id == uuid.Nil || !w.EmailEnabled {
		return err
	}
	client, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}
	_, err = client.Insert(ctx, AuditEmailArgs{NotificationID: id}, &river.InsertOpts{
		Queue: AuditEmailQueue, UniqueOpts: river.UniqueOpts{ByArgs: true},
	})
	return err
}

type DailyAuditArgs struct{}

func (DailyAuditArgs) Kind() string { return "daily_group_audits" }

type DailyAuditWorker struct {
	river.WorkerDefaults[DailyAuditArgs]
	Groups interface {
		AuditableGroupIDs(context.Context) ([]uuid.UUID, error)
	}
}

func (w *DailyAuditWorker) Work(ctx context.Context, _ *river.Job[DailyAuditArgs]) error {
	groupIDs, err := w.Groups.AuditableGroupIDs(ctx)
	if err != nil {
		return err
	}
	client, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}
	for _, id := range groupIDs {
		_, err := client.Insert(ctx, GroupAuditArgs{GroupID: id, Trigger: "daily_sync"}, &river.InsertOpts{
			UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 24 * time.Hour},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func DailyAuditSchedule() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(24*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) {
			return DailyAuditArgs{}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByPeriod: 24 * time.Hour}}
		},
		&river.PeriodicJobOpts{ID: "daily-group-audits", RunOnStart: true},
	)
}

type AuditService struct {
	River *river.Client[pgx.Tx]
	Store interface {
		RecentAudits(context.Context, uuid.UUID) ([]audit.Snapshot, error)
	}
}

func (s *AuditService) QueueAudit(ctx context.Context, groupID uuid.UUID) (int64, error) {
	result, err := s.River.Insert(ctx, GroupAuditArgs{GroupID: groupID, Trigger: "manual"}, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
			rivertype.JobStateRetryable, rivertype.JobStateScheduled,
		}},
	})
	if err != nil {
		return 0, err
	}
	return result.Job.ID, nil
}

func (s *AuditService) Recent(ctx context.Context, groupID uuid.UUID) ([]audit.Snapshot, error) {
	return s.Store.RecentAudits(ctx, groupID)
}
