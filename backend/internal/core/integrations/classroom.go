package integrations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

const (
	classroomProvider   = "google_classroom"
	classroomCourseType = "course"
)

type ClassroomCourseInput struct {
	Alias, Name, Section, Description string
}

type ClassroomGateway interface {
	EnsureCourse(context.Context, string, ClassroomCourseInput) (string, error)
	// ActivateCourse transitions a PROVISIONED course to ACTIVE (idempotent:
	// already-ACTIVE is a no-op). Students can only be invited to ACTIVE
	// courses, so provisioning must activate before enrolling.
	ActivateCourse(ctx context.Context, token, courseID string) error
	// EnrollStudent invites a student to a course by email; already-enrolled
	// returns success (idempotent).
	EnrollStudent(ctx context.Context, token, courseID, email string) error
	// AssignTeacher adds a teacher to a course by email; already-assigned
	// returns success (idempotent).
	AssignTeacher(ctx context.Context, token, courseID, email string) error
	RemoveStudent(ctx context.Context, token, courseID, email string) error
	RemoveTeacher(ctx context.Context, token, courseID, email string) error
}

type GoogleAccessTokenSource interface {
	AccessToken(context.Context) (string, error)
}

type groupGoogleAccessTokenSource interface {
	AccessTokenForGroup(context.Context, uuid.UUID) (string, error)
}

func googleTokenForGroup(ctx context.Context, source GoogleAccessTokenSource, groupID uuid.UUID) (string, error) {
	if scoped, ok := source.(groupGoogleAccessTokenSource); ok {
		return scoped.AccessTokenForGroup(ctx, groupID)
	}
	return source.AccessToken(ctx)
}

type ClassroomCourseStore interface {
	GetResource(context.Context, uuid.UUID, string, string, string) (GroupResource, bool, error)
	SaveClassroomCourse(context.Context, uuid.UUID, GroupResource) error
}

type ClassroomService struct {
	Tokens          GoogleAccessTokenSource
	Gateway         ClassroomGateway
	Store           ClassroomCourseStore
	Operations      ExternalOperationRecorder
	QueueJoinEmails func(context.Context, uuid.UUID, string, ...string) error
}

// ExistingCourseID reads the registered course without provisioning one.
func (s *ClassroomService) ExistingCourseID(ctx context.Context, groupID uuid.UUID) (string, error) {
	resource, found, err := s.Store.GetResource(ctx, groupID, classroomProvider, classroomCourseType, "p:launchpad-"+groupID.String())
	if err != nil || !found || resource.Status != ResourceStatusActive {
		return "", err
	}
	return resource.ExternalID, nil
}

func (s *ClassroomService) Provision(ctx context.Context, groupID uuid.UUID, name, section string, startDate time.Time) (string, error) {
	alias := "p:launchpad-" + groupID.String()
	var courseID string
	resource, found, err := s.Store.GetResource(ctx, groupID, classroomProvider, classroomCourseType, alias)
	if err != nil {
		return "", err
	}
	if found && resource.Status == ResourceStatusActive && resource.ExternalID != "" {
		courseID = resource.ExternalID
	} else {
		token, err := googleTokenForGroup(ctx, s.Tokens, groupID)
		if err != nil {
			return "", err
		}
		started := time.Now()
		id, err := s.Gateway.EnsureCourse(ctx, token, ClassroomCourseInput{
			Alias: alias, Name: name, Section: section,
			Description: "Group starts " + startDate.Format(time.DateOnly) + ".",
		})
		op := ExternalOperation{GroupID: &groupID, Provider: classroomProvider, Operation: "classroom.ensure_course", DurationMS: time.Since(started).Milliseconds()}
		if err != nil {
			op.Error = err.Error()
		}
		_ = s.Operations.RecordExternalOperation(ctx, op)
		if err != nil {
			return "", &core.AppError{Code: core.CodeUpstream, Message: err.Error()}
		}
		courseID = id
	}

	// A course is only usable when ACTIVE: freshly created ones are
	// PROVISIONED (a draft invisible to students, enrollment rejected), and
	// courses recorded by older versions may never have been activated. The
	// PATCH is idempotent, so it runs on EVERY provision — including the
	// registry fast path — before any roster writes.
	//
	// Best-effort: consumer (non-Workspace for Education) accounts cannot
	// transition courses to ACTIVE — Google answers 403 @CourseStateDenied —
	// and must keep working with a PROVISIONED course (the audit treats it
	// as an existing course per PRD §4.7). The outcome is recorded either
	// way; only transient failures surface as errors.
	token, err := googleTokenForGroup(ctx, s.Tokens, groupID)
	if err != nil {
		return "", err
	}
	started := time.Now()
	activateErr := s.Gateway.ActivateCourse(ctx, token, courseID)
	s.record(ctx, groupID, "classroom.activate_course", started, activateErr)
	if activateErr != nil && !isCourseStateDenied(activateErr) {
		return "", &core.AppError{Code: core.CodeUpstream, Message: activateErr.Error()}
	}

	if !found || resource.ExternalID != courseID || resource.Status != ResourceStatusActive {
		if err := s.Store.SaveClassroomCourse(ctx, groupID, GroupResource{
			Provider: classroomProvider, Type: classroomCourseType, Name: alias, Status: ResourceStatusActive, ExternalID: courseID,
		}); err != nil {
			return "", err
		}
	}
	if s.QueueJoinEmails != nil {
		if err := s.QueueJoinEmails(ctx, groupID, courseID); err != nil {
			return "", fmt.Errorf("queue Classroom join emails: %w", err)
		}
	}
	return courseID, nil
}

// isCourseStateDenied reports Google's permanent refusal to transition a
// course into ACTIVE for this account type (consumer accounts). Retrying
// cannot succeed; provisioning continues with a PROVISIONED course.
func isCourseStateDenied(err error) bool {
	return err != nil && strings.Contains(err.Error(), "@CourseStateDenied")
}

// EnrollAll enrolls every student email into the course. Individual failures
// are recorded but don't abort the batch — the caller receives a summary.
func (s *ClassroomService) EnrollAll(ctx context.Context, groupID uuid.UUID, courseID string, emails []string) []EnrollmentResult {
	token, err := googleTokenForGroup(ctx, s.Tokens, groupID)
	if err != nil {
		return errorResults(emails, "token_unavailable")
	}
	results := make([]EnrollmentResult, 0, len(emails))
	for _, email := range emails {
		started := time.Now()
		err := s.Gateway.EnrollStudent(ctx, token, courseID, email)
		s.record(ctx, groupID, "classroom.enroll_student", started, err)
		results = append(results, EnrollmentResult{Email: email, Err: err})
	}
	return results
}

// AssignTeachers adds teachers to the course. Idempotent per teacher.
func (s *ClassroomService) AssignTeachers(ctx context.Context, groupID uuid.UUID, courseID string, emails []string) []EnrollmentResult {
	token, err := googleTokenForGroup(ctx, s.Tokens, groupID)
	if err != nil {
		return errorResults(emails, "token_unavailable")
	}
	results := make([]EnrollmentResult, 0, len(emails))
	for _, email := range emails {
		started := time.Now()
		err := s.Gateway.AssignTeacher(ctx, token, courseID, email)
		s.record(ctx, groupID, "classroom.assign_teacher", started, err)
		results = append(results, EnrollmentResult{Email: email, Err: err})
	}
	return results
}

func (s *ClassroomService) RemoveMembers(ctx context.Context, groupID uuid.UUID, courseID string, emails []string, staff bool) []EnrollmentResult {
	token, err := googleTokenForGroup(ctx, s.Tokens, groupID)
	if err != nil {
		return errorResults(emails, "token_unavailable")
	}
	results := make([]EnrollmentResult, 0, len(emails))
	for _, email := range emails {
		started := time.Now()
		if staff {
			err = s.Gateway.RemoveTeacher(ctx, token, courseID, email)
		} else {
			err = s.Gateway.RemoveStudent(ctx, token, courseID, email)
		}
		operation := "classroom.remove_student"
		if staff {
			operation = "classroom.remove_teacher"
		}
		s.record(ctx, groupID, operation, started, err)
		results = append(results, EnrollmentResult{Email: email, Err: err})
	}
	return results
}

// EnrollmentResult reports one student/teacher enrollment outcome.
type EnrollmentResult struct {
	Email string
	Err   error // nil = success
}

func errorResults(emails []string, reason string) []EnrollmentResult {
	out := make([]EnrollmentResult, len(emails))
	for i, email := range emails {
		out[i] = EnrollmentResult{Email: email, Err: errors.New(reason)}
	}
	return out
}

func (s *ClassroomService) record(ctx context.Context, groupID uuid.UUID, operation string, started time.Time, err error) {
	op := ExternalOperation{GroupID: &groupID, Provider: classroomProvider, Operation: operation, DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		op.Error = err.Error()
	}
	_ = s.Operations.RecordExternalOperation(ctx, op)
}
