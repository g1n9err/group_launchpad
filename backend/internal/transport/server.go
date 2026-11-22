// transport wiring: echo server, middleware chain, and the single error mapper.
// This package is deliberately thin — parse, call core, map the result.
package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"launchpad/internal/config"
	"launchpad/internal/core"
	"launchpad/internal/core/auth"
	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
	"launchpad/internal/jobs"
	gen "launchpad/internal/transport/gen"
)

// PingFunc verifies the database dependency (used by /readyz).
type PingFunc func(ctx context.Context) error

// Handlers implements the generated gen.ServerInterface.
type Handlers struct {
	groups                *groups.Service
	google                *integrations.GoogleAuthService
	slack                 *integrations.SlackAuthService
	slackChannels         *integrations.SlackChannelsService
	slackEvents           *integrations.SlackEventsService
	slackOAuth            *integrations.SlackOAuthService
	slackWorkspaces       integrations.SlackWorkspaceRepository
	googleWorkspaces      integrations.GoogleWorkspaceRepository
	launch                SlackLaunchService
	classroom             *integrations.ClassroomService
	drive                 *integrations.DriveService
	forms                 *integrations.FormsService
	delivery              IntakeDeliveryService
	deliveryEnabled       bool
	coordinator           coordinatorAuth
	auth                  *auth.Service          // multi-role token auth (nil ⇒ Bearer auth unavailable)
	membership            GroupMembershipChecker // lecturer/TA group scoping
	groupOwner            GroupWorkspaceOwnerChecker
	assignedGroups        AssignedGroupLister // lecturer/TA group listing
	staffAdmin            StaffAdminStore     // predefined staff CRUD (admin)
	groupResources        GroupResourceReader // per-group external resources (read)
	audit                 GroupAuditService
	auditNotifications    AuditNotificationReader
	announcements         AnnouncementService
	staffEmails           []string // global fallback
	groupStaffReader      GroupStaffReader
	groupRosterWriter     GroupRosterWriter
	launchStepWriter      LaunchStepWriter
	staffAssignmentReader jobs.StaffAssignmentReader // per-group staff (overrides global)
	groupLauncher         GroupLauncher
	launchStatus          LaunchStatusReader
	notifications         NotificationStore
	templates             TemplateStore
	loginThrottle         *loginThrottle
	ping                  PingFunc
	logger                *slog.Logger
}

// AssignedGroupLister lists the groups a lecturer/TA participates in.
type AssignedGroupLister interface {
	ListAssignedGroups(ctx context.Context, userID uuid.UUID, normalizedEmail string, limit, offset int) ([]groups.Group, int, error)
}

// StaffAdminStore manages the predefined staff list (PRD §3.2, §4.2).
type StaffAdminStore interface {
	ListPredefinedStaff(ctx context.Context) ([]PredefinedStaffRow, error)
	CreatePredefinedStaff(ctx context.Context, email, fullName string) (PredefinedStaffRow, error)
	DeletePredefinedStaff(ctx context.Context, id uuid.UUID) (bool, error)
}

// PredefinedStaffRow is the transport-level predefined staff model.
type PredefinedStaffRow struct {
	ID        uuid.UUID
	Email     string
	FullName  string
	CreatedAt time.Time
}

// GroupResourceReader reads a group's external resources (channels, course,
// folders, intake form) for group-scoped member access (PRD §3.3, §3.4).
type GroupResourceReader interface {
	GroupResources(ctx context.Context, groupID uuid.UUID) ([]GroupResourceRow, error)
}

type GroupWorkspaceOwnerChecker interface {
	GroupOwnedBy(ctx context.Context, groupID, ownerID uuid.UUID) (bool, error)
}

// GroupResourceRow is one external resource of a group.
type GroupResourceRow struct {
	Provider string // slack | google_classroom | google_drive | google_forms
	Kind     string // channel | course | folder | form
	Name     string
	Ref      string // external ID or URL
	Status   string
}

// GroupProvisioning wires the group launch and Google provisioning use cases.
type GroupProvisioning struct {
	Launch              SlackLaunchService
	GroupLauncher       GroupLauncher
	LaunchStatus        LaunchStatusReader
	Classroom           *integrations.ClassroomService
	Drive               *integrations.DriveService
	Forms               *integrations.FormsService
	Notifications       NotificationStore
	Templates           TemplateStore
	StaffAssignments    jobs.StaffAssignmentReader
	GroupStaff          GroupStaffReader
	GroupRoster         GroupRosterWriter
	LaunchSteps         LaunchStepWriter
	Delivery            IntakeDeliveryService
	DeliveryEnabled     bool
	Audit               GroupAuditService
	AuditNotifications  AuditNotificationReader
	Announcements       AnnouncementService
	Membership          GroupMembershipChecker
	AssignedGroups      AssignedGroupLister
	StaffAdmin          StaffAdminStore
	GroupResources      GroupResourceReader
	SlackOAuth          *integrations.SlackOAuthService
	SlackWorkspaces     integrations.SlackWorkspaceRepository
	GoogleWorkspaces    integrations.GoogleWorkspaceRepository
	GroupWorkspaceOwner GroupWorkspaceOwnerChecker
}

// New builds the HTTP application and optionally enables group provisioning.
// authSvc enables multi-role token authentication (nil ⇒ only the legacy
// Basic coordinator flow works, Bearer auth fails closed).
func New(cfg config.Config, logger *slog.Logger, groupsSvc *groups.Service, googleSvc *integrations.GoogleAuthService, slackSvc *integrations.SlackAuthService, slackChannelsSvc *integrations.SlackChannelsService, slackEventsSvc *integrations.SlackEventsService, authSvc *auth.Service, ping PingFunc, provisioning ...GroupProvisioning) *echo.Echo {
	h := &Handlers{groups: groupsSvc, google: googleSvc, slack: slackSvc, slackChannels: slackChannelsSvc, slackEvents: slackEventsSvc, auth: authSvc, ping: ping, logger: logger}
	h.coordinator = newCoordinatorAuth(cfg.CoordinatorPassword, cfg.CORSAllowedOrigins)
	h.loginThrottle = newLoginThrottle(cfg.LoginRateMaxOrDefault(), cfg.LoginRateWindowOrDefault())
	if len(provisioning) > 0 {
		h.launch = provisioning[0].Launch
		h.groupLauncher = provisioning[0].GroupLauncher
		h.launchStatus = provisioning[0].LaunchStatus
		h.classroom = provisioning[0].Classroom
		h.drive = provisioning[0].Drive
		h.forms = provisioning[0].Forms
		h.delivery = provisioning[0].Delivery
		h.deliveryEnabled = provisioning[0].DeliveryEnabled
		h.audit = provisioning[0].Audit
		h.auditNotifications = provisioning[0].AuditNotifications
		h.announcements = provisioning[0].Announcements
		h.notifications = provisioning[0].Notifications
		h.templates = provisioning[0].Templates
		h.staffAssignmentReader = provisioning[0].StaffAssignments
		h.groupStaffReader = provisioning[0].GroupStaff
		h.groupRosterWriter = provisioning[0].GroupRoster
		h.launchStepWriter = provisioning[0].LaunchSteps
		h.membership = provisioning[0].Membership
		h.assignedGroups = provisioning[0].AssignedGroups
		h.staffAdmin = provisioning[0].StaffAdmin
		h.groupResources = provisioning[0].GroupResources
		h.groupOwner = provisioning[0].GroupWorkspaceOwner
		h.slackOAuth = provisioning[0].SlackOAuth
		h.slackWorkspaces = provisioning[0].SlackWorkspaces
		h.googleWorkspaces = provisioning[0].GoogleWorkspaces
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	// Behind a reverse proxy the direct socket peer is the proxy itself:
	// per-IP throttle buckets would merge every user into one. When (and
	// only when) the operator declares a trusted proxy that overwrites
	// X-Forwarded-For, attribute client IPs from that header instead.
	if cfg.TrustProxy {
		e.IPExtractor = echo.ExtractIPFromXFFHeader()
	}

	e.Use(echomw.RequestIDWithConfig(echomw.RequestIDConfig{}))
	e.Use(echomw.Recover())
	e.Use(requestLogger(logger))
	e.Use(echomw.BodyLimit("1M"))
	// Every /api response is dynamic, frequently per-user and sometimes
	// PII-bearing (rosters, emails): no browser or intermediary caching.
	e.Use(noStoreAPICache)
	e.Use(echomw.CORSWithConfig(echomw.CORSConfig{
		AllowOrigins:     cfg.CORSAllowedOrigins,
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders:     []string{echo.HeaderContentType, echo.HeaderAuthorization},
		AllowCredentials: true, // HttpOnly session cookie (Path=/api/auth) for split-origin deployments
	}))

	e.HTTPErrorHandler = errorHandler(logger)
	gen.RegisterHandlers(e, h)
	// These endpoints are intentionally registered alongside generated routes
	// while their API shapes settle; all still use the same auth and error path.
	e.GET("/api/integrations/slack/connect", h.SlackOAuthConnect)
	e.GET("/api/integrations/slack/callback", h.SlackOAuthCallback)
	e.GET("/api/integrations/slack/workspaces", h.SlackWorkspaces)
	e.PUT("/api/groups/:groupId/slack/workspace", h.AssignSlackWorkspace)
	e.GET("/api/groups/:groupId/slack/workspace", h.GetSlackWorkspaceAssignment)
	e.GET("/api/integrations/google/workspaces", h.GoogleWorkspaces)
	e.GET("/api/groups/slack-workspaces", h.GroupSlackWorkspaces)
	e.PUT("/api/integrations/slack/workspaces/:workspaceId/google-account", h.AssignWorkspaceGoogleAccount)
	return e
}

// ── middleware ────────────────────────────────────────────────────────────

// noStoreAPICache sets Cache-Control: no-store on every /api response: the
// API is dynamic and per-user, and roster/delivery payloads are PII that
// must not persist in browser or intermediary caches.
func noStoreAPICache(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if strings.HasPrefix(c.Request().URL.Path, "/api") {
			c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		}
		return next(c)
	}
}

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
	case core.CodeDuplicate, core.CodeConflict:
		return http.StatusConflict // 409
	case core.CodeNotFound:
		return http.StatusNotFound // 404
	case core.CodeNotReady:
		return http.StatusServiceUnavailable // 503
	case core.CodeOAuthExchange, core.CodeUpstream:
		return http.StatusBadGateway // 502 — upstream (Google) rejected the exchange
	case core.CodeUnauthorized:
		return http.StatusUnauthorized
	case core.CodeForbidden:
		return http.StatusForbidden
	case core.CodeRateLimited:
		return http.StatusTooManyRequests
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
