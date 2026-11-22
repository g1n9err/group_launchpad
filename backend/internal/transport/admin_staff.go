package transport

// Predefined staff management. Each coordinator maintains a private fallback
// list; platform admins can inspect/manage legacy unowned entries.

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"launchpad/internal/core"
	"launchpad/internal/core/auth"
	gen "launchpad/internal/transport/gen"
)

// ListPredefinedStaff GET /api/admin/staff
func (h *Handlers) ListPredefinedStaff(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.staffAdmin == nil {
		return errNotReady("predefined staff management is unavailable")
	}
	rows, err := h.staffAdmin.ListPredefinedStaff(c.Request().Context())
	if err != nil {
		return err
	}
	data := make([]gen.PredefinedStaff, len(rows))
	for i, s := range rows {
		data[i] = toAPIStaff(s)
	}
	return c.JSON(http.StatusOK, data)
}

// CreatePredefinedStaff POST /api/admin/staff
func (h *Handlers) CreatePredefinedStaff(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.staffAdmin == nil {
		return errNotReady("predefined staff management is unavailable")
	}
	var body gen.PredefinedStaffInput
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid request body: " + err.Error())
	}
	email := strings.ToLower(strings.TrimSpace(string(body.Email)))
	if !auth.ValidateEmail(email) {
		return core.Validation("invalid staff data", map[string]string{"email": "must be a valid email address"})
	}
	fullName := ""
	if body.FullName != nil {
		fullName = strings.TrimSpace(*body.FullName)
	}
	row, err := h.staffAdmin.CreatePredefinedStaff(c.Request().Context(), email, fullName)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, toAPIStaff(row))
}

// DeletePredefinedStaff DELETE /api/admin/staff/{staffId}
func (h *Handlers) DeletePredefinedStaff(c echo.Context, staffId openapi_types.UUID) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.staffAdmin == nil {
		return errNotReady("predefined staff management is unavailable")
	}
	found, err := h.staffAdmin.DeletePredefinedStaff(c.Request().Context(), uuid.UUID(staffId))
	if err != nil {
		return err
	}
	if !found {
		return errNotFound("predefined staff")
	}
	return c.NoContent(http.StatusNoContent)
}

func toAPIStaff(s PredefinedStaffRow) gen.PredefinedStaff {
	return gen.PredefinedStaff{
		StaffId: s.ID, Email: openapi_types.Email(s.Email), FullName: s.FullName, CreatedAt: s.CreatedAt,
	}
}
