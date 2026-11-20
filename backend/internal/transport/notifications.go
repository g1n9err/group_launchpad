package transport

// Coordinator notifications (PRD §4.9) and announcement templates (PRD §4.8).
// Both require coordinator auth; no student PII in notifications.

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"launchpad/internal/core"
	gen "launchpad/internal/transport/gen"
)

// NotificationStore is the persistence port for notifications.
type NotificationStore interface {
	InsertNotification(ctx context.Context, groupID *uuid.UUID, kind, severity, title, detail string) error
	ListNotifications(ctx context.Context, limit, offset int) ([]NotificationRow, int, int, error)
	MarkNotificationRead(ctx context.Context, id uuid.UUID) (bool, error)
	MarkAllNotificationsRead(ctx context.Context) error
}

// NotificationRow is the transport-level notification model.
type NotificationRow struct {
	ID        uuid.UUID
	GroupID   *uuid.UUID
	Kind      string
	Severity  string
	Title     string
	Detail    string
	ReadAt    *time.Time
	CreatedAt time.Time
}

// ListNotifications GET /api/notifications
func (h *Handlers) ListNotifications(c echo.Context, params gen.ListNotificationsParams) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.notifications == nil {
		return errNotReady("notifications are unavailable")
	}
	limit, offset := 50, 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	if params.Offset != nil {
		offset = *params.Offset
	}
	rows, total, unreadCount, err := h.notifications.ListNotifications(c.Request().Context(), limit, offset)
	if err != nil {
		return err
	}
	data := make([]gen.Notification, len(rows))
	for i, n := range rows {
		data[i] = toAPINotification(n)
	}
	return c.JSON(http.StatusOK, gen.NotificationPage{
		Data:        data,
		Page:        gen.Page{Limit: limit, Offset: offset, Total: total},
		UnreadCount: unreadCount,
	})
}

// MarkNotificationRead POST /api/notifications/{id}/read
func (h *Handlers) MarkNotificationRead(c echo.Context, notificationId openapi_types.UUID) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.notifications == nil {
		return errNotReady("notifications are unavailable")
	}
	found, err := h.notifications.MarkNotificationRead(c.Request().Context(), uuid.UUID(notificationId))
	if err != nil {
		return err
	}
	if !found {
		return errNotFound("notification")
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "read"})
}

// MarkAllNotificationsRead POST /api/notifications/read-all
func (h *Handlers) MarkAllNotificationsRead(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.notifications == nil {
		return errNotReady("notifications are unavailable")
	}
	if err := h.notifications.MarkAllNotificationsRead(c.Request().Context()); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "all_read"})
}

func toAPINotification(n NotificationRow) gen.Notification {
	out := gen.Notification{
		Id:        n.ID,
		Kind:      gen.NotificationKind(n.Kind),
		Severity:  gen.NotificationSeverity(n.Severity),
		Title:     n.Title,
		CreatedAt: n.CreatedAt,
	}
	if n.GroupID != nil {
		out.GroupId = n.GroupID
	}
	if n.Detail != "" {
		out.Detail = &n.Detail
	}
	if n.ReadAt != nil {
		out.ReadAt = n.ReadAt
	}
	return out
}

func errNotFound(what string) error {
	return &core.AppError{Code: core.CodeNotFound, Message: what + " not found"}
}
