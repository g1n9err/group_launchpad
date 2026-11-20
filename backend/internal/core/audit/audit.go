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
	StartDate time.Time
	Resources []Resource
	// Members are the per-group staff assignments (PRD §4.1) the Slack
	// membership audit compares against (PRD §4.7).
	Members []ExpectedMember
	// Roster summarizes student tracking for the audit.
	Roster RosterSummary
}

// ExpectedMember is one staff/lecturer/TA expected in the group's channels.
type ExpectedMember struct {
	Role  string // teaching_staff | lecturer | ta
	Email string // normalized; kept in memory only — never persisted in audit rows
}

// RosterSummary counts the student roster's tracking state (PRD §4.6).
type RosterSummary struct {
	Expected int // roster students
	Joined   int // joined_slack or complete
}

type Store interface {
	AuditInventory(context.Context, uuid.UUID) (Inventory, error)
	SaveAudit(context.Context, int64, Snapshot, []Resource, bool) (uuid.UUID, error)
}

type Checker interface {
	CheckResource(context.Context, Resource) string
}

// MemberChecker verifies expected members against live channel membership
// (PRD §4.7: "Slack staff: required staff are members", "Slack lecturers:
// assigned lecturers are members"). Implemented by the Slack gateway; nil
// disables the check (results report "unavailable" — never a false "ok").
type MemberChecker interface {
	CheckMembers(ctx context.Context, channels []Resource, expected []ExpectedMember) []Result
}

// Membership result statuses (reuse the existing audit status vocabulary).
const (
	StatusOK                    = "ok"
	StatusMissingOrInaccessible = "missing_or_inaccessible"
	StatusUnavailable           = "unavailable"
	StatusUnlinked              = "unlinked"
)

type Service struct {
	Store   Store
	Slack   Checker
	Google  Checker
	Members MemberChecker // optional — nil reports "unavailable"
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

	snapshot := Snapshot{GroupID: groupID, Type: trigger, Results: make([]Result, 0, len(expected)+4), Issues: []Result{}}
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

	// ── Membership audit (PRD §4.7) ─────────────────────────────────────
	// Staff/lecturers/TAs must be channel members; the student roster must
	// exist and be tracked. Aggregate results only — member emails never
	// enter audit rows or notifications (PII-safe by construction).
	memberResults := s.auditMembership(ctx, inventory)
	snapshot.Results = append(snapshot.Results, memberResults...)
	for _, result := range memberResults {
		if result.Status != "ok" {
			snapshot.Issues = append(snapshot.Issues, result)
		}
	}

	return s.Store.SaveAudit(ctx, jobID, snapshot, expected, emailEnabled)
}

// auditMembership produces one result per expected member role plus the
// student-roster tracking row.
func (s *Service) auditMembership(ctx context.Context, inventory Inventory) []Result {
	var results []Result
	if len(inventory.Members) > 0 {
		channels := make([]Resource, 0, 4)
		for _, r := range inventory.Resources {
			if r.Provider == "slack" && r.Kind == "channel" && r.ID != "" {
				channels = append(channels, r)
			}
		}
		byRole := map[string][]Result{}
		if s.Members != nil && len(channels) > 0 {
			checkCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			byRole = groupByRole(s.Members.CheckMembers(checkCtx, channels, inventory.Members))
			cancel()
		}
		roles := map[string]bool{}
		for _, m := range inventory.Members {
			roles[m.Role] = true
		}
		for role := range roles {
			status := StatusUnavailable
			if len(channels) == 0 {
				status = StatusUnlinked // channels not provisioned yet
			} else if s.Members != nil {
				if roleResults, ok := byRole[role]; ok && len(roleResults) > 0 {
					status = roleResults[0].Status
				}
			}
			results = append(results, Result{
				Resource: Resource{Provider: "slack", Kind: "membership", Key: role},
				Status:   status,
			})
		}
	}
	// Student roster tracking: PRD §4.1 requires a student list; an empty
	// roster is a configuration gap the audit must surface.
	rosterStatus := StatusOK
	if inventory.Roster.Expected == 0 {
		rosterStatus = StatusUnlinked
	}
	results = append(results, Result{
		Resource: Resource{Provider: "slack", Kind: "roster", Key: "students"},
		Status:   rosterStatus,
	})
	return results
}

func groupByRole(results []Result) map[string][]Result {
	out := make(map[string][]Result, len(results))
	for _, r := range results {
		out[r.Key] = append(out[r.Key], r)
	}
	return out
}
