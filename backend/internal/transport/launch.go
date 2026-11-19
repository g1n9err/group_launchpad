package transport

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
	gen "launchpad/internal/transport/gen"
)

type SlackLaunchService interface {
	QueueLaunch(context.Context, uuid.UUID) (groups.SlackLaunch, error)
	Status(context.Context, uuid.UUID) (groups.SlackLaunch, error)
}

func (h *Handlers) LaunchGroup(c echo.Context, groupID gen.GroupId) error {
	if err := h.coordinator.authorize(c); err != nil {
		return err
	}
	if h.launch == nil || !h.slackChannels.Configured() {
		return errNotReady("Slack launch is unavailable")
	}
	ctx := c.Request().Context()
	group, err := h.groups.Get(ctx, groupID.String())
	if err != nil {
		return err
	}
	if _, err := integrations.ChannelName(group.Name, integrations.DefaultChannelSuffixes[0]); err != nil {
		return err
	}
	run, err := h.launch.QueueLaunch(ctx, group.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, slackLaunchResponse(run))
}

func (h *Handlers) GetGroupLaunchStatus(c echo.Context, groupID gen.GroupId) error {
	if err := h.coordinator.authorize(c); err != nil {
		return err
	}
	if h.launch == nil {
		return errNotReady("launch status is unavailable")
	}
	run, err := h.launch.Status(c.Request().Context(), uuid.UUID(groupID))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, slackLaunchResponse(run))
}

func slackLaunchResponse(run groups.SlackLaunch) gen.SlackLaunch {
	response := gen.SlackLaunch{
		Id: run.ID, GroupId: run.GroupID, Status: gen.SlackLaunchStatus(run.Status),
		Attempts: run.Attempts, CreatedAt: run.CreatedAt, CompletedAt: run.CompletedAt,
	}
	if run.Error != "" {
		response.Error = &run.Error
	}
	return response
}
