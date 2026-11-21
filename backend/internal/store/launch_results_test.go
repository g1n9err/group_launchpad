package store_test

// SaveLaunchResult mirrors the outcome onto groups.status (the dashboard's
// "active groups" source of truth): launching → launched/partially_failed,
// and archived groups are never resurrected.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"launchpad/internal/core/groups"
	"launchpad/internal/dbmigrate"
	"launchpad/internal/jobs"
	"launchpad/internal/store"
	"launchpad/internal/testdb"
)

func TestSaveLaunchResultSyncsGroupStatus(t *testing.T) {
	url := testdb.New(t)
	if err := dbmigrate.Up(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := store.New(pool)
	ctx := context.Background()
	group, err := groups.New(repo).Create(ctx, groups.CreateInput{
		Name: "Status Sync", CourseName: "C", StartDate: "2026-12-01",
		StaffEmails: []string{"s@t.e"}, LecturerEmails: []string{"l@t.e"},
	})
	if err != nil {
		t.Fatal(err)
	}
	readStatus := func() string {
		t.Helper()
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM groups WHERE id=$1`, group.ID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	if readStatus() != "draft" {
		t.Fatalf("initial status = %s, want draft", readStatus())
	}
	steps := []jobs.LaunchStep{{Platform: "slack", Status: jobs.StepSucceeded}}
	for _, tc := range []struct{ launch, want string }{
		{"running", "launching"},
		{"completed", "launched"},
		{"partially_failed", "partially_failed"},
	} {
		if err := repo.SaveLaunchResult(ctx, group.ID, tc.launch, steps); err != nil {
			t.Fatal(err)
		}
		if got := readStatus(); got != tc.want {
			t.Errorf("after %s: group status = %s, want %s", tc.launch, got, tc.want)
		}
	}
	// Archived groups are never resurrected by a late launch write.
	if _, err := pool.Exec(ctx, `UPDATE groups SET status='archived' WHERE id=$1`, group.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveLaunchResult(ctx, group.ID, "completed", steps); err != nil {
		t.Fatal(err)
	}
	if got := readStatus(); got != "archived" {
		t.Errorf("archived group overwritten: %s", got)
	}
}
