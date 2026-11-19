package transport

// The dedicated Slack Events listener: a MINIMAL echo application exposing
// exactly one route — POST /api/integrations/slack/events. This is the only
// surface intended for public exposure (through a tunnel pointing at its
// loopback port); every other path or method returns 404/405 by construction
// because it is never registered here. The handler itself is the SAME
// Handlers.SlackEvents used by the main API — no duplicated or weakened
// security logic: signature verification, replay window, body limit, and
// deduplication all live inside the shared service.

import (
	"log/slog"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"launchpad/internal/core/integrations"
)

// NewSlackEventsListener builds the events-only echo application. It shares
// the request logger, body limit, recovery, and error-envelope conventions
// of the main API. CORS is deliberately omitted: Slack is not a browser.
func NewSlackEventsListener(logger *slog.Logger, slackEventsSvc *integrations.SlackEventsService) *echo.Echo {
	h := &Handlers{slackEvents: slackEventsSvc, logger: logger}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.Use(echomw.Recover())
	e.Use(echomw.BodyLimit("1M"))
	e.Use(requestLogger(logger))
	e.HTTPErrorHandler = errorHandler(logger)

	// The ONLY route on this listener. Unknown paths fall through to echo's
	// 404 (mapped to the standard error envelope); other methods on this
	// path yield 405.
	e.POST("/api/integrations/slack/events", h.SlackEvents)
	return e
}

// Compile-time guarantee the handler signature matches echo's HandlerFunc.
var _ echo.HandlerFunc = (*Handlers)(nil).SlackEvents
