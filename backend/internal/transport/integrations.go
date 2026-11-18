package transport

// Integration connection handlers — thin: validate, call the core
// integrations service, map the result. The browser-facing connect/callback
// endpoints never expose client secrets or tokens (PRD 7.4).

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"launchpad/internal/core/integrations"
	gen "launchpad/internal/transport/gen"
)

// GoogleConnect GET /api/integrations/google/connect — 302 to Google consent.
func (h *Handlers) GoogleConnect(c echo.Context) error {
	if !h.google.Configured() {
		return errNotConfigured("google oauth")
	}
	consentURL, err := h.google.ConnectURL(c.Request().Context())
	if err != nil {
		return err
	}
	return c.Redirect(http.StatusFound, consentURL)
}

// GoogleCallback GET /api/integrations/google/callback?code&state[&error]
// Google redirects the browser here after consent.
func (h *Handlers) GoogleCallback(c echo.Context, params gen.GoogleCallbackParams) error {
	if !h.google.Configured() {
		return errNotConfigured("google oauth")
	}
	if params.Error != nil && *params.Error != "" {
		// access_denied and friends: user-facing, safe to echo verbatim.
		return errConsentDenied(*params.Error)
	}
	code := deref(params.Code)
	state := deref(params.State)
	status, err := h.google.Callback(c.Request().Context(), code, state)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toGoogleStatus(status))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// GoogleStatus GET /api/integrations/google/status — connection state only.
func (h *Handlers) GoogleStatus(c echo.Context) error {
	if !h.google.Configured() {
		return errNotConfigured("google oauth")
	}
	status, err := h.google.Status(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toGoogleStatus(status))
}

func toGoogleStatus(s integrations.GoogleStatus) gen.GoogleStatus {
	out := gen.GoogleStatus{Status: gen.GoogleStatusStatus(s.Status)}
	if s.AccountEmail != "" {
		out.AccountEmail = &s.AccountEmail
	}
	if len(s.Scopes) > 0 {
		out.Scopes = &s.Scopes
	}
	if !s.ConnectedAt.IsZero() {
		out.ConnectedAt = &s.ConnectedAt
	}
	return out
}
