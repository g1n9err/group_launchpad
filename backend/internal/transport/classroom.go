package transport

import (
	"net/http"

	"github.com/labstack/echo/v4"

	gen "launchpad/internal/transport/gen"
)

func (h *Handlers) ProvisionGroupClassroom(c echo.Context, groupID gen.GroupId) error {
	if err := h.coordinator.authorize(c); err != nil {
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
	roster, _ := h.groups.Onboarding(ctx, group.ID.String())
	studentEmails := make([]string, 0, len(roster.Students))
	for _, s := range roster.Students {
		studentEmails = append(studentEmails, s.Email)
	}
	if len(studentEmails) > 0 {
		for _, r := range h.classroom.EnrollAll(ctx, group.ID, id, studentEmails) {
			studentResults = append(studentResults, gen.ClassroomEnrollment{
				Email: r.Email, Success: r.Err == nil,
			})
		}
	}

	var teacherResults []gen.ClassroomEnrollment
	if len(h.staffEmails) > 0 {
		for _, r := range h.classroom.AssignTeachers(ctx, group.ID, id, h.staffEmails) {
			teacherResults = append(teacherResults, gen.ClassroomEnrollment{
				Email: r.Email, Success: r.Err == nil,
			})
		}
	}

	return c.JSON(http.StatusOK, gen.ClassroomCourse{
		CourseId:          id,
		StudentEnrollments: &studentResults,
		TeacherAssignments: &teacherResults,
	})
}
