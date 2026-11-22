package integrations

import (
	"context"
	"errors"
	"fmt"
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
	RevokeAccess(ctx context.Context, token, folderID, email string) error
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
			token, err = googleTokenForGroup(ctx, s.Tokens, groupID)
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

// ExistingFolders reads the registered folders without creating missing ones.
func (s *DriveService) ExistingFolders(ctx context.Context, groupID uuid.UUID, groupName string) (DriveFolders, bool, error) {
	root, found, err := s.Store.GetResource(ctx, groupID, driveProvider, "folder", "root")
	if err != nil || !found || root.Status != ResourceStatusActive || root.ExternalID == "" {
		return DriveFolders{}, false, err
	}
	folders := DriveFolders{Root: DriveFolder{ID: root.ExternalID, Name: groupName}}
	for _, name := range driveSubfolders {
		resource, found, err := s.Store.GetResource(ctx, groupID, driveProvider, "folder", "root/"+name)
		if err != nil {
			return DriveFolders{}, false, err
		}
		if found && resource.Status == ResourceStatusActive && resource.ExternalID != "" {
			folders.Subfolders = append(folders.Subfolders, DriveFolder{ID: resource.ExternalID, Name: name})
		}
	}
	return folders, true, nil
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
	token, err := googleTokenForGroup(ctx, s.Tokens, groupID)
	if err != nil {
		return err
	}

	var firstFailure error
	var failures int
	grant := func(folderID, email, role string) {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		err := s.Gateway.GrantAccess(ctx, token, folderID, email, role)
		_ = s.record(ctx, groupID, "drive.grant_access", started, err)
		if err != nil {
			failures++
			if firstFailure == nil {
				firstFailure = err
			}
		}
	}

	seen := make(map[string]bool, len(rules.StaffEmails)+len(rules.StudentEmails))
	// Staff get editor access to the root and every subfolder.
	for _, raw := range rules.StaffEmails {
		email := NormalizeEmail(raw)
		if email == "" || seen[email] {
			continue
		}
		seen[email] = true
		grant(folders.Root.ID, email, "writer")
		for _, sub := range folders.Subfolders {
			grant(sub.ID, email, "writer")
		}
	}

	// Students get reader access on shared subfolders. Sharing the root
	// would let its permissions propagate to the private Student Data folder.
	for _, raw := range rules.StudentEmails {
		email := NormalizeEmail(raw)
		if email == "" || seen[email] {
			continue
		}
		seen[email] = true
		for _, sub := range folders.Subfolders {
			if driveStudentAccessible[sub.Name] {
				grant(sub.ID, email, "reader")
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if firstFailure != nil {
		return fmt.Errorf("%d Drive permission grants failed (first error: %w)", failures, firstFailure)
	}
	return nil
}

// RevokeAccess removes direct permissions for a former group member from the
// registered folder tree. Inherited grants are removed at their parent folder.
func (s *DriveService) RevokeAccess(ctx context.Context, groupID uuid.UUID, folders DriveFolders, email string) error {
	token, err := googleTokenForGroup(ctx, s.Tokens, groupID)
	if err != nil {
		return err
	}
	all := append([]DriveFolder{folders.Root}, folders.Subfolders...)
	var failures []error
	for _, folder := range all {
		started := time.Now()
		err := s.Gateway.RevokeAccess(ctx, token, folder.ID, NormalizeEmail(email))
		_ = s.record(ctx, groupID, "drive.revoke_access", started, err)
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
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
