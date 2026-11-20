package integrations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ── fakes ──────────────────────────────────────────────────────────────────

type fakeClassroomGateway struct {
	Courses    map[string]string   // alias → courseID
	Students   map[string][]string // courseID → enrolled emails
	Teachers   map[string][]string // courseID → assigned emails
	FailEmails map[string]bool     // emails that should fail
}

func newFakeClassroomGateway() *fakeClassroomGateway {
	return &fakeClassroomGateway{
		Courses:    map[string]string{},
		Students:   map[string][]string{},
		Teachers:   map[string][]string{},
		FailEmails: map[string]bool{},
	}
}

func (g *fakeClassroomGateway) EnsureCourse(_ context.Context, _ string, in ClassroomCourseInput) (string, error) {
	if id, ok := g.Courses[in.Alias]; ok {
		return id, nil
	}
	id := "C_" + in.Alias
	g.Courses[in.Alias] = id
	return id, nil
}

func (g *fakeClassroomGateway) EnrollStudent(_ context.Context, _, courseID, email string) error {
	if g.FailEmails[email] {
		return errors.New("enrollment failed")
	}
	for _, existing := range g.Students[courseID] {
		if existing == email {
			return nil // already enrolled
		}
	}
	g.Students[courseID] = append(g.Students[courseID], email)
	return nil
}

func (g *fakeClassroomGateway) AssignTeacher(_ context.Context, _, courseID, email string) error {
	if g.FailEmails[email] {
		return errors.New("teacher assignment failed")
	}
	for _, existing := range g.Teachers[courseID] {
		if existing == email {
			return nil // already assigned
		}
	}
	g.Teachers[courseID] = append(g.Teachers[courseID], email)
	return nil
}

type fakeClassroomStore struct {
	resources map[string]GroupResource
}

func newFakeClassroomStore() *fakeClassroomStore {
	return &fakeClassroomStore{resources: map[string]GroupResource{}}
}

type classroomTokenSource struct{}

func (classroomTokenSource) AccessToken(context.Context) (string, error) { return "test-token", nil }

type classroomOpRecorder struct{ ops []ExternalOperation }

func (f *classroomOpRecorder) RecordExternalOperation(_ context.Context, op ExternalOperation) error {
	f.ops = append(f.ops, op)
	return nil
}

func (s *fakeClassroomStore) GetResource(_ context.Context, _ uuid.UUID, _, _, name string) (GroupResource, bool, error) {
	r, ok := s.resources[name]
	return r, ok, nil
}

func (s *fakeClassroomStore) SaveClassroomCourse(_ context.Context, _ uuid.UUID, r GroupResource) error {
	s.resources[r.Name] = r
	return nil
}

func newClassroomService(gw *fakeClassroomGateway, store *fakeClassroomStore) *ClassroomService {
	return &ClassroomService{
		Tokens:     classroomTokenSource{},
		Gateway:    gw,
		Store:      store,
		Operations: &classroomOpRecorder{},
	}
}

// ── tests ──────────────────────────────────────────────────────────────────

func TestClassroomEnrollAll(t *testing.T) {
	gw, store := newFakeClassroomGateway(), newFakeClassroomStore()
	svc := newClassroomService(gw, store)
	groupID := uuid.New()

	courseID, err := svc.Provision(context.Background(), groupID, "Python", "Section 1", time.Now())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	emails := []string{"s1@x.example", "s2@x.example", "s3@x.example"}
	results := svc.EnrollAll(context.Background(), groupID, courseID, emails)

	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("result[%d] (%s): %v", i, r.Email, r.Err)
		}
	}

	// Verify all 3 students enrolled.
	enrolled := gw.Students[courseID]
	if len(enrolled) != 3 {
		t.Errorf("enrolled = %d, want 3", len(enrolled))
	}
}

func TestClassroomEnrollAllIdempotent(t *testing.T) {
	gw, store := newFakeClassroomGateway(), newFakeClassroomStore()
	svc := newClassroomService(gw, store)
	groupID := uuid.New()

	courseID, _ := svc.Provision(context.Background(), groupID, "Test", "S1", time.Now())
	emails := []string{"s1@x.example"}

	// Enroll twice — no duplicates, no errors.
	svc.EnrollAll(context.Background(), groupID, courseID, emails)
	svc.EnrollAll(context.Background(), groupID, courseID, emails)

	if n := len(gw.Students[courseID]); n != 1 {
		t.Errorf("enrolled = %d, want 1 (idempotent)", n)
	}
}

func TestClassroomEnrollAllReportsIndividualFailures(t *testing.T) {
	gw, store := newFakeClassroomGateway(), newFakeClassroomStore()
	gw.FailEmails["bad@x.example"] = true
	svc := newClassroomService(gw, store)
	groupID := uuid.New()

	courseID, _ := svc.Provision(context.Background(), groupID, "Test", "S1", time.Now())
	emails := []string{"good@x.example", "bad@x.example"}
	results := svc.EnrollAll(context.Background(), groupID, courseID, emails)

	if results[0].Err != nil {
		t.Errorf("good email failed: %v", results[0].Err)
	}
	if results[1].Err == nil {
		t.Error("bad email should have failed")
	}
	// Good one still enrolled.
	if len(gw.Students[courseID]) != 1 {
		t.Errorf("enrolled = %d, want 1 (only the good one)", len(gw.Students[courseID]))
	}
}

func TestClassroomAssignTeachers(t *testing.T) {
	gw, store := newFakeClassroomGateway(), newFakeClassroomStore()
	svc := newClassroomService(gw, store)
	groupID := uuid.New()

	courseID, _ := svc.Provision(context.Background(), groupID, "Test", "S1", time.Now())
	emails := []string{"teacher1@x.example", "ta@x.example"}
	results := svc.AssignTeachers(context.Background(), groupID, courseID, emails)

	for i, r := range results {
		if r.Err != nil {
			t.Errorf("result[%d]: %v", i, r.Err)
		}
	}
	if len(gw.Teachers[courseID]) != 2 {
		t.Errorf("teachers = %d, want 2", len(gw.Teachers[courseID]))
	}
}

func TestClassroomProvisionIdempotent(t *testing.T) {
	gw, store := newFakeClassroomGateway(), newFakeClassroomStore()
	svc := newClassroomService(gw, store)
	groupID := uuid.New()

	id1, err := svc.Provision(context.Background(), groupID, "Python", "S1", time.Now())
	if err != nil {
		t.Fatalf("first provision: %v", err)
	}
	id2, err := svc.Provision(context.Background(), groupID, "Python", "S1", time.Now())
	if err != nil {
		t.Fatalf("second provision: %v", err)
	}
	if id1 != id2 {
		t.Errorf("course IDs differ: %q vs %q (not idempotent)", id1, id2)
	}
}
