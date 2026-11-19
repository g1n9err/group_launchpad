package groups

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"launchpad/internal/core"
)

// stubRepo is an in-memory Repository for pure unit tests (no database).
type stubRepo struct {
	groups       []Group
	lastStudents []string
	lastListArgs struct{ limit, offset int }
}

func (r *stubRepo) Create(_ context.Context, p CreateParams) (Group, error) {
	for _, g := range r.groups {
		if g.Name == p.Name {
			return Group{}, core.DuplicateGroup(p.Name)
		}
	}
	g := Group{ID: uuid.New(), Name: p.Name, CourseName: p.CourseName, StartDate: p.StartDate, Status: StatusDraft, CreatedAt: time.Now().UTC()}
	r.groups = append(r.groups, g)
	r.lastStudents = p.StudentEmails
	return g, nil
}

func (r *stubRepo) GetByID(_ context.Context, id uuid.UUID) (Group, error) {
	for _, g := range r.groups {
		if g.ID == id {
			return g, nil
		}
	}
	return Group{}, core.NotFound("group", id.String())
}

func (r *stubRepo) List(_ context.Context, limit, offset int) ([]Group, int, error) {
	r.lastListArgs.limit, r.lastListArgs.offset = limit, offset

	total := len(r.groups)
	end := offset + limit
	if end > total {
		end = total
	}
	if offset > total {
		offset = total
	}
	return r.groups[offset:end], total, nil
}

func (r *stubRepo) StudentRoster(context.Context, uuid.UUID) ([]Student, error) {
	return nil, nil
}

func newService() (*Service, *stubRepo) {
	repo := &stubRepo{}
	return New(repo), repo
}

func TestCreateValidation(t *testing.T) {
	tests := []struct {
		name        string
		in          CreateInput
		wantField   string // expected entry in FieldErrors
		wantMessage string
	}{
		{"empty name", CreateInput{Name: "", CourseName: "Go", StartDate: "2026-12-01"}, "name", "invalid group data"},
		{"whitespace name", CreateInput{Name: "   ", CourseName: "Go", StartDate: "2026-12-01"}, "name", "invalid group data"},
		{"name too long", CreateInput{Name: string(make([]byte, 201)), CourseName: "Go", StartDate: "2026-12-01"}, "name", "invalid group data"},
		{"empty course", CreateInput{Name: "Python 13", CourseName: "", StartDate: "2026-12-01"}, "courseName", "invalid group data"},
		{"missing date", CreateInput{Name: "Python 13", CourseName: "Go"}, "startDate", "invalid group data"},
		{"malformed date", CreateInput{Name: "Python 13", CourseName: "Go", StartDate: "01-12-2026"}, "startDate", "invalid group data"},
		{"impossible date", CreateInput{Name: "Python 13", CourseName: "Go", StartDate: "2026-02-30"}, "startDate", "invalid group data"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newService()
			_, err := svc.Create(context.Background(), tc.in)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			appErr, ok := err.(*core.AppError)
			if !ok {
				t.Fatalf("expected *core.AppError, got %T: %v", err, err)
			}
			if appErr.Code != core.CodeValidation {
				t.Errorf("code = %q, want %q", appErr.Code, core.CodeValidation)
			}
			if appErr.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", appErr.Message, tc.wantMessage)
			}
			if _, present := appErr.FieldErrors[tc.wantField]; !present {
				t.Errorf("expected field error for %q, got %v", tc.wantField, appErr.FieldErrors)
			}
		})
	}
}

func TestCreateHappyPathTrimsAndDefaultsToDraft(t *testing.T) {
	svc, _ := newService()
	g, err := svc.Create(context.Background(), CreateInput{
		Name: "  Python Backend 13  ", CourseName: " Go ", StartDate: "2026-12-01",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.Name != "Python Backend 13" || g.CourseName != "Go" {
		t.Errorf("input not trimmed: %+v", g)
	}
	if g.Status != StatusDraft {
		t.Errorf("status = %q, want draft", g.Status)
	}
	want := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if !g.StartDate.Equal(want) {
		t.Errorf("startDate = %v, want %v", g.StartDate, want)
	}
}

func TestCreateDuplicateName(t *testing.T) {
	svc, _ := newService()
	in := CreateInput{Name: "Python Backend 13", CourseName: "Go", StartDate: "2026-12-01"}
	if _, err := svc.Create(context.Background(), in); err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	_, err := svc.Create(context.Background(), in)
	appErr, ok := err.(*core.AppError)
	if !ok {
		t.Fatalf("expected *core.AppError, got %T: %v", err, err)
	}
	if appErr.Code != core.CodeDuplicate {
		t.Errorf("code = %q, want %q", appErr.Code, core.CodeDuplicate)
	}
}

func TestGetRejectsInvalidUUID(t *testing.T) {
	svc, _ := newService()
	_, err := svc.Get(context.Background(), "not-a-uuid")
	appErr, ok := err.(*core.AppError)
	if !ok {
		t.Fatalf("expected *core.AppError, got %T: %v", err, err)
	}
	if appErr.Code != core.CodeValidation {
		t.Errorf("code = %q, want %q", appErr.Code, core.CodeValidation)
	}
	if _, present := appErr.FieldErrors["groupId"]; !present {
		t.Errorf("expected fieldErrors[groupId], got %v", appErr.FieldErrors)
	}
}

func TestGetUnknownIDReturnsNotFound(t *testing.T) {
	svc, _ := newService()
	_, err := svc.Get(context.Background(), uuid.NewString())
	appErr, ok := err.(*core.AppError)
	if !ok || appErr.Code != core.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

func TestListClampsPagination(t *testing.T) {
	tests := []struct {
		name       string
		limit      int
		offset     int
		wantLimit  int
		wantOffset int
	}{
		{"zero limit becomes default", 0, 0, 50, 0},
		{"negative offset clamps", 10, -5, 10, 0},
		{"oversized limit clamps", 10_000, 0, 200, 0},
		{"valid passthrough", 25, 10, 25, 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newService()
			for i := 0; i < 3; i++ {
				if _, err := svc.Create(context.Background(), CreateInput{
					Name:       time.Now().Add(time.Duration(i) * time.Second).String(), // unique names
					CourseName: "Go", StartDate: "2026-12-01",
				}); err != nil {
					t.Fatalf("seed create failed: %v", err)
				}
			}
			svc.List(context.Background(), tc.limit, tc.offset) //nolint:errcheck — clamping is what we assert on
			if got := repo.lastListArgs; got.limit != tc.wantLimit || got.offset != tc.wantOffset {
				t.Errorf("repo saw limit=%d offset=%d, want %d/%d", got.limit, got.offset, tc.wantLimit, tc.wantOffset)
			}
		})
	}
}
