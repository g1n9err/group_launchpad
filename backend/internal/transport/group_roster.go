package transport

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"launchpad/internal/core"
	"launchpad/internal/core/groups"
	"launchpad/internal/core/integrations"
	gen "launchpad/internal/transport/gen"
)

type GroupRosterWriter interface {
	UpdateGroupRoster(context.Context, uuid.UUID, groups.RosterUpdate) error
}

func (h *Handlers) UpdateGroupRoster(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, groupID); err != nil {
		return err
	}
	if h.groupRosterWriter == nil {
		return errNotReady("group roster updates are unavailable")
	}
	var body gen.GroupRosterUpdate
	if err := c.Bind(&body); err != nil {
		return errBadRequest("invalid group roster request")
	}
	update, err := groups.ValidateRosterUpdate(body.StudentEmails, body.LecturerEmails, body.TaEmails)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	group, err := h.groups.Get(ctx, groupID.String())
	if err != nil {
		return err
	}

	oldReport, err := h.groups.Onboarding(ctx, groupID.String())
	if err != nil {
		return err
	}
	oldStudents := make([]string, 0, len(oldReport.Students))
	for _, student := range oldReport.Students {
		oldStudents = append(oldStudents, student.Email)
	}
	oldStaff := []gen.GroupOnboardingStaff{}
	if h.groupStaffReader != nil {
		oldStaff, err = h.groupStaffReader.GroupStaff(ctx, groupID)
		if err != nil {
			return err
		}
	}
	lecturers, tas := currentStaffLists(oldStaff)
	desiredStudents := oldStudents
	if update.Students != nil {
		desiredStudents = update.Students
	}
	if update.Lecturers != nil {
		lecturers = update.Lecturers
	}
	if update.TAs != nil {
		tas = update.TAs
	}
	desiredStaff := slices.Concat(lecturers, tas)
	if len(intersectionEmails(desiredStudents, desiredStaff)) > 0 {
		return &core.AppError{Code: core.CodeConflict, Message: "a person cannot be both a student and teaching staff in the same group"}
	}
	if len(intersectionEmails(lecturers, tas)) > 0 {
		return &core.AppError{Code: core.CodeConflict, Message: "a person cannot have both teaching staff roles in the same group"}
	}
	oldAll := slices.Concat(oldStudents, staffEmails(oldStaff))
	desiredAll := slices.Concat(desiredStudents, desiredStaff)
	removed := differenceEmails(oldAll, desiredAll)
	removedStudents := intersectionEmails(removed, oldStudents)
	removedStaff := intersectionEmails(removed, staffEmails(oldStaff))

	// Revoke external group access first. If any linked provider rejects a
	// revocation, leave the saved roster unchanged so the same request can retry.
	removal := gen.GroupRosterUpdateResult{}
	if len(removed) > 0 {
		driveStatus, classroomStatus, slackStatus, revokeErr := h.revokeGroupAccess(ctx, group, removed, removedStudents, removedStaff)
		removal.DriveRemoval, removal.ClassroomRemoval, removal.SlackRemoval = &driveStatus, &classroomStatus, &slackStatus
		if revokeErr != nil {
			return errNotReady("could not revoke all group access; roster was not changed")
		}
	}
	if err := h.groupRosterWriter.UpdateGroupRoster(ctx, groupID, update); err != nil {
		return err
	}

	rules := integrations.DriveAccessRules{StaffEmails: slices.Clone(desiredStaff)}
	for _, email := range desiredStudents {
		if !slices.Contains(desiredStaff, email) {
			rules.StudentEmails = append(rules.StudentEmails, email)
		}
	}
	emails := slices.Concat(rules.StudentEmails, rules.StaffEmails)
	drive := h.updateRosterDrive(ctx, group, rules)
	classroom, mail := h.updateRosterClassroom(ctx, groupID, rules, emails)
	result := gen.GroupRosterUpdateResult{
		DriveAccess: &drive, ClassroomAccess: &classroom, ClassroomEmail: &mail,
		DriveRemoval: removal.DriveRemoval, ClassroomRemoval: removal.ClassroomRemoval, SlackRemoval: removal.SlackRemoval,
	}
	message := "Slack invitations are unavailable"
	if h.slackChannels == nil {
		result.SlackError = &message
	} else if slack, err := h.slackChannels.InviteExisting(ctx, groupID, group.Name, emails); err != nil {
		result.SlackError = &message
	} else {
		outcome := toProvisionResult(slack)
		result.Slack = &outcome
	}
	return c.JSON(http.StatusOK, result)
}

func (h *Handlers) revokeGroupAccess(ctx context.Context, group groups.Group, removed, students, staff []string) (gen.RosterAccessStatus, gen.RosterAccessStatus, gen.RosterAccessStatus, error) {
	groupID := group.ID
	driveStatus, classroomStatus, slackStatus := gen.RosterAccessStatusReady, gen.RosterAccessStatusReady, gen.RosterAccessStatusReady
	var failures []error
	if h.drive == nil {
		driveStatus = gen.RosterAccessStatusUnavailable
		failures = append(failures, errors.New("Drive unavailable"))
	} else if folders, found, err := h.drive.ExistingFolders(ctx, group.ID, group.Name); err != nil {
		driveStatus = gen.RosterAccessStatusUnavailable
		failures = append(failures, err)
	} else if !found {
		driveStatus = gen.RosterAccessStatusNotProvisioned
	} else {
		for _, email := range removed {
			if err := h.drive.RevokeAccess(ctx, group.ID, folders, email); err != nil {
				driveStatus = gen.RosterAccessStatusPartial
				failures = append(failures, err)
			}
		}
	}
	if h.classroom == nil {
		classroomStatus = gen.RosterAccessStatusUnavailable
		failures = append(failures, errors.New("Classroom unavailable"))
	} else if courseID, err := h.classroom.ExistingCourseID(ctx, groupID); err != nil {
		classroomStatus = gen.RosterAccessStatusUnavailable
		failures = append(failures, err)
	} else if courseID == "" {
		classroomStatus = gen.RosterAccessStatusNotProvisioned
	} else {
		for _, group := range []struct {
			emails []string
			staff  bool
		}{{students, false}, {staff, true}} {
			if len(group.emails) == 0 {
				continue
			}
			for _, result := range h.classroom.RemoveMembers(ctx, groupID, courseID, group.emails, group.staff) {
				if result.Err != nil {
					classroomStatus = gen.RosterAccessStatusPartial
					failures = append(failures, result.Err)
				}
			}
		}
	}
	if h.slackChannels == nil {
		slackStatus = gen.RosterAccessStatusUnavailable
		failures = append(failures, errors.New("Slack unavailable"))
	} else if !h.slackChannels.Configured() {
		slackStatus = gen.RosterAccessStatusUnavailable
		failures = append(failures, errors.New("Slack unavailable"))
	} else if err := h.slackChannels.RemoveExisting(ctx, group.ID, group.Name, removed); err != nil {
		slackStatus = gen.RosterAccessStatusPartial
		failures = append(failures, err)
	}
	return driveStatus, classroomStatus, slackStatus, errors.Join(failures...)
}

func currentStaffLists(staff []gen.GroupOnboardingStaff) (lecturers, tas []string) {
	seenLecturers, seenTAs := map[string]bool{}, map[string]bool{}
	for _, member := range staff {
		email := integrations.NormalizeEmail(string(member.Email))
		switch member.Role {
		case "lecturer":
			if !seenLecturers[email] {
				lecturers = append(lecturers, email)
				seenLecturers[email] = true
			}
		case "ta", "teaching_staff":
			if !seenTAs[email] {
				tas = append(tas, email)
				seenTAs[email] = true
			}
		}
	}
	return
}
func staffEmails(staff []gen.GroupOnboardingStaff) []string {
	lecturers, tas := currentStaffLists(staff)
	return slices.Concat(lecturers, tas)
}
func differenceEmails(values, keep []string) []string {
	var result []string
	for _, value := range values {
		if !slices.ContainsFunc(keep, func(candidate string) bool {
			return integrations.NormalizeEmail(candidate) == integrations.NormalizeEmail(value)
		}) && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}
func intersectionEmails(values, candidates []string) []string {
	var result []string
	for _, value := range values {
		if slices.ContainsFunc(candidates, func(candidate string) bool {
			return integrations.NormalizeEmail(candidate) == integrations.NormalizeEmail(value)
		}) {
			result = append(result, value)
		}
	}
	return result
}

func (h *Handlers) updateRosterDrive(ctx context.Context, group groups.Group, rules integrations.DriveAccessRules) gen.RosterAccessStatus {
	if h.drive == nil {
		return gen.RosterAccessStatusUnavailable
	}
	folders, found, err := h.drive.ExistingFolders(ctx, group.ID, group.Name)
	if err != nil {
		return gen.RosterAccessStatusUnavailable
	}
	if !found {
		return gen.RosterAccessStatusNotProvisioned
	}
	if err := h.drive.ApplyAccessRules(ctx, group.ID, folders, rules); err != nil {
		return gen.RosterAccessStatusPartial
	}
	return gen.RosterAccessStatusReady
}

func (h *Handlers) updateRosterClassroom(ctx context.Context, groupID uuid.UUID, rules integrations.DriveAccessRules, emails []string) (access, mail gen.RosterAccessStatus) {
	if h.classroom == nil {
		return gen.RosterAccessStatusUnavailable, gen.RosterAccessStatusUnavailable
	}
	courseID, err := h.classroom.ExistingCourseID(ctx, groupID)
	if err != nil {
		return gen.RosterAccessStatusUnavailable, gen.RosterAccessStatusUnavailable
	}
	if courseID == "" {
		return gen.RosterAccessStatusNotProvisioned, gen.RosterAccessStatusNotProvisioned
	}
	access = gen.RosterAccessStatusReady
	apply := func(emails []string, add func(context.Context, uuid.UUID, string, []string) []integrations.EnrollmentResult) {
		for _, result := range add(ctx, groupID, courseID, emails) {
			if result.Err != nil {
				access = gen.RosterAccessStatusPartial
			}
		}
	}
	apply(rules.StudentEmails, h.classroom.EnrollAll)
	apply(rules.StaffEmails, h.classroom.AssignTeachers)
	mail = gen.RosterAccessStatusUnavailable
	if h.classroom.QueueJoinEmails != nil && len(emails) != 0 {
		if err := h.classroom.QueueJoinEmails(ctx, groupID, courseID, emails...); err == nil {
			mail = gen.RosterAccessStatusReady
		}
	}
	return
}
