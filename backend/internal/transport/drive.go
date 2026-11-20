package transport

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"launchpad/internal/core"
	"launchpad/internal/core/integrations"
	gen "launchpad/internal/transport/gen"
)

func (h *Handlers) ProvisionGroupDrive(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, uuid.UUID(groupID)); err != nil {
		return err
	}
	if h.drive == nil {
		return errNotReady("Google Drive is unavailable")
	}

	ctx := c.Request().Context()
	group, err := h.groups.Get(ctx, groupID.String())
	if err != nil {
		return err
	}
	folders, err := h.drive.Provision(ctx, group.ID, group.Name)
	if err != nil {
		return err
	}

	// Apply access rules: staff editor everywhere, students reader on
	// non-sensitive folders (never Student Data). Uses the group's roster
	// to resolve student emails; staff emails come from the coordinator's
	// configured list (Phase 2 feature — empty until staff management lands).
	roster, _ := h.groups.Onboarding(ctx, group.ID.String())
	studentEmails := make([]string, 0, len(roster.Students))
	for _, s := range roster.Students {
		studentEmails = append(studentEmails, s.Email)
	}
	// Use per-group staff assignments (PRD §4.1), falling back to global.
	staffEmails := h.staffEmails
	if h.staffAssignmentReader != nil {
		perGroup, err := h.staffAssignmentReader.StaffEmailsFor(ctx, group.ID)
		if err != nil {
			return &core.AppError{Code: core.CodeUpstream, Message: "failed to read staff assignments"}
		}
		if len(perGroup) > 0 {
			staffEmails = perGroup
		}
	}
	if err := h.drive.ApplyAccessRules(ctx, group.ID, folders, integrations.DriveAccessRules{
		StaffEmails:   staffEmails,
		StudentEmails: studentEmails,
	}); err != nil {
		// Permissions failure doesn't fail provisioning — folders exist.
		// The coordinator can retry permissions separately.
		c.Logger().Warn("drive permissions partial", "error", err.Error())
	}

	result := gen.DriveFolders{
		Root:       gen.DriveFolder{Id: folders.Root.ID, Name: folders.Root.Name},
		Subfolders: make([]gen.DriveFolder, 0, len(folders.Subfolders)),
	}
	for _, folder := range folders.Subfolders {
		result.Subfolders = append(result.Subfolders, gen.DriveFolder{Id: folder.ID, Name: folder.Name})
	}
	return c.JSON(http.StatusOK, result)
}
