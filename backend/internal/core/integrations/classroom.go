package integrations

import (
	"context"
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
}

type GoogleAccessTokenSource interface {
	AccessToken(context.Context) (string, error)
}

type ClassroomCourseStore interface {
	GetResource(context.Context, uuid.UUID, string, string, string) (GroupResource, bool, error)
	SaveClassroomCourse(context.Context, uuid.UUID, GroupResource) error
}

type ClassroomService struct {
	Tokens     GoogleAccessTokenSource
	Gateway    ClassroomGateway
	Store      ClassroomCourseStore
	Operations ExternalOperationRecorder
}

func (s *ClassroomService) Provision(ctx context.Context, groupID uuid.UUID, name, section string, startDate time.Time) (string, error) {
	alias := "p:launchpad-" + groupID.String()
	resource, found, err := s.Store.GetResource(ctx, groupID, classroomProvider, classroomCourseType, alias)
	if err != nil {
		return "", err
	}
	if found && resource.Status == ResourceStatusActive && resource.ExternalID != "" {
		return resource.ExternalID, nil
	}

	token, err := s.Tokens.AccessToken(ctx)
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
	if err := s.Store.SaveClassroomCourse(ctx, groupID, GroupResource{
		Provider: classroomProvider, Type: classroomCourseType, Name: alias, Status: ResourceStatusActive, ExternalID: id,
	}); err != nil {
		return "", err
	}
	return id, nil
}
