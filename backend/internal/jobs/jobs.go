// Package jobs defines River workers for the automation engine (PRD §4.7,
// §4.8, §8). This file contains the infrastructure: the queue client,
// registration, and a reference job that proves the pipeline works.
// Business-specific workers (launch, audit, announcements) are added in
// later phases on top of this foundation.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
)

// Config for the River client.
type Config struct {
	// WorkerID identifies this worker process in job logs (optional).
	WorkerID string
	// MaxWorkers is the concurrent job workers for the default queue.
	MaxWorkers                int
	IntakeEmailWorkers        int
	AuditEmailEnabled         bool
	SlackAnnouncementsEnabled bool
}

// DefaultMaxWorkers is the number of concurrent workers for the default queue.
const DefaultMaxWorkers = 10

// NewWorkers returns the Workers registry with all job types registered.
// This is the composition root for the job layer — business workers are
// added in later phases via additional AddWorker calls.
func NewWorkers(logger *slog.Logger) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &PingWorker{Logger: logger})
	return workers
}

// NewLaunchWorkers returns workers with the unified group launch registered,
// alongside the base ping worker. Callers inject their service dependencies.
func NewLaunchWorkers(
	logger *slog.Logger,
	slack SlackProvisioner,
	slackConfigured bool,
	classroom *integrations.ClassroomService,
	drive *integrations.DriveService,
	forms *integrations.FormsService,
	groupsSvc *groups.Service,
	staffEmails []string,
	resultStore LaunchResultStore,
) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &PingWorker{Logger: logger})
	river.AddWorker(workers, &GroupLaunchWorker{
		Logger:           logger,
		SlackProvisioner: slack,
		SlackConfigured:  slackConfigured,
		ClassroomService: classroom,
		DriveService:     drive,
		FormsService:     forms,
		GroupsService:    groupsSvc,
		StaffEmails:      staffEmails,
		ResultStore:      resultStore,
	})
	return workers
}

// NewClient builds a River client over the existing pgx pool.
func NewClient(pool *pgxpool.Pool, workers *river.Workers, cfg Config) (*river.Client[pgx.Tx], error) {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = DefaultMaxWorkers
	}

	queues := map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: cfg.MaxWorkers}}
	if cfg.IntakeEmailWorkers > 0 {
		queues[IntakeEmailQueue] = river.QueueConfig{MaxWorkers: cfg.IntakeEmailWorkers}
	}

	if cfg.AuditEmailEnabled {
		queues[AuditEmailQueue] = river.QueueConfig{MaxWorkers: 1}
	}

	if cfg.SlackAnnouncementsEnabled {
		queues[SlackAnnouncementsQueue] = river.QueueConfig{MaxWorkers: 1}
	}

	client, err := river.NewClient(
		riverpgxv5.New(pool),
		&river.Config{
			ID:              cfg.WorkerID,
			Queues:          queues,
			Workers:         workers,
			SoftStopTimeout: 30 * time.Second,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("jobs: new client: %w", err)
	}
	return client, nil
}

// RunWorker starts the River client, blocks until the context is cancelled,
// then gracefully drains in-flight jobs. This is the main entrypoint for
// cmd/worker.
func RunWorker(ctx context.Context, client *river.Client[pgx.Tx], logger *slog.Logger) error {
	// Register periodic jobs (only effective on the elected leader).
	_, err := client.PeriodicJobs().AddSafely(dailyPingPeriodicJob())
	if err != nil {
		return fmt.Errorf("jobs: register periodic: %w", err)
	}

	if err := client.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("jobs: start: %w", err)
	}
	logger.Info("river worker started", "client_id", client.ID())

	// Block until context is cancelled (SIGINT/SIGTERM from main).
	select {
	case <-ctx.Done():
	case <-client.Stopped():
		return nil
	}
	logger.Info("river worker shutting down", "reason", ctx.Err())

	// River cancels remaining work after the soft-stop timeout. Bound the
	// total wait too, in case a job does not respond to cancellation.
	stopCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := client.Stop(stopCtx); err != nil {
		return fmt.Errorf("jobs: stop: %w", err)
	}
	logger.Info("river worker stopped cleanly")
	return nil
}

// ── Ping job (infrastructure proof) ────────────────────────────────────────

// PingArgs is a minimal job that proves the queue pipeline works end-to-end.
type PingArgs struct {
	Message string `json:"message"`
}

func (PingArgs) Kind() string { return "ping" }

// PingWorker logs the message — no side effects.
type PingWorker struct {
	river.WorkerDefaults[PingArgs]
	Logger *slog.Logger
}

func (w *PingWorker) Work(ctx context.Context, job *river.Job[PingArgs]) error {
	w.Logger.Info("ping", "job_id", job.ID, "message", job.Args.Message, "attempt", job.Attempt)
	return nil
}

// ── Periodic job registration ──────────────────────────────────────────────

// dailyPingPeriodicJob returns a River periodic job that runs the ping job
// every 24 hours — proving the scheduling mechanism works. Future phases
// replace this with the actual daily sync audit (PRD §4.7).
func dailyPingPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(24*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) {
			return PingArgs{Message: "daily health check"}, &river.InsertOpts{
				UniqueOpts: river.UniqueOpts{
					ByState: []rivertype.JobState{
						rivertype.JobStateAvailable,
						rivertype.JobStatePending,
						rivertype.JobStateRunning,
						rivertype.JobStateRetryable,
						rivertype.JobStateScheduled,
					},
				},
			}
		},
		&river.PeriodicJobOpts{ID: "daily-ping"},
	)
}

// ── Helper: insert a job from the API process ─────────────────────────────

// Enqueue inserts a job without running workers (for use from cmd/api).
func Enqueue(ctx context.Context, client *river.Client[pgx.Tx], args river.JobArgs, opts *river.InsertOpts) error {
	if opts == nil {
		opts = &river.InsertOpts{}
	}
	_, err := client.Insert(ctx, args, opts)
	if err != nil {
		return fmt.Errorf("jobs: enqueue: %w", err)
	}
	return nil
}

// JobInserter is the narrow River client interface needed for queueing.
type JobInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// RiverLauncher implements transport.GroupLauncher by inserting the unified
// group launch job into River. The worker process picks it up.
type RiverLauncher struct {
	Client      JobInserter
	ResultStore LaunchResultStore // overwrites stale results on queue
}

// QueueGroupLaunch marks the group as "running" BEFORE inserting the job,
// so the status endpoint immediately reflects the new launch — the UI
// never sees a stale completed/partially_failed result from a previous run.
// If the River insert fails, the result is rolled back to "failed" so
// the coordinator isn't left with a permanently "running" status.
func (l *RiverLauncher) QueueGroupLaunch(ctx context.Context, groupID uuid.UUID) error {
	// Overwrite any previous result with "running" first. This ensures
	// GET /status returns "running" (not the old terminal state) from
	// the moment the launch is queued.
	if l.ResultStore != nil {
		if err := l.ResultStore.SaveLaunchResult(ctx, groupID, "running", nil); err != nil {
			return fmt.Errorf("mark launch running: %w", err)
		}
	}
	_, err := l.Client.Insert(ctx, GroupLaunchArgs{GroupID: groupID}, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateRetryable,
				rivertype.JobStateScheduled,
			},
		},
	})
	if err != nil {
		// Roll back the "running" status so the UI doesn't poll forever.
		if l.ResultStore != nil {
			rollbackErr := l.ResultStore.SaveLaunchResult(ctx, groupID, "failed", []LaunchStep{
				{Platform: "queue", Status: StepFailed, Detail: "failed to enqueue launch job"},
			})
			if rollbackErr != nil {
				return fmt.Errorf("queue launch (%v); rollback to failed also failed: %w", err, rollbackErr)
			}
		}
		return fmt.Errorf("queue group launch: %w", err)
	}
	return nil
}
