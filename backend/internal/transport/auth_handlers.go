package transport

// Authentication endpoints (PRD §7.4). Login exchanges email+password for a
// bearer session token; the token travels ONLY in the Authorization header —
// never in URLs — and is stored server-side as a SHA-256 hash.

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"launchpad/internal/core"
	"launchpad/internal/core/auth"
	gen "launchpad/internal/transport/gen"
)

func ptr[T any](v T) *T { return &v }

func errAuthUnavailable() error {
	return errNotReady("token authentication is not configured on the server")
}

// Login POST /api/auth/login
func (h *Handlers) Login(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	if h.auth == nil {
		return errAuthUnavailable()
	}
	var body gen.LoginInput
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid request body: " + err.Error())
	}
	// Brute-force throttling (pair/account/IP windows) BEFORE any credential
	// work: a blocked combination gets 429 whether or not the account exists
	// (no oracle), and no bcrypt work is performed. Retry-After is expressed
	// in delta-seconds (RFC 9110 §10.1.2 / RFC 7231 §7.1.3).
	throttled := h.loginThrottle
	if throttled != nil {
		ip, email := c.RealIP(), strings.ToLower(strings.TrimSpace(string(body.Email)))
		if blocked, retryAfter := throttled.blocked(ip, email); blocked {
			seconds := int(math.Ceil(retryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			c.Response().Header().Set("Retry-After", strconv.Itoa(seconds))
			return &core.AppError{Code: core.CodeRateLimited, Message: "too many failed sign-in attempts; try again later"}
		}
		if !auth.ValidateEmail(string(body.Email)) || body.Password == "" {
			throttled.fail(ip, email)
			return errUnauthorized(c)
		}
		session, err := h.auth.Login(c.Request().Context(), string(body.Email), body.Password)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) {
				throttled.fail(ip, email)
			}
			return errUnauthorized(c)
		}
		throttled.reset(ip, email)
		return c.JSON(http.StatusOK, toAuthSession(session))
	}
	if !auth.ValidateEmail(string(body.Email)) || body.Password == "" {
		return errUnauthorized(c)
	}
	session, err := h.auth.Login(c.Request().Context(), string(body.Email), body.Password)
	if err != nil {
		return errUnauthorized(c)
	}
	return c.JSON(http.StatusOK, toAuthSession(session))
}

// Logout POST /api/auth/logout — revokes the presented bearer session.
func (h *Handlers) Logout(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	if h.auth == nil {
		return errAuthUnavailable()
	}
	token, ok := bearerToken(c.Request())
	if !ok {
		return errUnauthorized(c)
	}
	if err := h.auth.Logout(c.Request().Context(), token); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "logged_out"})
}

// GetAuthMe GET /api/auth/me
func (h *Handlers) GetAuthMe(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	id, err := h.resolveIdentity(c)
	if err != nil {
		return err
	}
	if id.Role == "" {
		return errUnauthorized(c)
	}
	return c.JSON(http.StatusOK, toAuthUser(id))
}

// Refresh POST /api/auth/refresh — rotates the bearer token.
func (h *Handlers) RefreshToken(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	if h.auth == nil {
		return errAuthUnavailable()
	}
	token, ok := bearerToken(c.Request())
	if !ok || strings.TrimSpace(token) == "" {
		return errUnauthorized(c)
	}
	session, err := h.auth.Refresh(c.Request().Context(), token)
	if err != nil {
		return errUnauthorized(c)
	}
	return c.JSON(http.StatusOK, toAuthSession(session))
}

func toAuthUser(id identity) gen.AuthUser {
	user := gen.AuthUser{
		Role: gen.UserRole(id.Role),
	}
	if id.UserID != uuid.Nil {
		user.UserId = ptr(uuid.UUID(id.UserID))
	}
	// Only a real account email is emitted; the shared Basic coordinator
	// identity has none (the field is optional in the contract).
	if id.Email != "" && strings.Contains(id.Email, "@") {
		user.Email = ptr(openapi_types.Email(id.Email))
	}
	if id.ViaBasic {
		user.ViaBasic = ptr(true)
	}
	return user
}

func toAuthSession(s auth.Session) gen.AuthSession {
	user := toAuthUser(identity{UserID: s.User.ID, Email: s.User.Email, Role: s.User.Role})
	return gen.AuthSession{
		Token:     s.Token,
		ExpiresAt: s.ExpiresAt,
		User:      user,
	}
}
