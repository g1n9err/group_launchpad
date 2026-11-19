package audit

import (
	"time"

	"github.com/google/uuid"
)

type Change struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Key      string `json:"key"`
	Before   string `json:"before"`
	After    string `json:"after"`
}

type Notification struct {
	ID          uuid.UUID
	GroupID     uuid.UUID
	GroupName   string
	CheckedAt   time.Time
	Changes     []Change
	EmailStatus string
	EmailError  string
}

type OpenIssues struct {
	GroupID   uuid.UUID
	GroupName string
	CheckedAt time.Time
	Issues    []Result
}

func Changes(previous, current []Result) []Change {
	type resourceKey struct{ provider, kind, key string }
	prior := make(map[resourceKey]string, len(previous))
	for _, item := range previous {
		prior[resourceKey{item.Provider, item.Kind, item.Key}] = item.Status
	}
	changes := make([]Change, 0)
	for _, item := range current {
		before := prior[resourceKey{item.Provider, item.Kind, item.Key}]
		if item.Status == before || (before == "" && item.Status == "ok") {
			continue
		}
		changes = append(changes, Change{Provider: item.Provider, Kind: item.Kind, Key: item.Key, Before: before, After: item.Status})
	}
	return changes
}
