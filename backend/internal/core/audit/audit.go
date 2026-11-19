package audit

import (
	"context"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core/integrations"
)

type Resource struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Key      string `json:"key"`
	ID       string `json:"id,omitempty"`
	ParentID string `json:"parentId,omitempty"`
}

type Result struct {
	Resource
	Status string `json:"status"`
}

type Snapshot struct {
	ID        uuid.UUID
	GroupID   uuid.UUID
	Type      string
	CheckedAt time.Time
	Results   []Result
	Issues    []Result
}

type Inventory struct {
	GroupName string
	Resources []Resource
}

type Store interface {
	AuditInventory(context.Context, uuid.UUID) (Inventory, error)
	SaveAudit(context.Context, int64, Snapshot, []Resource, bool) (uuid.UUID, error)
}

type Checker interface {
	CheckResource(context.Context, Resource) string
}

type Service struct {
	Store  Store
	Slack  Checker
	Google Checker
}

func (s *Service) Run(ctx context.Context, groupID uuid.UUID, jobID int64, trigger string, emailEnabled bool) (uuid.UUID, error) {
	inventory, err := s.Store.AuditInventory(ctx, groupID)
	if err != nil {
		return uuid.Nil, err
	}
	expected := []Resource{
		{Provider: "google_classroom", Kind: "course", Key: "course"},
		{Provider: "google_drive", Kind: "folder", Key: "root"},
		{Provider: "google_drive", Kind: "folder", Key: "root/General"},
		{Provider: "google_drive", Kind: "folder", Key: "root/Lectures"},
		{Provider: "google_drive", Kind: "folder", Key: "root/Assignments"},
		{Provider: "google_drive", Kind: "folder", Key: "root/Resources"},
		{Provider: "google_drive", Kind: "folder", Key: "root/Student Data"},
		{Provider: "google_forms", Kind: "form", Key: "intake"},
	}
	for _, suffix := range integrations.DefaultChannelSuffixes {
		name, err := integrations.ChannelName(inventory.GroupName, suffix)
		if err != nil {
			name = suffix
		}
		expected = append(expected, Resource{Provider: "slack", Kind: "channel", Key: name})
	}

	snapshot := Snapshot{GroupID: groupID, Type: trigger, Results: make([]Result, 0, len(expected)), Issues: []Result{}}
	for i, resource := range expected {
		if err := ctx.Err(); err != nil {
			return uuid.Nil, err
		}
		for _, linked := range inventory.Resources {
			if linked.Provider == resource.Provider && linked.Kind == resource.Kind && linked.Key == resource.Key {
				resource.ID, resource.ParentID = linked.ID, linked.ParentID
				break
			}
		}
		expected[i] = resource
		result := Result{Resource: resource, Status: "unlinked"}
		if resource.ID != "" {
			checker := s.Google
			if resource.Provider == "slack" {
				checker = s.Slack
			}
			result.Status = "unavailable"
			if checker != nil {
				checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				result.Status = checker.CheckResource(checkCtx, resource)
				cancel()
			}
		}
		snapshot.Results = append(snapshot.Results, result)
		if result.Status != "ok" {
			snapshot.Issues = append(snapshot.Issues, result)
		}
	}
	return s.Store.SaveAudit(ctx, jobID, snapshot, expected, emailEnabled)
}
