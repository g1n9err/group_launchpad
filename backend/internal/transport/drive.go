package transport

import (
	"net/http"

	"github.com/labstack/echo/v4"

	gen "launchpad/internal/transport/gen"
)

func (h *Handlers) ProvisionGroupDrive(c echo.Context, groupID gen.GroupId) error {
	if err := h.coordinator.authorize(c); err != nil {
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
	result := gen.DriveFolders{
		Root:       gen.DriveFolder{Id: folders.Root.ID, Name: folders.Root.Name},
		Subfolders: make([]gen.DriveFolder, 0, len(folders.Subfolders)),
	}
	for _, folder := range folders.Subfolders {
		result.Subfolders = append(result.Subfolders, gen.DriveFolder{Id: folder.ID, Name: folder.Name})
	}
	return c.JSON(http.StatusOK, result)
}
