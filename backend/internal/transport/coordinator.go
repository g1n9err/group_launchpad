package transport

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"slices"

	"github.com/labstack/echo/v4"

	"launchpad/internal/core"
)

const coordinatorUsername = "coordinator"

type coordinatorAuth struct {
	configured     bool
	passwordHash   [sha256.Size]byte
	allowedOrigins []string
}

func newCoordinatorAuth(password string, origins []string) coordinatorAuth {
	return coordinatorAuth{
		configured: password != "", passwordHash: sha256.Sum256([]byte(password)),
		allowedOrigins: origins,
	}
}

func (a coordinatorAuth) authorize(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	if !a.configured {
		return errNotReady("coordinator access is not configured")
	}
	req := c.Request()
	username, password, ok := req.BasicAuth()
	hash := sha256.Sum256([]byte(password))
	passwordMatches := subtle.ConstantTimeCompare(hash[:], a.passwordHash[:]) == 1
	if !ok || username != coordinatorUsername || !passwordMatches {
		c.Response().Header().Set("WWW-Authenticate", `Basic realm="Group Launchpad coordinator", charset="UTF-8"`)
		return &core.AppError{Code: core.CodeUnauthorized, Message: "coordinator credentials required"}
	}
	if req.Method == http.MethodPost && !a.allowedWriteOrigin(c) {
		return &core.AppError{Code: core.CodeForbidden, Message: "request origin is not allowed"}
	}
	return nil
}

func (a coordinatorAuth) allowedWriteOrigin(c echo.Context) bool {
	req := c.Request()
	origin := req.Header.Get(echo.HeaderOrigin)
	if origin == "" {
		return req.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	return origin == c.Scheme()+"://"+req.Host || slices.Contains(a.allowedOrigins, origin)
}
