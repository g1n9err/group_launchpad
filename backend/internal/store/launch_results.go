// Package store — launch result persistence (unified multi-platform launch).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"launchpad/internal/jobs"
	"launchpad/internal/store/gen"
)

// Compile-time proof.
var _ jobs.LaunchResultStore = (*Repo)(nil)

// SavedLaunchResult is what the status endpoint returns.
type SavedLaunchResult struct {
	GroupID   uuid.UUID
	Status    string
	Steps     []jobs.LaunchStep
	CreatedAt time.Time
	UpdatedAt time.Time
}

// launchStatusToGroupStatus maps a unified launch outcome onto the group's
// lifecycle status (PRD group.status). Empty = leave the group untouched.
func launchStatusToGroupStatus(launchStatus string) string {
	switch launchStatus {
	case "running":
		return "launching"
	case "completed":
		return "launched"
	case "partially_failed", "failed":
		return launchStatus
	}
	return ""
}

// SaveLaunchResult persists the unified launch outcome (idempotent upsert)
// and mirrors it onto the group's lifecycle status so dashboards reflect
// reality: launching → launched / partially_failed / failed. Archived
// groups are never resurrected.
func (r *Repo) SaveLaunchResult(ctx context.Context, groupID uuid.UUID, status string, steps []jobs.LaunchStep) error {
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return fmt.Errorf("store: marshal launch steps: %w", err)
	}
	if err := r.q.UpsertLaunchResult(ctx, gen.UpsertLaunchResultParams{
		GroupID: groupID,
		Status:  status,
		Column3: stepsJSON,
	}); err != nil {
		return fmt.Errorf("store: upsert launch result: %w", err)
	}
	if groupStatus := launchStatusToGroupStatus(status); groupStatus != "" {
		if _, err := r.pool.Exec(ctx,
			`UPDATE groups SET status = $2 WHERE id = $1 AND status <> 'archived' AND status <> $2`,
			groupID, groupStatus); err != nil {
			return fmt.Errorf("store: sync group status: %w", err)
		}
	}
	return nil
}

// GetLaunchResult implements transport.LaunchStatusReader.
func (r *Repo) GetLaunchResult(ctx context.Context, groupID uuid.UUID) (string, json.RawMessage, bool, error) {
	row, err := r.q.GetLaunchResult(ctx, groupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, false, nil
		}
		return "", nil, false, fmt.Errorf("store: get launch result: %w", err)
	}
	return row.Status, json.RawMessage(row.Steps), true, nil
}
