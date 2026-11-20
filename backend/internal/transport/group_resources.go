package transport

// Group resources (PRD §3.3, §3.4): the Slack channels, Classroom course,
// Drive folders, and intake form of one group. Readable by coordinator/admin
// and by lecturers/TAs assigned to the group — group-scoped member access.

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	gen "launchpad/internal/transport/gen"
)

// GetGroupResources GET /api/groups/{groupId}/resources
func (h *Handlers) GetGroupResources(c echo.Context, groupId gen.GroupId) error {
	if _, err := h.requireGroupRead(c, uuid.UUID(groupId)); err != nil {
		return err
	}
	if h.groupResources == nil {
		return errNotReady("group resources are unavailable")
	}
	rows, err := h.groupResources.GroupResources(c.Request().Context(), uuid.UUID(groupId))
	if err != nil {
		return err
	}
	data := make([]gen.GroupResource, len(rows))
	for i, r := range rows {
		data[i] = gen.GroupResource{
			Provider: gen.GroupResourceProvider(r.Provider), Kind: gen.GroupResourceKind(r.Kind),
			Name: r.Name, Ref: r.Ref, Status: gen.GroupResourceStatus(r.Status),
		}
	}
	return c.JSON(http.StatusOK, data)
}
