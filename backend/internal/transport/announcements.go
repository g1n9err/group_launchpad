package transport

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"launchpad/internal/core/announcements"
	gen "launchpad/internal/transport/gen"
)

type AnnouncementService interface {
	Schedule(context.Context, uuid.UUID, announcements.Schedule) (announcements.Announcement, error)
	List(context.Context, uuid.UUID) ([]announcements.Announcement, error)
}

func (h *Handlers) ScheduleGroupAnnouncement(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, uuid.UUID(groupID)); err != nil {
		return err
	}
	if h.announcements == nil || !h.slackChannels.Configured() {
		return errNotReady("Slack announcements are unavailable")
	}
	ctx := c.Request().Context()
	if _, err := h.groups.Get(ctx, groupID.String()); err != nil {
		return err
	}
	var body gen.AnnouncementSchedule
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid announcement schedule")
	}

	// Template reference (PRD §4.8): when a templateId is provided,
	// load the template body and use it as the message.
	var message string
	if body.Message != nil {
		message = *body.Message
	}
	if body.TemplateId != nil && *body.TemplateId != uuid.Nil {
		if h.templates == nil {
			return errNotReady("announcement templates are unavailable")
		}
		template, err := h.templates.GetTemplate(ctx, *body.TemplateId)
		if err != nil {
			if errors.Is(err, ErrTemplateNotFound) {
				return errNotFound("template")
			}
			return err
		}
		message = template.Body
	}
	if message == "" {
		return errBadRequest("message or templateId is required")
	}

	item, err := h.announcements.Schedule(ctx, groupID, announcements.Schedule{
		RequestID: body.RequestId, ChannelName: body.ChannelName, Type: string(body.Type),
		Message: message, ScheduledAt: body.ScheduledAt,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, toAnnouncement(item))
}

func (h *Handlers) ListGroupAnnouncements(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupRead(c, uuid.UUID(groupID)); err != nil {
		return err
	}
	if h.announcements == nil {
		return errNotReady("Slack announcement access is unavailable")
	}
	ctx := c.Request().Context()
	if _, err := h.groups.Get(ctx, groupID.String()); err != nil {
		return err
	}
	items, err := h.announcements.List(ctx, groupID)
	if err != nil {
		return err
	}
	result := make([]gen.ScheduledAnnouncement, 0, len(items))
	for _, item := range items {
		result = append(result, toAnnouncement(item))
	}
	return c.JSON(http.StatusOK, result)
}

func toAnnouncement(item announcements.Announcement) gen.ScheduledAnnouncement {
	msg := item.Message
	result := gen.ScheduledAnnouncement{
		Id: item.ID, GroupId: item.GroupID, Status: gen.ScheduledAnnouncementStatus(item.Status),
		PublishedAt: item.PublishedAt,
		Schedule: gen.AnnouncementSchedule{
			RequestId: item.RequestID, ChannelName: item.ChannelName, Type: gen.AnnouncementScheduleType(item.Type),
			Message: &msg, ScheduledAt: item.ScheduledAt,
		},
	}
	if item.ExternalMessageID != "" {
		result.SlackMessageId = &item.ExternalMessageID
	}
	if item.Error != "" {
		result.Error = &item.Error
	}
	return result
}
