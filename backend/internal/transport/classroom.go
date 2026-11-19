package transport

import (
	"net/http"

	"github.com/labstack/echo/v4"

	gen "launchpad/internal/transport/gen"
)

func (h *Handlers) ProvisionGroupClassroom(c echo.Context, groupID gen.GroupId) error {
	if err := h.coordinator.authorize(c); err != nil {
		return err
	}
	if h.classroom == nil {
		return errNotReady("Google Classroom is unavailable")
	}
	ctx := c.Request().Context()
	group, err := h.groups.Get(ctx, groupID.String())
	if err != nil {
		return err
	}
	id, err := h.classroom.Provision(ctx, group.ID, group.CourseName, group.Name, group.StartDate)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, gen.ClassroomCourse{CourseId: id})
}
