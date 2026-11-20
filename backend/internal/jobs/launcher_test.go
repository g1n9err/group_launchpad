package jobs_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"launchpad/internal/jobs"
)

// fakeInserter succeeds or fails on demand — no PostgreSQL needed.
type fakeInserter struct {
	Err   error
	Calls int
}

func (f *fakeInserter) Insert(_ context.Context, _ river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	f.Calls++
	if f.Err != nil {
		return nil, f.Err
	}
	return &rivertype.JobInsertResult{}, nil
}

// recordingStore captures SaveLaunchResult calls.
type recordingStore struct {
	Saves []saveCall
}

type saveCall struct {
	GroupID uuid.UUID
	Status  string
}

func (s *recordingStore) SaveLaunchResult(_ context.Context, groupID uuid.UUID, status string, _ []jobs.LaunchStep) error {
	s.Saves = append(s.Saves, saveCall{GroupID: groupID, Status: status})
	return nil
}

func TestQueueLaunchWritesRunningBeforeInsert(t *testing.T) {
	inserter := &fakeInserter{}
	store := &recordingStore{}
	launcher := &jobs.RiverLauncher{Client: inserter, ResultStore: store}

	if err := launcher.QueueGroupLaunch(context.Background(), uuid.New()); err != nil {
		t.Fatalf("QueueGroupLaunch: %v", err)
	}

	if len(store.Saves) != 1 || store.Saves[0].Status != "running" {
		t.Fatalf("saves = %+v, want [running]", store.Saves)
	}
	if inserter.Calls != 1 {
		t.Errorf("insert calls = %d, want 1", inserter.Calls)
	}
}

func TestQueueLaunchRollbackOnInsertFailure(t *testing.T) {
	inserter := &fakeInserter{Err: context.DeadlineExceeded}
	store := &recordingStore{}
	launcher := &jobs.RiverLauncher{Client: inserter, ResultStore: store}

	err := launcher.QueueGroupLaunch(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error from insert failure")
	}

	// Verify rollback: "running" then "failed".
	if len(store.Saves) != 2 {
		t.Fatalf("saves = %d, want 2 (running + failed rollback), got %+v", len(store.Saves), store.Saves)
	}
	if store.Saves[0].Status != "running" {
		t.Errorf("first save = %q, want running", store.Saves[0].Status)
	}
	if store.Saves[1].Status != "failed" {
		t.Errorf("second save = %q, want failed (rollback)", store.Saves[1].Status)
	}
}
