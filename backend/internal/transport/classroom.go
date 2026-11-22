package transport

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"launchpad/internal/core"
	"launchpad/internal/jobs"
	gen "launchpad/internal/transport/gen"
)

func (h *Handlers) ProvisionGroupClassroom(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, uuid.UUID(groupID)); err != nil {
		return err
	}
	if h.classroom == nil {
		return errNotReady("Google Classroom is unavailable")
	}
	ctx := c.Request().Context()
	group, err := h.groups.Get(ctx, groupID.String())
	if err != nil {
		return err
	}
	id, err := h.classroom.Provision(ctx, group.ID, group.CourseName, group.Name, group.StartDate)
	if err != nil {
		return err
	}

	// Enroll students (from the group's roster) and assign teachers (from
	// the coordinator's staff list). Individual failures are reported but
	// don't abort the provisioning response.
	var studentResults []gen.ClassroomEnrollment
	var studentFailures, teacherFailures int
	roster, err := h.groups.Onboarding(ctx, group.ID.String())
	if err != nil {
		return err
	}
	studentEmails := make([]string, 0, len(roster.Students))
	for _, s := range roster.Students {
		studentEmails = append(studentEmails, s.Email)
	}
	if len(studentEmails) > 0 {
		for _, r := range h.classroom.EnrollAll(ctx, group.ID, id, studentEmails) {
			if r.Err != nil {
				studentFailures++
			}
			studentResults = append(studentResults, gen.ClassroomEnrollment{
				Email: r.Email, Success: r.Err == nil,
			})
		}
	}

	// Use per-group staff assignments (PRD §4.1), falling back to
	// the handler's global staffEmails when none are configured.
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

	var teacherResults []gen.ClassroomEnrollment
	if len(staffEmails) > 0 {
		for _, r := range h.classroom.AssignTeachers(ctx, group.ID, id, staffEmails) {
			if r.Err != nil {
				teacherFailures++
			}
			teacherResults = append(teacherResults, gen.ClassroomEnrollment{
				Email: r.Email, Success: r.Err == nil,
			})
		}
	}

	step := jobs.LaunchStep{Platform: "classroom", Status: jobs.StepSucceeded}
	if studentFailures+teacherFailures > 0 {
		step.Status = jobs.StepPartiallyFailed
		step.Detail = fmt.Sprintf("course exists, %d student and %d teacher access failures", studentFailures, teacherFailures)
	}
	if err := h.saveLaunchStep(ctx, group.ID, step); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, gen.ClassroomCourse{
		CourseId:           id,
		StudentEnrollments: &studentResults,
		TeacherAssignments: &teacherResults,
	})
}
