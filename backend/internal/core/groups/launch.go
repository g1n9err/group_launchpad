package groups

import (
	"time"

	"github.com/google/uuid"
)

// SlackLaunch tracks the Slack stage of a group's launch.
type SlackLaunch struct {
	ID          uuid.UUID
	GroupID     uuid.UUID
	Status      string
	Attempts    int
	Error       string
	CreatedAt   time.Time
	CompletedAt *time.Time
}
