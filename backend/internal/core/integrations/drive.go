package integrations

import (
	"context"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

const driveProvider = "google_drive"

var driveSubfolders = []string{"General", "Lectures", "Assignments", "Resources", "Student Data"}

type DriveGateway interface {
	GenerateFolderID(ctx context.Context, token string) (string, error)
	EnsureFolder(ctx context.Context, token, id, name, parentID string) error
	// GrantAccess gives a user the specified role on a folder via the Drive
	// Permissions API. role: "writer" (editor) or "reader" (viewer).
	// Sending to an already-permitted user returns success (idempotent).
	GrantAccess(ctx context.Context, token, folderID, email, role string) error
}

type DriveFolderStore interface {
	GetResource(context.Context, uuid.UUID, string, string, string) (GroupResource, bool, error)
	ReserveDriveFolder(context.Context, uuid.UUID, GroupResource) (GroupResource, error)
	UpdateResourceResult(context.Context, uuid.UUID, GroupResource) error
}

type DriveFolder struct {
	ID   string
	Name string
}

type DriveFolders struct {
	Root       DriveFolder
	Subfolders []DriveFolder
}

type DriveService struct {
	Tokens     GoogleAccessTokenSource
	Gateway    DriveGateway
	Store      DriveFolderStore
	Operations ExternalOperationRecorder
}

func (s *DriveService) Provision(ctx context.Context, groupID uuid.UUID, groupName string) (DriveFolders, error) {
	result := DriveFolders{Subfolders: make([]DriveFolder, 0, len(driveSubfolders))}
	var token string
	ensure := func(key, name, parentID string) (DriveFolder, error) {
		resource, found, err := s.Store.GetResource(ctx, groupID, driveProvider, "folder", key)
		if err != nil {
			return DriveFolder{}, err
		}
		if found && resource.Status == ResourceStatusActive {
			return DriveFolder{ID: resource.ExternalID, Name: name}, nil
		}
		if token == "" {
			token, err = s.Tokens.AccessToken(ctx)
			if err != nil {
				return DriveFolder{}, err
			}
		}
		if !found {
			started := time.Now()
			id, err := s.Gateway.GenerateFolderID(ctx, token)
			if err := s.record(ctx, groupID, "drive.generate_folder_id", started, err); err != nil {
				return DriveFolder{}, err
			}
			resource, err = s.Store.ReserveDriveFolder(ctx, groupID, GroupResource{
				Provider: driveProvider, Type: "folder", Name: key, ExternalID: id, Status: ResourceStatusPending,
			})
			if err != nil {
				return DriveFolder{}, err
			}
		}
		if resource.Status != ResourceStatusActive {
			started := time.Now()
			err := s.Gateway.EnsureFolder(ctx, token, resource.ExternalID, name, parentID)
			if err := s.record(ctx, groupID, "drive.ensure_folder", started, err); err != nil {
				return DriveFolder{}, err
			}
			resource.Status = ResourceStatusActive
			if err := s.Store.UpdateResourceResult(ctx, groupID, resource); err != nil {
				return DriveFolder{}, err
			}
		}
		return DriveFolder{ID: resource.ExternalID, Name: name}, nil
	}

	root, err := ensure("root", groupName, "")
	if err != nil {
		return DriveFolders{}, err
	}
	result.Root = root
	for _, name := range driveSubfolders {
		folder, err := ensure("root/"+name, name, root.ID)
		if err != nil {
			return DriveFolders{}, err
		}
		result.Subfolders = append(result.Subfolders, folder)
	}
	return result, nil
}

// DriveAccessRules defines who gets what permission on which folders.
// Per PRD §4.4: "Configure appropriate access permissions" and "Restricted
// data is not accessible to unauthorized users."
//
// Student Data is the sensitive folder — students NEVER get access.
// Lectures/Assignments are curriculum material — students read-only.
// General/Resources are shared — students read-only.
// Staff (coordinators, lecturers, TAs) get editor on everything.
type DriveAccessRules struct {
	// StaffEmails are users who get editor access to ALL folders (including
	// Student Data).
	StaffEmails []string

	// StudentEmails are users who get reader access to General/Resources/
	// Lectures/Assignments — but NEVER Student Data.
	StudentEmails []string
}

// driveFolderPermissions maps folder names to which student role can access them.
// "Student Data" is deliberately absent: students never appear there.
var driveStudentAccessible = map[string]bool{
	"General":     true,
	"Resources":   true,
	"Lectures":    true,
	"Assignments": true,
	// "Student Data": false — intentionally omitted
}

// ApplyAccessRules grants Drive permissions per the rules after folder
// creation. Safe to re-run: GrantAccess is idempotent on the Drive API.
func (s *DriveService) ApplyAccessRules(ctx context.Context, groupID uuid.UUID, folders DriveFolders, rules DriveAccessRules) error {
	token, err := s.Tokens.AccessToken(ctx)
	if err != nil {
		return err
	}

	// Staff: editor on root + ALL subfolders (including Student Data).
	for _, email := range rules.StaffEmails {
		started := time.Now()
		err := s.Gateway.GrantAccess(ctx, token, folders.Root.ID, email, "writer")
		s.record(ctx, groupID, "drive.grant_access", started, err)
		if err != nil {
			return err
		}
		for _, sub := range folders.Subfolders {
			started := time.Now()
			err := s.Gateway.GrantAccess(ctx, token, sub.ID, email, "writer")
			s.record(ctx, groupID, "drive.grant_access", started, err)
			if err != nil {
				return err
			}
		}
	}

	// Students: reader on accessible folders ONLY — never Student Data.
	for _, email := range rules.StudentEmails {
		for _, sub := range folders.Subfolders {
			if !driveStudentAccessible[sub.Name] {
				continue // Student Data: skip
			}
			started := time.Now()
			err := s.Gateway.GrantAccess(ctx, token, sub.ID, email, "reader")
			s.record(ctx, groupID, "drive.grant_access", started, err)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *DriveService) record(ctx context.Context, groupID uuid.UUID, operation string, started time.Time, err error) error {
	op := ExternalOperation{GroupID: &groupID, Provider: driveProvider, Operation: operation, DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		op.Error = err.Error()
	}
	_ = s.Operations.RecordExternalOperation(ctx, op)
	if err != nil {
		return &core.AppError{Code: core.CodeUpstream, Message: err.Error()}
	}
	return nil
}
