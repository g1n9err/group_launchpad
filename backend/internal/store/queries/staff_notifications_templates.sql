-- Staff assignments (PRD §4.1) — per-group lecturer/TA/teaching_staff.
-- name: CreateStaffAssignment :execrows
INSERT INTO staff_assignments (group_id, email, role)
VALUES ($1, $2, $3)
ON CONFLICT (group_id, email) DO NOTHING;

-- name: ListStaffAssignments :many
SELECT id, group_id, email, role, created_at
FROM staff_assignments
WHERE group_id = $1
ORDER BY role, email;

-- name: DeleteStaffAssignment :exec
DELETE FROM staff_assignments
WHERE group_id = $1 AND email = $2;

-- Notifications (PRD §4.9).
-- name: InsertNotification :exec
INSERT INTO notifications (group_id, kind, severity, title, detail)
VALUES ($1, $2, $3, $4, $5);

-- name: ListNotifications :many
SELECT id, group_id, kind, severity, title, detail, read_at, created_at
FROM notifications
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountNotifications :one
SELECT count(*) FROM notifications;

-- name: CountUnreadNotifications :one
SELECT count(*) FROM notifications WHERE read_at IS NULL;

-- name: MarkNotificationRead :execrows
UPDATE notifications SET read_at = now()
WHERE id = $1 AND read_at IS NULL;

-- name: MarkAllNotificationsRead :exec
UPDATE notifications SET read_at = now() WHERE read_at IS NULL;

-- Announcement templates (PRD §4.8).
-- name: CreateAnnouncementTemplate :one
INSERT INTO announcement_templates (name, body, kind)
VALUES ($1, $2, $3)
RETURNING id, name, body, kind, created_at, updated_at;

-- name: ListAnnouncementTemplates :many
SELECT id, name, body, kind, created_at, updated_at
FROM announcement_templates
ORDER BY name;

-- name: GetAnnouncementTemplate :one
SELECT id, name, body, kind, created_at, updated_at
FROM announcement_templates
WHERE id = $1;

-- name: UpdateAnnouncementTemplate :exec
UPDATE announcement_templates
SET name = $2, body = $3, kind = $4, updated_at = now()
WHERE id = $1;

-- name: DeleteAnnouncementTemplate :execrows
DELETE FROM announcement_templates WHERE id = $1;
