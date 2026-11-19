package groups

import (
	"context"
	"strings"
	"testing"
	"time"

	"launchpad/internal/core"
)

func TestCreateStudentEmailsValidation(t *testing.T) {
	tests := []struct {
		name      string
		emails    []string
		wantField string
		wantInMsg string
	}{
		{"invalid format", []string{"not-an-email"}, "studentEmails", "invalid email format"},
		{"missing domain tld", []string{"a@b"}, "studentEmails", "invalid email format"},
		{"empty after trim", []string{"   "}, "studentEmails", "invalid email format"},
		{"exact duplicate", []string{"a@x.example", "a@x.example"}, "studentEmails", "duplicate email addresses: a@x.example"},
		{"case-variant duplicate", []string{"A@X.example", "a@x.example"}, "studentEmails", "duplicate email addresses: a@x.example"},
		{"whitespace-variant duplicate", []string{" a@x.example ", "a@x.example"}, "studentEmails", "duplicate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newService()
			_, err := svc.Create(context.Background(), CreateInput{
				Name: "G", CourseName: "C", StartDate: "2026-12-01", StudentEmails: tc.emails,
			})
			appErr, ok := err.(*core.AppError)
			if !ok {
				t.Fatalf("expected AppError, got %v", err)
			}
			if msg, present := appErr.FieldErrors[tc.wantField]; !present || !strings.Contains(msg, tc.wantInMsg) {
				t.Errorf("fieldErrors[%s] = %q, want containing %q", tc.wantField, msg, tc.wantInMsg)
			}
		})
	}
}

func TestCreateStudentEmailsNormalizedAndForwarded(t *testing.T) {
	svc, repo := newService()
	_, err := svc.Create(context.Background(), CreateInput{
		Name:          "G",
		CourseName:    "C",
		StartDate:     "2026-12-01",
		StudentEmails: []string{"  Mixed.Case@School.EDU ", "other@school.edu"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := []string{"mixed.case@school.edu", "other@school.edu"}
	if len(repo.lastStudents) != 2 {
		t.Fatalf("students = %v, want 2", repo.lastStudents)
	}
	for i, w := range want {
		if repo.lastStudents[i] != w {
			t.Errorf("student[%d] = %q, want %q", i, repo.lastStudents[i], w)
		}
	}
}

func TestCreateWithoutStudentsIsBackwardCompatible(t *testing.T) {
	svc, repo := newService()
	g, err := svc.Create(context.Background(), CreateInput{
		Name: "Legacy", CourseName: "C", StartDate: "2026-12-01", // no StudentEmails
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if g.Status != StatusDraft {
		t.Errorf("status = %q", g.Status)
	}
	if repo.lastStudents != nil {
		t.Errorf("students forwarded = %v, want nil", repo.lastStudents)
	}
}

func TestCreateTooManyStudentsRejected(t *testing.T) {
	svc, _ := newService()
	many := make([]string, maxStudentEmails+1)
	for i := range many {
		many[i] = "s" + string(rune('a'+i%26)) + strconv(i%10) + "@x.example"
	}
	_, err := svc.Create(context.Background(), CreateInput{
		Name: "G", CourseName: "C", StartDate: "2026-12-01", StudentEmails: many,
	})
	appErr, ok := err.(*core.AppError)
	if !ok || appErr.FieldErrors["studentEmails"] == "" {
		t.Fatalf("expected studentEmails field error, got %v", err)
	}
}

func strconv(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

var _ = time.Now // keep time import when assertions shrink
