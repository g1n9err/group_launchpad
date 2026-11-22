package transport

// Integration connection handlers — thin: validate, call the core
// integrations service, map the result. The browser-facing connect/callback
// endpoints never expose client secrets or tokens (PRD 7.4).

import (
	"html"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"launchpad/internal/core/integrations"
	gen "launchpad/internal/transport/gen"
)

// GoogleConnect GET /api/integrations/google/connect — starts the Google
// consent flow. Two caller shapes:
//
//   - Browser NAVIGATION (no session header): classic 302 to Google. Clients
//     that use the legacy Basic flow must send that Authorization header;
//     this API does not trigger a browser-native credential dialog.
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
	_, authErr := h.requireStaff(c)
	status, err := h.google.Status(c.Request().Context())
	if err != nil {
		return err
	}
	// Try staff auth — on failure return the safe (public) projection.
	if authErr != nil {
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
	id, staffErr := h.requireStaff(c)
	if staffErr == nil && id.Role == "coordinator" && id.UserID != uuid.Nil {
		if h.slackWorkspaces == nil {
			return c.JSON(http.StatusOK, gen.SlackStatus{Status: gen.SlackStatusStatus(integrations.StatusDisconnected)})
		}
		workspaces, err := h.slackWorkspaces.ListSlackWorkspaces(c.Request().Context())
		if err != nil {
			return err
		}
		if len(workspaces) == 0 {
			return c.JSON(http.StatusOK, gen.SlackStatus{Status: gen.SlackStatusStatus(integrations.StatusDisconnected)})
		}
		teamID, teamName := workspaces[0].TeamID, workspaces[0].TeamName
		return c.JSON(http.StatusOK, gen.SlackStatus{Status: gen.SlackStatusStatus(workspaces[0].Status), TeamId: &teamID, TeamName: &teamName, ConnectedAt: &workspaces[0].ConnectedAt})
	}
	if !h.slack.Configured() {
		if h.slackWorkspaces != nil {
			if staffErr != nil {
				return errUnauthorized(c)
			}
			workspaces, err := h.slackWorkspaces.ListSlackWorkspaces(c.Request().Context())
			if err != nil {
				return err
			}
			if len(workspaces) > 0 {
				status := gen.SlackStatus{Status: gen.SlackStatusStatus(workspaces[0].Status)}
				teamID, teamName := workspaces[0].TeamID, workspaces[0].TeamName
				status.TeamId, status.TeamName, status.ConnectedAt = &teamID, &teamName, &workspaces[0].ConnectedAt
				return c.JSON(http.StatusOK, status)
			}
		}
		return errNotConfigured("slack")
	}
	if staffErr != nil {
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

func (h *Handlers) SlackOAuthConnect(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.slackOAuth == nil || !h.slackOAuth.Configured() {
		return errNotConfigured("Slack workspace OAuth")
	}
	url, err := h.slackOAuth.ConnectURL(c.Request().Context())
	if err != nil {
		return err
	}
	if strings.Contains(c.Request().Header.Get(echo.HeaderAccept), "application/json") {
		return c.JSON(http.StatusOK, map[string]string{"url": url})
	}
	return c.Redirect(http.StatusFound, url)
}

func (h *Handlers) SlackOAuthCallback(c echo.Context) error {
	if h.slackOAuth == nil || !h.slackOAuth.Configured() {
		return errNotConfigured("Slack workspace OAuth")
	}
	if providerErr := c.QueryParam("error"); providerErr != "" {
		return errConsentDenied(providerErr)
	}
	workspace, err := h.slackOAuth.Callback(c.Request().Context(), c.QueryParam("code"), c.QueryParam("state"))
	if err != nil {
		return err
	}
	return c.HTML(http.StatusOK, "<!doctype html><html><meta charset=\"utf-8\"><title>Slack connected</title><body><p>Slack workspace <strong>"+html.EscapeString(workspace.TeamName)+"</strong> connected. You can close this tab and return to Group Launchpad.</p><script>if(window.opener){window.opener.postMessage({type:'slack-workspace-connected'},location.origin);window.close()}</script></body></html>")
}

func (h *Handlers) SlackWorkspaces(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.slackWorkspaces == nil {
		return errNotConfigured("Slack workspaces")
	}
	items, err := h.slackWorkspaces.ListSlackWorkspaces(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (h *Handlers) AssignSlackWorkspace(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.slackWorkspaces == nil {
		return errNotConfigured("Slack workspaces")
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		return errBadRequest("invalid group id")
	}
	if _, err := h.requireGroupWrite(c, groupID); err != nil {
		return err
	}
	var body struct {
		WorkspaceID string `json:"workspaceId"`
	}
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid request body")
	}
	var id uuid.UUID
	if body.WorkspaceID != "" {
		id, err = uuid.Parse(body.WorkspaceID)
		if err != nil {
			return errBadRequest("invalid workspace id")
		}
	}
	if err := h.slackWorkspaces.SetGroupSlackWorkspace(c.Request().Context(), groupID, id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handlers) GetSlackWorkspaceAssignment(c echo.Context) error {
	if h.slackWorkspaces == nil {
		return errNotConfigured("Slack workspaces")
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		return errBadRequest("invalid group id")
	}
	if _, err := h.requireGroupRead(c, groupID); err != nil {
		return err
	}
	id, assigned, err := h.slackWorkspaces.GroupSlackWorkspace(c.Request().Context(), groupID)
	if err != nil {
		return err
	}
	if assigned {
		return c.JSON(http.StatusOK, map[string]string{"workspaceId": id.String()})
	}
	return c.JSON(http.StatusOK, map[string]string{"workspaceId": ""})
}

func (h *Handlers) GoogleWorkspaces(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.googleWorkspaces == nil {
		return errNotConfigured("Google accounts")
	}
	items, err := h.googleWorkspaces.ListGoogleWorkspaces(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (h *Handlers) GroupSlackWorkspaces(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.slackWorkspaces == nil {
		return errNotConfigured("Slack workspaces")
	}
	items, err := h.slackWorkspaces.ListGroupSlackWorkspaceAssignments(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (h *Handlers) AssignWorkspaceGoogleAccount(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.slackWorkspaces == nil {
		return errNotConfigured("Slack workspaces")
	}
	slackID, err := uuid.Parse(c.Param("workspaceId"))
	if err != nil {
		return errBadRequest("invalid Slack workspace id")
	}
	var body struct {
		GoogleAccountID string `json:"googleAccountId"`
	}
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid request body")
	}
	var googleID uuid.UUID
	if body.GoogleAccountID != "" {
		googleID, err = uuid.Parse(body.GoogleAccountID)
		if err != nil {
			return errBadRequest("invalid Google account id")
		}
	}
	if err := h.slackWorkspaces.SetSlackWorkspaceGoogleAccount(c.Request().Context(), slackID, googleID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
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
		InviteEmails:    derefStrings(body.InviteEmails),
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

// derefStrings unwraps an optional string list (email LISTS are plain
// strings in the contract — validation with field-level errors lives in
// core/groups, not in the OpenAPI format: email regex).
func derefStrings(list *[]string) []string {
	if list == nil {
		return nil
	}
	return *list
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
