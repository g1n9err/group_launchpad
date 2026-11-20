package transport

// Administrator user management (PRD §3.2 "manage application settings and
// permissions"). Admin-only: create accounts with roles, reset passwords,
// remove accounts, list users.

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"launchpad/internal/core"
	"launchpad/internal/core/auth"
	gen "launchpad/internal/transport/gen"
)

// ListAdminUsers GET /api/admin/users
func (h *Handlers) ListAdminUsers(c echo.Context) error {
	if _, err := h.requireAdmin(c); err != nil {
		return err
	}
	if h.auth == nil {
		return errAuthUnavailable()
	}
	users, err := h.auth.Repo.ListUsers(c.Request().Context())
	if err != nil {
		return err
	}
	data := make([]gen.AdminUser, len(users))
	for i, u := range users {
		data[i] = gen.AdminUser{
			UserId: u.ID, Email: openapi_types.Email(u.Email), FullName: u.FullName,
			Role: gen.UserRole(u.Role), CreatedAt: u.CreatedAt,
		}
	}
	return c.JSON(http.StatusOK, data)
}

// CreateAdminUser POST /api/admin/users
func (h *Handlers) CreateAdminUser(c echo.Context) error {
	if _, err := h.requireAdmin(c); err != nil {
		return err
	}
	if h.auth == nil {
		return errAuthUnavailable()
	}
	var body gen.AdminUserInput
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid request body: " + err.Error())
	}
	user, err := h.auth.CreateUserWithPassword(c.Request().Context(), string(body.Email), body.FullName, string(body.Role), body.Password)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, gen.AdminUser{
		UserId: user.ID, Email: openapi_types.Email(user.Email), FullName: user.FullName,
		Role: gen.UserRole(user.Role), CreatedAt: user.CreatedAt,
	})
}

// ResetAdminUserPassword PUT /api/admin/users/{userId}/password
func (h *Handlers) ResetAdminUserPassword(c echo.Context, userId openapi_types.UUID) error {
	if _, err := h.requireAdmin(c); err != nil {
		return err
	}
	if h.auth == nil {
		return errAuthUnavailable()
	}
	var body gen.PasswordResetInput
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid request body: " + err.Error())
	}
	if _, err := h.auth.Repo.UserByID(c.Request().Context(), uuid.UUID(userId)); err != nil {
		return errNotFound("user")
	}
	if err := h.auth.SetPassword(c.Request().Context(), uuid.UUID(userId), body.Password); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "password_reset"})
}

// DeleteAdminUser DELETE /api/admin/users/{userId}
func (h *Handlers) DeleteAdminUser(c echo.Context, userId openapi_types.UUID) error {
	id, err := h.requireAdmin(c)
	if err != nil {
		return err
	}
	if h.auth == nil {
		return errAuthUnavailable()
	}
	if id.UserID == uuid.UUID(userId) {
		return errBadRequest("admins cannot delete their own account")
	}
	// The self-deletion and last-admin protections are enforced inside the
	// deletion transaction (concurrent deletions serialize), so the HTTP
	// checks above are only an early, friendly rejection.
	if err := h.auth.DeleteAccount(c.Request().Context(), uuid.UUID(userId), id.UserID); err != nil {
		switch {
		case errors.Is(err, auth.ErrSelfDelete):
			return errBadRequest("admins cannot delete their own account")
		case errors.Is(err, auth.ErrLastAdmin):
			return errBadRequest("cannot delete the last administrator account")
		default:
			if isNotFound(err) {
				return errNotFound("user")
			}
			return err
		}
	}
	return c.NoContent(http.StatusNoContent)
}

// isNotFound reports a core NotFound domain error without importing echo.
func isNotFound(err error) bool {
	var appErr *core.AppError
	return errors.As(err, &appErr) && appErr.Code == core.CodeNotFound
}
