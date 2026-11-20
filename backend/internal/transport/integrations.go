package transport

// Integration connection handlers — thin: validate, call the core
// integrations service, map the result. The browser-facing connect/callback
// endpoints never expose client secrets or tokens (PRD 7.4).

import (
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"launchpad/internal/core/integrations"
	gen "launchpad/internal/transport/gen"
)

// GoogleConnect GET /api/integrations/google/connect — starts the Google
// consent flow. Two caller shapes:
//
//   - Browser NAVIGATION (no session header): classic 302 to Google. The
//     legacy Basic coordinator dialog covers this path.
//   - Authenticated fetch with Accept: application/json (the SPA's button):
//     returns {"url": ...} so the app can navigate with the in-memory
//     Bearer session — no Basic dialog, no token in URLs.
func (h *Handlers) GoogleConnect(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if !h.google.Configured() {
		return errNotConfigured("google oauth")
	}
	consentURL, err := h.google.ConnectURL(c.Request().Context())
	if err != nil {
		return err
	}
	if strings.Contains(c.Request().Header.Get(echo.HeaderAccept), "application/json") {
		return c.JSON(http.StatusOK, map[string]string{"url": consentURL})
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

// GoogleStatus GET /api/integrations/google/status — public endpoint that
// returns the connection state. Staff-only metadata (account email,
// scopes, connected-at) is stripped for non-staff callers; the full status
// is returned only with valid coordinator/admin credentials.
func (h *Handlers) GoogleStatus(c echo.Context) error {
	if !h.google.Configured() {
		return errNotConfigured("google oauth")
	}
	status, err := h.google.Status(c.Request().Context())
	if err != nil {
		return err
	}
	// Try staff auth — on failure return the safe (public) projection.
	if _, authErr := h.requireStaff(c); authErr != nil {
		return c.JSON(http.StatusOK, gen.GoogleStatus{
			Status: gen.GoogleStatusStatus(status.Status),
		})
	}
	return c.JSON(http.StatusOK, toGoogleStatus(status))
}

// SlackStatus GET /api/integrations/slack/status — verifies the bot token
// (one auth.test call), persists the outcome, and returns the state. The
// token itself never crosses this boundary. Anonymous and non-staff callers
// receive ONLY the persisted connection state (stripped projection, no live
// call, no writes); the live check and workspace metadata require
// coordinator/admin credentials.
func (h *Handlers) SlackStatus(c echo.Context) error {
	if !h.slack.Configured() {
		return errNotConfigured("slack")
	}
	if _, authErr := h.requireStaff(c); authErr != nil {
		stored, err := h.slack.StoredStatus(c.Request().Context())
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, gen.SlackStatus{Status: gen.SlackStatusStatus(stored.Status)})
	}
	status, err := h.slack.CheckConnection(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toSlackStatus(status))
}

// ProvisionGroupSlack POST /api/groups/{groupId}/slack/provision — idempotent
// channel + invitation + welcome provisioning. Group data comes from the
// database; invite emails come from the (optional) request body — staff data
// is never hardcoded.
func (h *Handlers) ProvisionGroupSlack(c echo.Context, groupId gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, uuid.UUID(groupId)); err != nil {
		return err
	}
	if !h.slackChannels.Configured() {
		return errNotConfigured("slack")
	}
	g, err := h.groups.Get(c.Request().Context(), groupId.String())
	if err != nil {
		return err
	}

	var body gen.SlackProvisionRequest
	if c.Request().ContentLength != 0 {
		if err := c.Bind(&body); err != nil {
			return errBadRequest("invalid request body: " + err.Error())
		}
	}

	result, err := h.slackChannels.Provision(c.Request().Context(), integrations.ProvisionInput{
		GroupID:         g.ID,
		GroupName:       g.Name,
		CourseName:      g.CourseName,
		StartDate:       g.StartDate.Format("2006-01-02"),
		InviteEmails:    derefEmails(body.InviteEmails),
		WelcomeTemplate: deref(body.WelcomeTemplate),
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toProvisionResult(result))
}

// SlackEvents POST /api/integrations/slack/events — the public webhook.
// The RAW body is read and signature-verified BEFORE any parsing (echo's
// binder never touches it). Auth is the signing secret, not the bot token.
func (h *Handlers) SlackEvents(c echo.Context) error {
	if !h.slackEvents.Configured() {
		return errNotConfigured("slack events")
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, 1<<20)) // bounded: 1 MiB
	if err != nil {
		return errBadRequest("could not read request body")
	}
	ack, err := h.slackEvents.Handle(c.Request().Context(), integrations.SlackEventRequest{
		Timestamp: c.Request().Header.Get("X-Slack-Request-Timestamp"),
		Signature: c.Request().Header.Get("X-Slack-Signature"),
		RawBody:   string(raw),
	})
	if err != nil {
		return err // sanitized AppError from the service (401/400/503)
	}
	if ack.Kind == "challenge" {
		return c.JSON(http.StatusOK, gen.SlackEventAck{Challenge: &ack.Challenge})
	}
	status := gen.SlackEventAckStatus(ack.Kind)
	return c.JSON(http.StatusOK, gen.SlackEventAck{Status: &status})
}

func derefEmails(list *[]openapi_types.Email) []string {
	if list == nil {
		return nil
	}
	out := make([]string, len(*list))
	for i, e := range *list {
		out[i] = string(e)
	}
	return out
}

func toProvisionResult(r integrations.ProvisionResult) gen.SlackProvisionResult {
	channels := make([]gen.SlackChannelResult, len(r.Channels))
	for i, ch := range r.Channels {
		channels[i] = gen.SlackChannelResult{Name: ch.Name, ChannelId: &ch.ChannelID, Status: gen.SlackChannelResultStatus(ch.Status)}
		if ch.Error != "" {
			channels[i].Error = &ch.Error
		}
	}
	invites := make([]gen.SlackInviteResult, len(r.Invites))
	for i, iv := range r.Invites {
		invites[i] = gen.SlackInviteResult{Email: iv.Email, ChannelName: iv.ChannelName, Status: gen.SlackInviteResultStatus(iv.Status)}
		if iv.Error != "" {
			invites[i].Error = &iv.Error
		}
	}
	welcomes := make([]gen.SlackWelcomeResult, len(r.Welcomes))
	for i, w := range r.Welcomes {
		welcomes[i] = gen.SlackWelcomeResult{ChannelName: w.ChannelName, Status: gen.SlackWelcomeResultStatus(w.Status)}
		if w.Error != "" {
			welcomes[i].Error = &w.Error
		}
	}
	return gen.SlackProvisionResult{Channels: channels, Invites: invites, WelcomeMessages: welcomes}
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

func toSlackStatus(s integrations.SlackStatus) gen.SlackStatus {
	out := gen.SlackStatus{Status: gen.SlackStatusStatus(s.Status)}
	if s.TeamID != "" {
		out.TeamId = &s.TeamID
	}
	if s.TeamName != "" {
		out.TeamName = &s.TeamName
	}
	if !s.ConnectedAt.IsZero() {
		out.ConnectedAt = &s.ConnectedAt
	}
	return out
}
