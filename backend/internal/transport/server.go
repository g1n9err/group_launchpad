// transport wiring: echo server, middleware chain, and the single error mapper.
// This package is deliberately thin — parse, call core, map the result.
package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"launchpad/internal/config"
	"launchpad/internal/core"
	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
	gen "launchpad/internal/transport/gen"
)

// PingFunc verifies the database dependency (used by /readyz).
type PingFunc func(ctx context.Context) error

// Handlers implements the generated gen.ServerInterface.
type Handlers struct {
	groups        *groups.Service
	google        *integrations.GoogleAuthService
	slack         *integrations.SlackAuthService
	slackChannels *integrations.SlackChannelsService
	slackEvents   *integrations.SlackEventsService
	ping          PingFunc
	logger        *slog.Logger
}

// New builds the echo application with the full middleware chain and routes.
func New(cfg config.Config, logger *slog.Logger, groupsSvc *groups.Service, googleSvc *integrations.GoogleAuthService, slackSvc *integrations.SlackAuthService, slackChannelsSvc *integrations.SlackChannelsService, slackEventsSvc *integrations.SlackEventsService, ping PingFunc) *echo.Echo {
	h := &Handlers{groups: groupsSvc, google: googleSvc, slack: slackSvc, slackChannels: slackChannelsSvc, slackEvents: slackEventsSvc, ping: ping, logger: logger}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.Use(echomw.RequestIDWithConfig(echomw.RequestIDConfig{}))
	e.Use(echomw.Recover())
	e.Use(requestLogger(logger))
	e.Use(echomw.BodyLimit("1M"))
	e.Use(echomw.CORSWithConfig(echomw.CORSConfig{
		AllowOrigins: cfg.CORSAllowedOrigins,
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderContentType, echo.HeaderAuthorization},
	}))

	e.HTTPErrorHandler = errorHandler(logger)
	gen.RegisterHandlers(e, h)
	return e
}

// ── middleware ────────────────────────────────────────────────────────────

func requestLogger(logger *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			attrs := []any{
				"req_id", c.Response().Header().Get(echo.HeaderXRequestID),
				"method", c.Request().Method,
				"path", c.Request().URL.Path,
				"status", c.Response().Status,
				"dur_ms", time.Since(start).Milliseconds(),
			}
			if err != nil {
				attrs = append(attrs, "err", err.Error())
			}
			logger.Info("http_request", attrs...)
			return err
		}
	}
}

// ── error mapping — the ONLY place domain errors become HTTP responses ────

func errorHandler(logger *slog.Logger) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}
		var appErr *core.AppError
		var httpErr *echo.HTTPError
		switch {
		case errors.As(err, &appErr):
			writeError(c, appStatus(appErr.Code), appErr)
		case errors.As(err, &httpErr):
			message := fmt.Sprint(httpErr.Message)
			if message == "" {
				message = http.StatusText(httpErr.Code)
			}
			writeError(c, httpErr.Code, &core.AppError{
				Code:    httpErrorCode(httpErr.Code),
				Message: message,
			})
		default:
			logger.Error("unhandled_error", "err", err.Error(), "path", c.Path(), "req_id", c.Response().Header().Get(echo.HeaderXRequestID))
			writeError(c, http.StatusInternalServerError, &core.AppError{
				Code:    core.CodeInternal,
				Message: "internal server error",
			})
		}
	}
}

// writeError is the single construction site of the error envelope. The inner
// object is generated as an inline anonymous struct by oapi-codegen; binding it
// to a local first keeps the literal parseable.
func writeError(c echo.Context, status int, e *core.AppError) error {
	var fieldErrors *map[string]string
	if len(e.FieldErrors) > 0 {
		fieldErrors = &e.FieldErrors
	}
	inner := struct {
		Code        string             `json:"code"`
		FieldErrors *map[string]string `json:"fieldErrors,omitempty"`
		Message     string             `json:"message"`
	}{Code: e.Code, FieldErrors: fieldErrors, Message: e.Message}
	return c.JSON(status, gen.Error{Error: inner})
}

func appStatus(code string) int {
	switch code {
	case core.CodeValidation:
		return http.StatusUnprocessableEntity // 422
	case core.CodeDuplicate:
		return http.StatusConflict // 409
	case core.CodeNotFound:
		return http.StatusNotFound // 404
	case core.CodeNotReady:
		return http.StatusServiceUnavailable // 503
	case core.CodeOAuthExchange:
		return http.StatusBadGateway // 502 — upstream (Google) rejected the exchange
	case core.CodeUnauthorized:
		return http.StatusUnauthorized // 401 — signature verification failed
	default:
		return http.StatusInternalServerError
	}
}

func httpErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "BAD_REQUEST"
	case http.StatusNotFound:
		return core.CodeNotFound
	case http.StatusMethodNotAllowed:
		return "METHOD_NOT_ALLOWED"
	case http.StatusRequestEntityTooLarge:
		return "PAYLOAD_TOO_LARGE"
	default:
		return core.CodeInternal
	}
}

// errBadRequest signals malformed transport input (bad JSON, bad query params).
func errBadRequest(message string) error {
	return &echo.HTTPError{Code: http.StatusBadRequest, Message: message}
}

// errNotReady signals a failing dependency check (maps to 503).
func errNotReady(message string) error {
	return &core.AppError{Code: core.CodeNotReady, Message: message}
}

// errNotConfigured signals a disabled integration (maps to 503).
func errNotConfigured(what string) error {
	return &core.AppError{Code: core.CodeNotReady, Message: what + " is not configured on the server"}
}

// errConsentDenied reports a user-facing OAuth failure from the provider
// (e.g. access_denied); the provider code is safe to echo.
func errConsentDenied(providerCode string) error {
	return &core.AppError{Code: core.CodeValidation, Message: "oauth consent failed: " + providerCode}
}
