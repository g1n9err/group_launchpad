package transport

// Announcement template CRUD (PRD §4.8) — coordinator-only.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	openapi_types "github.com/oapi-codegen/runtime/types"

	gen "launchpad/internal/transport/gen"
)

var ErrTemplateNotFound = errors.New("announcement template not found")

// TemplateStore is the persistence port for announcement templates.
type TemplateStore interface {
	CreateTemplate(ctx context.Context, name, body, kind string) (TemplateRow, error)
	ListTemplates(ctx context.Context) ([]TemplateRow, error)
	GetTemplate(ctx context.Context, id uuid.UUID) (TemplateRow, error)
	UpdateTemplate(ctx context.Context, id uuid.UUID, name, body, kind string) error
	DeleteTemplate(ctx context.Context, id uuid.UUID) (bool, error)
}

// TemplateRow is the transport-level template model.
type TemplateRow struct {
	ID        uuid.UUID
	Name      string
	Body      string
	Kind      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListAnnouncementTemplates GET /api/announcement-templates
func (h *Handlers) ListAnnouncementTemplates(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.templates == nil {
		return errNotReady("announcement templates are unavailable")
	}
	rows, err := h.templates.ListTemplates(c.Request().Context())
	if err != nil {
		return err
	}
	data := make([]gen.AnnouncementTemplate, len(rows))
	for i, t := range rows {
		data[i] = toAPITemplate(t)
	}
	return c.JSON(http.StatusOK, data)
}

// CreateAnnouncementTemplate POST /api/announcement-templates
func (h *Handlers) CreateAnnouncementTemplate(c echo.Context) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.templates == nil {
		return errNotReady("announcement templates are unavailable")
	}
	var body gen.AnnouncementTemplateInput
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid request body: " + err.Error())
	}
	if body.Name == "" || body.Body == "" {
		return errBadRequest("name and body are required")
	}
	kind := "general"
	if body.Kind != nil {
		kind = string(*body.Kind)
	}
	row, err := h.templates.CreateTemplate(c.Request().Context(), body.Name, body.Body, kind)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, toAPITemplate(row))
}

// GetAnnouncementTemplate GET /api/announcement-templates/{id}
func (h *Handlers) GetAnnouncementTemplate(c echo.Context, templateId openapi_types.UUID) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.templates == nil {
		return errNotReady("announcement templates are unavailable")
	}
	row, err := h.templates.GetTemplate(c.Request().Context(), uuid.UUID(templateId))
	if err != nil {
		if errors.Is(err, ErrTemplateNotFound) {
			return errNotFound("template")
		}
		return err
	}
	return c.JSON(http.StatusOK, toAPITemplate(row))
}

// UpdateAnnouncementTemplate PUT /api/announcement-templates/{id}
func (h *Handlers) UpdateAnnouncementTemplate(c echo.Context, templateId openapi_types.UUID) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.templates == nil {
		return errNotReady("announcement templates are unavailable")
	}
	var body gen.AnnouncementTemplateInput
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid request body: " + err.Error())
	}
	if body.Name == "" || body.Body == "" {
		return errBadRequest("name and body are required")
	}
	kind := "general"
	if body.Kind != nil {
		kind = string(*body.Kind)
	}
	if err := h.templates.UpdateTemplate(c.Request().Context(), uuid.UUID(templateId), body.Name, body.Body, kind); err != nil {
		if errors.Is(err, ErrTemplateNotFound) {
			return errNotFound("template")
		}
		return err
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

// DeleteAnnouncementTemplate DELETE /api/announcement-templates/{id}
func (h *Handlers) DeleteAnnouncementTemplate(c echo.Context, templateId openapi_types.UUID) error {
	if _, err := h.requireStaff(c); err != nil {
		return err
	}
	if h.templates == nil {
		return errNotReady("announcement templates are unavailable")
	}
	found, err := h.templates.DeleteTemplate(c.Request().Context(), uuid.UUID(templateId))
	if err != nil {
		return err
	}
	if !found {
		return errNotFound("template")
	}
	return c.NoContent(http.StatusNoContent)
}

func toAPITemplate(t TemplateRow) gen.AnnouncementTemplate {
	return gen.AnnouncementTemplate{
		Id: t.ID, Name: t.Name, Body: t.Body,
		Kind:      gen.AnnouncementTemplateKind(t.Kind),
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}
