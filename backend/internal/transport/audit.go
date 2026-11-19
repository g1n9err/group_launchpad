package transport

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"launchpad/internal/core/audit"
	gen "launchpad/internal/transport/gen"
)

type GroupAuditService interface {
	QueueAudit(context.Context, uuid.UUID) (int64, error)
	Recent(context.Context, uuid.UUID) ([]audit.Snapshot, error)
}

func (h *Handlers) QueueGroupAudit(c echo.Context, groupID gen.GroupId) error {
	if err := h.coordinator.authorize(c); err != nil {
		return err
	}
	if h.audit == nil {
		return errNotReady("group audits are unavailable")
	}
	ctx := c.Request().Context()
	if _, err := h.groups.Get(ctx, groupID.String()); err != nil {
		return err
	}
	jobID, err := h.audit.QueueAudit(ctx, groupID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, gen.GroupAuditQueued{JobId: jobID})
}

func (h *Handlers) ListGroupAudits(c echo.Context, groupID gen.GroupId) error {
	if err := h.coordinator.authorize(c); err != nil {
		return err
	}
	if h.audit == nil {
		return errNotReady("group audits are unavailable")
	}
	ctx := c.Request().Context()
	if _, err := h.groups.Get(ctx, groupID.String()); err != nil {
		return err
	}
	history, err := h.audit.Recent(ctx, groupID)
	if err != nil {
		return err
	}
	response := make([]gen.GroupAudit, 0, len(history))
	for _, run := range history {
		response = append(response, gen.GroupAudit{
			Id: run.ID, GroupId: run.GroupID, Type: gen.GroupAuditType(run.Type), CheckedAt: run.CheckedAt,
			Results: toAuditResults(run.Results), Issues: toAuditResults(run.Issues),
		})
	}
	return c.JSON(http.StatusOK, response)
}

func toAuditResults(results []audit.Result) []gen.GroupAuditResource {
	response := make([]gen.GroupAuditResource, 0, len(results))
	for _, result := range results {
		response = append(response, gen.GroupAuditResource{
			Provider: result.Provider, Kind: result.Kind, Key: result.Key, Status: gen.GroupAuditResourceStatus(result.Status),
		})
	}
	return response
}
