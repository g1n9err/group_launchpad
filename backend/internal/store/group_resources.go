// Group-scoped external resources for member access (PRD §3.3, §3.4).
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"launchpad/internal/transport"
)

// GroupResources returns the active external resources of a group: Slack
// channels, the Classroom course, Drive folders, and the intake form URL.
// Implements transport.GroupResourceReader.
func (r *Repo) GroupResources(ctx context.Context, groupID uuid.UUID) ([]transport.GroupResourceRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT provider, resource_type, resource_name, external_resource_id, status
		FROM group_resources
		WHERE group_id = $1 AND status = 'active'
		ORDER BY provider, resource_type, resource_name`, groupID)
	if err != nil {
		return nil, fmt.Errorf("store: group resources: %w", err)
	}
	defer rows.Close()
	out := []transport.GroupResourceRow{}
	for rows.Next() {
		var row transport.GroupResourceRow
		if err := rows.Scan(&row.Provider, &row.Kind, &row.Name, &row.Ref, &row.Status); err != nil {
			return nil, fmt.Errorf("store: scan group resource: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The intake form's member-facing reference is its responder URL.
	var formURL *string
	if err := r.pool.QueryRow(ctx,
		`SELECT form_url FROM intake_forms WHERE group_id = $1 AND form_url IS NOT NULL AND status IN ('created','distributed')`,
		groupID).Scan(&formURL); err == nil && formURL != nil {
		out = append(out, transport.GroupResourceRow{
			Provider: "google_forms", Kind: "form", Name: "Student intake form", Ref: *formURL, Status: "active",
		})
	}
	return out, nil
}
