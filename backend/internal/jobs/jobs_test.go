package jobs_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"launchpad/internal/dbmigrate"
	"launchpad/internal/jobs"
	"launchpad/internal/testdb"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	testURL := testdb.New(t)
	if err := dbmigrate.Up(testURL); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), testURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type pingWorker struct {
	river.WorkerDefaults[jobs.PingArgs]
	executed chan struct{}
	failN    int
}

func (w *pingWorker) Work(ctx context.Context, job *river.Job[jobs.PingArgs]) error {
	if job.Attempt <= w.failN {
		return fmt.Errorf("intentional failure attempt %d", job.Attempt)
	}
	w.executed <- struct{}{}
	return nil
}

func TestJobInsertAndProcess(t *testing.T) {
	pool := testPool(t)
	executed := make(chan struct{}, 1)
	workers := river.NewWorkers()
	river.AddWorker(workers, &pingWorker{executed: executed})

	client, err := river.NewClient(
		riverpgxv5.New(pool),
		&river.Config{
			Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 5}},
			Workers: workers,
		},
	)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.StopAndCancel(stopCtx); err != nil {
			t.Errorf("cleanup worker: %v", err)
		}
	})

	inserted, err := client.Insert(ctx, jobs.PingArgs{Message: "test-ping"}, nil)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if inserted.Job.Kind != "ping" {
		t.Errorf("kind = %q", inserted.Job.Kind)
	}

	select {
	case <-executed:
	case <-time.After(10 * time.Second):
		t.Fatal("job was not processed within 10s")
	}

	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestUniqueJobInsertion(t *testing.T) {
	pool := testPool(t)
	executed := make(chan struct{}, 10)
	workers := river.NewWorkers()
	river.AddWorker(workers, &pingWorker{executed: executed})

	client, err := river.NewClient(
		riverpgxv5.New(pool),
		&river.Config{
			Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 5}},
			Workers: workers,
		},
	)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	ctx := context.Background()
	opts := &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateRetryable,
				rivertype.JobStateScheduled,
				rivertype.JobStateCompleted,
			},
		},
	}
	first, err := client.Insert(ctx, jobs.PingArgs{Message: "unique-test"}, opts)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	duplicate, err := client.Insert(ctx, jobs.PingArgs{Message: "unique-test"}, opts)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if !duplicate.UniqueSkippedAsDuplicate || duplicate.Job.ID != first.Job.ID {
		t.Fatalf("expected duplicate of job %d, got %+v", first.Job.ID, duplicate)
	}

	var count int
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM river_job
		WHERE kind = 'ping' AND args->>'message' = 'unique-test'
		AND state IN ('available', 'running', 'retryable', 'scheduled', 'completed')
	`).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 job, found %d", count)
	}
}

func TestJobRetryOnFailure(t *testing.T) {
	pool := testPool(t)
	executed := make(chan struct{}, 10)
	workers := river.NewWorkers()
	river.AddWorker(workers, &pingWorker{executed: executed, failN: 1})

	client, err := river.NewClient(
		riverpgxv5.New(pool),
		&river.Config{
			Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 5}},
			Workers: workers,
		},
	)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.StopAndCancel(stopCtx); err != nil {
			t.Errorf("cleanup worker: %v", err)
		}
	})

	_, err = client.Insert(ctx, jobs.PingArgs{Message: "retry-test"}, &river.InsertOpts{MaxAttempts: 3})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	select {
	case <-executed:
	case <-time.After(60 * time.Second):
		t.Fatal("job did not succeed after retry within 60s")
	}

	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

type drainingWorker struct {
	river.WorkerDefaults[jobs.PingArgs]
	started chan struct{}
	release chan struct{}
}

func (w *drainingWorker) Work(ctx context.Context, _ *river.Job[jobs.PingArgs]) error {
	close(w.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.release:
		return nil
	}
}

func TestWorkerGracefulShutdown(t *testing.T) {
	pool := testPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := &drainingWorker{started: make(chan struct{}), release: make(chan struct{})}
	workers := river.NewWorkers()
	river.AddWorker(workers, worker)
	client, err := jobs.NewClient(pool, workers, jobs.Config{})
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	inserted, err := client.Insert(context.Background(), jobs.PingArgs{Message: "shutdown-test"}, nil)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- jobs.RunWorker(ctx, client, logger) }()
	t.Cleanup(func() {
		cancel()
		// A failed assertion still needs to release the job before closing the pool.
		select {
		case <-worker.release:
		default:
			close(worker.release)
		}
	})
	select {
	case <-worker.started:
	case err := <-done:
		t.Fatalf("worker exited before executing the job: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("job did not start")
	}

	cancel()
	close(worker.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("graceful shutdown did not complete")
	}
	job, err := client.JobGet(context.Background(), inserted.Job.ID)
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	if job.State != rivertype.JobStateCompleted || job.Attempt != 1 {
		t.Fatalf("job state=%s attempts=%d; want completed in one attempt", job.State, job.Attempt)
	}
}

func TestClientCreation(t *testing.T) {
	pool := testPool(t)
	workers := jobs.NewWorkers(slog.New(slog.NewTextHandler(io.Discard, nil)))
	client, err := jobs.NewClient(pool, workers, jobs.Config{})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if client == nil {
		t.Fatal("client is nil")
	}
}
