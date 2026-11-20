package transport

import (
	"context"
	"encoding/json"
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

// GroupLauncher enqueues the unified multi-platform launch job (Slack +
// Classroom + Drive + Forms).
type GroupLauncher interface {
	QueueGroupLaunch(ctx context.Context, groupID uuid.UUID) error
}

// LaunchGroup POST /api/groups/{groupId}/launch — queues the unified
// multi-platform launch: Slack channels, Classroom course + enrollment,
// Drive folders + permissions, Forms intake. Each step is idempotent.
func (h *Handlers) LaunchGroup(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, uuid.UUID(groupID)); err != nil {
		return err
	}

	ctx := c.Request().Context()
	group, err := h.groups.Get(ctx, groupID.String())
	if err != nil {
		return err
	}
	if _, err := integrations.ChannelName(group.Name, integrations.DefaultChannelSuffixes[0]); err != nil {
		return err
	}

	// Try the unified launcher first (multi-platform). Fall back to
	// Slack-only launch if the unified launcher isn't wired (e.g. in tests
	// or when River isn't running).
	if h.groupLauncher != nil {
		if err := h.groupLauncher.QueueGroupLaunch(ctx, group.ID); err != nil {
			return err
		}
		message := "Unified launch queued: Slack + Classroom + Drive + Forms"
		return c.JSON(http.StatusAccepted, gen.LaunchQueued{Status: "queued", Message: &message})
	}

	// Fallback: Slack-only (original behavior, preserves existing tests).
	if h.launch == nil || !h.slackChannels.Configured() {
		return errNotReady("group launch is unavailable")
	}
	if _, err := h.launch.QueueLaunch(ctx, group.ID); err != nil {
		return err
	}
	msg := "Slack launch queued (unified launcher not available)"
	return c.JSON(http.StatusAccepted, gen.LaunchQueued{Status: "queued", Message: &msg})
}

// LaunchStatusReader returns the persisted unified launch result.
type LaunchStatusReader interface {
	GetLaunchResult(ctx context.Context, groupID uuid.UUID) (status string, steps json.RawMessage, found bool, err error)
}

func (h *Handlers) GetGroupLaunchStatus(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupRead(c, uuid.UUID(groupID)); err != nil {
		return err
	}
	ctx := c.Request().Context()

	// Unified launch result first.
	if h.launchStatus != nil {
		status, stepsJSON, found, err := h.launchStatus.GetLaunchResult(ctx, uuid.UUID(groupID))
		if err != nil {
			return err
		}
		if found {
			return c.JSON(http.StatusOK, map[string]any{
				"groupId": groupID,
				"status":  status,
				"steps":   json.RawMessage(stepsJSON),
			})
		}
	}

	// Fall back to Slack-only status (legacy).
	if h.launch == nil {
		return errNotReady("launch status is unavailable")
	}
	run, err := h.launch.Status(ctx, uuid.UUID(groupID))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"groupId": run.GroupID, "status": run.Status,
		"attempts": run.Attempts, "error": run.Error,
		"completedAt": run.CompletedAt,
	})
}
