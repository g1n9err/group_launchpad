package transport

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	"launchpad/internal/core/audit"
	gen "launchpad/internal/transport/gen"
)

type AuditNotificationReader interface {
	AuditNotifications(context.Context) ([]audit.Notification, error)
	CurrentAuditIssues(context.Context) ([]audit.OpenIssues, error)
}

func (h *Handlers) GetAuditNotifications(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.auditNotifications == nil {
		return errNotReady("audit notifications are unavailable")
	}
	ctx := c.Request().Context()
	openIssues, err := h.auditNotifications.CurrentAuditIssues(ctx)
	if err != nil {
		return err
	}
	notifications, err := h.auditNotifications.AuditNotifications(ctx)
	if err != nil {
		return err
	}
	result := gen.AuditNotifications{
		OpenIssues:    make([]gen.OpenAuditIssues, 0, len(openIssues)),
		Notifications: make([]gen.AuditNotification, 0, len(notifications)),
	}
	for _, group := range openIssues {
		result.OpenIssues = append(result.OpenIssues, gen.OpenAuditIssues{
			GroupId: group.GroupID, GroupName: group.GroupName, CheckedAt: group.CheckedAt, Issues: toAuditResults(group.Issues),
		})
	}
	for _, notification := range notifications {
		item := gen.AuditNotification{
			Id: notification.ID, GroupId: notification.GroupID, GroupName: notification.GroupName,
			CheckedAt: notification.CheckedAt, EmailStatus: gen.AuditNotificationEmailStatus(notification.EmailStatus),
			Changes: make([]gen.AuditChange, 0, len(notification.Changes)),
		}
		if notification.EmailError != "" {
			item.EmailError = &notification.EmailError
		}
		for _, change := range notification.Changes {
			item.Changes = append(item.Changes, gen.AuditChange{
				Provider: change.Provider, Kind: change.Kind, Key: change.Key, Before: change.Before, After: change.After,
			})
		}
		result.Notifications = append(result.Notifications, item)
	}
	return c.JSON(http.StatusOK, result)
}
