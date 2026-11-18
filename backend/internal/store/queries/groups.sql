-- Group queries. sqlc compiles these against migrations/ and generates
-- type-safe Go (internal/store/gen). Column list is explicit so new columns
-- never silently change generated structs.

-- name: CreateGroup :one
INSERT INTO groups (name, course_name, start_date, status)
VALUES ($1, $2, $3, 'draft')
RETURNING id, name, course_name, start_date, status, created_at;

-- name: GetGroup :one
SELECT id, name, course_name, start_date, status, created_at
FROM groups
WHERE id = $1;

-- name: ListGroups :many
SELECT id, name, course_name, start_date, status, created_at
FROM groups
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: CountGroups :one
SELECT count(*) FROM groups;
