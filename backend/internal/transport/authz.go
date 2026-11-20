package transport

// Role-based authorization (PRD §3, §7.4). Identity resolution accepts two
// credential mechanisms:
//
//  1. Bearer session token (multi-role): issued by POST /api/auth/login,
//     stored hashed server-side, revocable, expiring.
//  2. HTTP Basic "coordinator" + COORDINATOR_PASSWORD — the original
//     coordinator flow, preserved unchanged for compatibility (the deployed
//     frontend and existing operators use it). It maps to the coordinator
//     role, which has full operational access.
//
// Every policy fails closed: anonymous → 401, wrong role → 403, unverifiable
// membership → 403. Students have no API surface in the PRD (they use Google
// Forms and Slack directly), so a student token authorizes nothing.

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"launchpad/internal/core"
	"launchpad/internal/core/auth"
)

// identity is the resolved caller.
type identity struct {
	UserID   uuid.UUID // zero for the shared Basic coordinator account
	Email    string
	Role     string
	ViaBasic bool // true when authenticated through the legacy Basic flow
}

// anonymous marks an unauthenticated request.
var anonymous identity

// GroupMembershipChecker verifies that a user participates in a group
// (impl: internal/store).
type GroupMembershipChecker interface {
	IsGroupMember(ctx context.Context, userID uuid.UUID, normalizedEmail string, groupID uuid.UUID) (bool, error)
}

// errUnauthorized asks for credentials (401 + WWW-Authenticate for Basic).
func errUnauthorized(c echo.Context) error {
	c.Response().Header().Set("WWW-Authenticate", `Basic realm="Group Launchpad", charset="UTF-8"`)
	return &core.AppError{Code: core.CodeUnauthorized, Message: "credentials required"}
}

func errForbidden() error {
	return &core.AppError{Code: core.CodeForbidden, Message: "insufficient permissions for this operation"}
}

// resolveIdentity maps the request's credentials to an identity, or anonymous.
// A malformed/expired/revoked Bearer token is a hard 401 — it never degrades
// to anonymous (fail closed).
func (h *Handlers) resolveIdentity(c echo.Context) (identity, error) {
	req := c.Request()
	token, bearer := bearerToken(req)
	if bearer {
		if h.auth == nil {
			return anonymous, errUnauthorized(c)
		}
		user, err := h.auth.Authenticate(req.Context(), token)
		if err != nil {
			return anonymous, errUnauthorized(c)
		}
		return identity{UserID: user.ID, Email: user.Email, Role: user.Role}, nil
	}
	username, _, ok := req.BasicAuth()
	if ok {
		if username != coordinatorUsername {
			return anonymous, errUnauthorized(c)
		}
		// Legacy shared-coordinator flow: constant-time password check plus
		// the CSRF origin guard for writes.
		if err := h.coordinator.authorize(c); err != nil {
			return anonymous, err
		}
		return identity{Email: "coordinator", Role: auth.RoleCoordinator, ViaBasic: true}, nil
	}
	return anonymous, nil
}

// requireStaff allows coordinator and admin only (all operational writes and
// roster/notification reads).
func (h *Handlers) requireStaff(c echo.Context) (identity, error) {
	id, err := h.resolveIdentity(c)
	if err != nil {
		return id, err
	}
	if id.Role == "" {
		return anonymous, errUnauthorized(c)
	}
	if id.Role != auth.RoleCoordinator && id.Role != auth.RoleAdmin {
		return anonymous, errForbidden()
	}
	return id, nil
}

// requireAdmin allows admin only (integration/user/staff management).
func (h *Handlers) requireAdmin(c echo.Context) (identity, error) {
	id, err := h.resolveIdentity(c)
	if err != nil {
		return id, err
	}
	if id.Role == "" {
		return anonymous, errUnauthorized(c)
	}
	if id.Role != auth.RoleAdmin {
		return anonymous, errForbidden()
	}
	return id, nil
}

// requireGroupRead allows coordinator/admin for any group, and lecturer/TA
// for groups they participate in (PRD §3.3, §3.4 — "access ... resources" of
// the assigned group). Students and anonymous callers are rejected.
func (h *Handlers) requireGroupRead(c echo.Context, groupID uuid.UUID) (identity, error) {
	id, err := h.resolveIdentity(c)
	if err != nil {
		return id, err
	}
	switch id.Role {
	case auth.RoleCoordinator, auth.RoleAdmin:
		return id, nil
	case auth.RoleLecturer, auth.RoleTA:
		if h.membership == nil {
			return anonymous, errForbidden() // cannot verify membership → fail closed
		}
		member, err := h.membership.IsGroupMember(c.Request().Context(), id.UserID, normalizeEmailForAuthz(id.Email), groupID)
		if err != nil {
			return anonymous, &core.AppError{Code: core.CodeInternal, Message: "group membership could not be verified"}
		}
		if !member {
			return anonymous, errForbidden()
		}
		return id, nil
	}
	if id.Role == "" {
		return anonymous, errUnauthorized(c)
	}
	return anonymous, errForbidden()
}

// requireGroupWrite allows coordinator/admin for any group; every other role
// (including lecturers/TAs on their own group) is read-only by design.
func (h *Handlers) requireGroupWrite(c echo.Context, groupID uuid.UUID) (identity, error) {
	id, err := h.resolveIdentity(c)
	if err != nil {
		return id, err
	}
	if id.Role == "" {
		return anonymous, errUnauthorized(c)
	}
	if id.Role != auth.RoleCoordinator && id.Role != auth.RoleAdmin {
		return anonymous, errForbidden()
	}
	return id, nil
}

func normalizeEmailForAuthz(email string) string {
	return auth.NormalizeEmail(email)
}

// bearerToken extracts the token of an Authorization: Bearer header.
func bearerToken(req *http.Request) (string, bool) {
	header := req.Header.Get(echo.HeaderAuthorization)
	if header == "" {
		return "", false
	}
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", true // a Bearer header without a token is a failed auth
	}
	return token, true
}
