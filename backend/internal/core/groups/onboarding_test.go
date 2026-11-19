package groups

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// rosterStubRepo returns a fixed roster.
type rosterStubRepo struct {
	stubRepo
	roster []Student
}

func (r *rosterStubRepo) StudentRoster(context.Context, uuid.UUID) ([]Student, error) {
	return r.roster, nil
}

func mkStudent(name, status string) Student {
	return Student{UserID: uuid.New(), FullName: name, Email: name + "@x.example", OnboardingStatus: status}
}

func TestSummarizeRosterClassification(t *testing.T) {
	roster := []Student{
		mkStudent("a", "pending"),
		mkStudent("b", "invited"),
		mkStudent("c", "joined_slack"),
		mkStudent("d", "form_submitted"),
		mkStudent("e", "complete"),
	}
	s := summarizeRoster(roster)
	if s.ExpectedCount != 5 {
		t.Errorf("expected = %d", s.ExpectedCount)
	}
	if s.JoinedCount != 3 { // joined_slack + form_submitted + complete
		t.Errorf("joined = %d, want 3", s.JoinedCount)
	}
	if s.PendingCount != 2 { // pending + invited
		t.Errorf("pending = %d, want 2", s.PendingCount)
	}
}

func TestCompletionPercentageRounding(t *testing.T) {
	tests := []struct {
		joined, expected int
		want             float64
	}{
		{0, 0, 0},      // empty roster
		{0, 5, 0},      // none joined
		{5, 5, 100},    // all joined
		{21, 24, 87.5}, // PRD example
		{1, 3, 33.3},
		{2, 3, 66.7},
		{1, 8, 12.5},
	}
	for _, tc := range tests {
		got := completionPercentage(tc.joined, tc.expected)
		if got != tc.want {
			t.Errorf("completionPercentage(%d,%d) = %v, want %v", tc.joined, tc.expected, got, tc.want)
		}
	}
}

func TestOnboardingEmptyRosterIsZeroCounts(t *testing.T) {
	repo := &rosterStubRepo{}
	svc := New(repo)
	g, _ := repo.Create(context.Background(), CreateParams{Name: "Empty", CourseName: "C"})

	report, err := svc.Onboarding(context.Background(), g.ID.String())
	if err != nil {
		t.Fatalf("Onboarding: %v", err)
	}
	if report.Summary.ExpectedCount != 0 || report.Summary.JoinedCount != 0 || report.Summary.CompletionPercentage != 0 {
		t.Errorf("empty summary = %+v, want zeros", report.Summary)
	}
	if len(report.Students) != 0 {
		t.Errorf("students = %d, want 0", len(report.Students))
	}
}

func TestOnboardingInvalidUUIDRejected(t *testing.T) {
	svc := New(&rosterStubRepo{})
	if _, err := svc.Onboarding(context.Background(), "nope"); err == nil {
		t.Fatal("expected validation error")
	}
}
