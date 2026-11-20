package transport

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"launchpad/internal/core/groups"
	gen "launchpad/internal/transport/gen"
)

// Compile-time proof the handlers satisfy the generated server interface.
var _ gen.ServerInterface = (*Handlers)(nil)

// ── system ────────────────────────────────────────────────────────────────

// GetHealth GET /health — liveness: the process is up.
func (h *Handlers) GetHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, gen.Health{Status: "ok"})
}

// GetReadiness GET /readyz — readiness: dependencies (PostgreSQL) reachable.
func (h *Handlers) GetReadiness(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()
	if err := h.ping(ctx); err != nil {
		return errNotReady("postgres unreachable: " + err.Error())
	}
	return c.JSON(http.StatusOK, gen.Health{Status: "ready"})
}

// ── groups ────────────────────────────────────────────────────────────────

// ListGroups GET /api/groups
func (h *Handlers) ListGroups(c echo.Context, params gen.ListGroupsParams) error {
	limit, offset := 50, 0 // spec defaults
	if params.Limit != nil {
		limit = *params.Limit
	}
	if params.Offset != nil {
		offset = *params.Offset
	}
	items, total, err := h.groups.List(c.Request().Context(), limit, offset)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, gen.GroupsPage{
		Data: toAPIGroups(items),
		Page: gen.Page{Limit: limit, Offset: offset, Total: total},
	})
}

// CreateGroup POST /api/groups
func (h *Handlers) CreateGroup(c echo.Context) error {
	if err := h.coordinator.authorize(c); err != nil {
		return err
	}
	var body gen.GroupCreate
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid request body: " + err.Error())
	}
	g, err := h.groups.Create(c.Request().Context(), groups.CreateInput{
		Name:          body.Name,
		CourseName:    body.CourseName,
		StartDate:     body.StartDate, // Date is a string alias; the service parses it
		StudentEmails: derefEmails(body.StudentEmails),
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, toAPIGroup(g))
}

// GetGroup GET /api/groups/{groupId}
func (h *Handlers) GetGroup(c echo.Context, groupId gen.GroupId) error {
	g, err := h.groups.Get(c.Request().Context(), groupId.String())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toAPIGroup(g))
}

// GetGroupOnboarding GET /api/groups/{groupId}/onboarding — exposes student
// names and emails (PRD 4.6): coordinator-only.
func (h *Handlers) GetGroupOnboarding(c echo.Context, groupId gen.GroupId) error {
	if err := h.coordinator.authorize(c); err != nil {
		return err
	}
	report, err := h.groups.Onboarding(c.Request().Context(), groupId.String())
	if err != nil {
		return err
	}
	students := make([]gen.GroupOnboardingStudent, len(report.Students))
	for i, s := range report.Students {
		students[i] = gen.GroupOnboardingStudent{
			UserId:           openapi_types.UUID(s.UserID),
			FullName:         s.FullName,
			Email:            s.Email,
			OnboardingStatus: gen.GroupOnboardingStudentOnboardingStatus(s.OnboardingStatus),
		}
	}
	// Inline anonymous struct in the generated model — bind once, locally.
	summary := struct {
		CompletionPercentage float32 `json:"completionPercentage"`
		ExpectedCount        int     `json:"expectedCount"`
		JoinedCount          int     `json:"joinedCount"`
		PendingCount         int     `json:"pendingCount"`
	}{
		CompletionPercentage: float32(report.Summary.CompletionPercentage),
		ExpectedCount:        report.Summary.ExpectedCount,
		JoinedCount:          report.Summary.JoinedCount,
		PendingCount:         report.Summary.PendingCount,
	}
	return c.JSON(http.StatusOK, gen.GroupOnboarding{
		Group:    toAPIGroup(report.Group),
		Summary:  summary,
		Students: students,
	})
}

// ── domain → API mapping ──────────────────────────────────────────────────

func toAPIGroup(g groups.Group) gen.Group {
	return gen.Group{
		Id:         openapi_types.UUID(g.ID), // matches generated openapi_types.UUID
		Name:       g.Name,
		CourseName: g.CourseName,
		StartDate:  g.StartDate.Format("2006-01-02"),
		Status:     gen.GroupStatus(g.Status),
		CreatedAt:  g.CreatedAt.UTC(),
	}
}

func toAPIGroups(gs []groups.Group) []gen.Group {
	out := make([]gen.Group, len(gs))
	for i, g := range gs {
		out[i] = toAPIGroup(g)
	}
	return out
}
