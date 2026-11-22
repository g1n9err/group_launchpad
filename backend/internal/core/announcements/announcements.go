package announcements

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

type Schedule struct {
	RequestID   uuid.UUID
	ChannelName string
	Type        string
	Message     string
	ScheduledAt time.Time
}

func (a Schedule) Validate() (Schedule, error) {
	a.ChannelName = strings.TrimSpace(a.ChannelName)
	a.Message = strings.TrimSpace(a.Message)
	a.ScheduledAt = a.ScheduledAt.UTC().Truncate(time.Microsecond)
	switch {
	case a.RequestID == uuid.Nil:
		return a, core.Validation("requestId must be a non-zero UUID", nil)
	case a.ChannelName == "" || len(a.ChannelName) > 80:
		return a, core.Validation("a valid Slack channel name is required", nil)
	case a.Message == "" || utf8.RuneCountInString(a.Message) > 4000:
		return a, core.Validation("message must contain 1 to 4000 characters", nil)
	case a.ScheduledAt.IsZero():
		return a, core.Validation("scheduledAt is required", nil)
	}
	switch a.Type {
	case "welcome", "staff_intro", "rules", "reminder", "general":
		return a, nil
	default:
		return a, core.Validation("invalid announcement type", nil)
	}
}

type Announcement struct {
	Schedule
	ID                uuid.UUID
	GroupID           uuid.UUID
	ChannelID         string
	ChannelIDs        []string
	Status            string
	PublishedAt       *time.Time
	ExternalMessageID string
	Error             string
}
