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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

// Config for the River client.
type Config struct {
	// WorkerID identifies this worker process in job logs (optional).
	WorkerID string
	// MaxWorkers is the concurrent job workers for the default queue.
	MaxWorkers int
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

// NewClient builds a River client over the existing pgx pool.
func NewClient(pool *pgxpool.Pool, workers *river.Workers, cfg Config) (*river.Client[pgx.Tx], error) {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = DefaultMaxWorkers
	}

	client, err := river.NewClient(
		riverpgxv5.New(pool),
		&river.Config{
			ID: cfg.WorkerID,
			Queues: map[string]river.QueueConfig{
				river.QueueDefault: {MaxWorkers: cfg.MaxWorkers},
			},
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
