-- name: UpsertLaunchResult :exec
INSERT INTO launch_results (group_id, status, steps)
VALUES ($1, $2, $3::jsonb)
ON CONFLICT (group_id) DO UPDATE SET
    status = EXCLUDED.status,
    steps = EXCLUDED.steps,
    updated_at = now();

-- name: GetLaunchResult :one
SELECT id, group_id, status, steps, created_at, updated_at
FROM launch_results
WHERE group_id = $1;
