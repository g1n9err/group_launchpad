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

-- name: InsertUserIfAbsent :one
-- Student ingestion (Stage 3B): create the user only when the NORMALIZED
-- email is new; DO NOTHING (no target) covers both the stock email unique
-- constraint and uq_users_email_normalized. No row returned ⇒ concurrent
-- insert won; the caller re-selects.
INSERT INTO users (full_name, email, role)
VALUES ($1, $2, 'student')
ON CONFLICT DO NOTHING
RETURNING id;

-- name: FindUserIDByNormalizedEmail :one
SELECT id FROM users
WHERE lower(btrim(email)) = $1
LIMIT 1;

-- name: InsertGroupMember :execrows
-- Idempotent membership: (group_id, user_id) conflicts are no-ops.
INSERT INTO group_members (group_id, user_id, member_role, onboarding_status)
VALUES ($1, $2, 'student', 'pending')
ON CONFLICT (group_id, user_id) DO NOTHING;

-- name: ListStudentRoster :many
-- One query for the whole roster (Stage 3C): joined-first, then name.
-- Backed by idx_group_members_group (0001).
SELECT u.id AS user_id, u.full_name, u.email, gm.onboarding_status
FROM group_members gm
JOIN users u ON u.id = gm.user_id
WHERE gm.group_id = $1 AND gm.member_role = 'student'
ORDER BY gm.onboarding_status DESC, lower(u.full_name);
