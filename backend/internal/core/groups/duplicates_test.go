package groups

// Determinism test for cross-role duplicate validation: the reported field
// and message must be identical on EVERY execution. The original map-based
// iteration produced a random field per run; this test runs the same input
// many times so any regression to nondeterminism fails immediately.

import (
	"context"
	"testing"

	"launchpad/internal/core"
)

// TestCreateCrossRoleDuplicateReportingIsDeterministic repeats each duplicate
// scenario and requires the SAME field and message every time. The duplicate
// is always reported against the LATER category in the fixed order
// staffEmails → lecturerEmails → taEmails.
func TestCreateCrossRoleDuplicateReportingIsDeterministic(t *testing.T) {
	tests := []struct {
		name      string
		in        CreateInput
		wantField string
		wantMsg   string
	}{
		{
			name: "staff+lecturer duplicate reports lecturerEmails",
			in: CreateInput{Name: "Python 13", CourseName: "Go", StartDate: "2026-12-01",
				StaffEmails: []string{"dup@t.e"}, LecturerEmails: []string{"DUP@t.e"}},
			wantField: "lecturerEmails",
			wantMsg:   "dup@t.e already assigned as staffEmails",
		},
		{
			name: "staff+ta duplicate reports taEmails",
			in: CreateInput{Name: "Python 13", CourseName: "Go", StartDate: "2026-12-01",
				StaffEmails: []string{"dup@t.e"}, TAEmails: []string{"dup@t.e"}},
			wantField: "taEmails",
			wantMsg:   "dup@t.e already assigned as staffEmails",
		},
		{
			name: "lecturer+ta duplicate reports taEmails",
			in: CreateInput{Name: "Python 13", CourseName: "Go", StartDate: "2026-12-01",
				StaffEmails: []string{"staff@t.e"}, LecturerEmails: []string{"dup@t.e"}, TAEmails: []string{"dup@t.e"}},
			wantField: "taEmails",
			wantMsg:   "dup@t.e already assigned as lecturerEmails",
		},
	}

	const iterations = 100
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for i := range iterations {
				svc, _ := newService()
				_, err := svc.Create(context.Background(), tc.in)
				if err == nil {
					t.Fatalf("iteration %d: expected validation error, got nil", i)
				}
				appErr, ok := err.(*core.AppError)
				if !ok {
					t.Fatalf("iteration %d: expected *core.AppError, got %T: %v", i, err, err)
				}
				if got := appErr.FieldErrors[tc.wantField]; got != tc.wantMsg {
					t.Fatalf("iteration %d: field %q = %q, want %q (all errors: %v)",
						i, tc.wantField, got, tc.wantMsg, appErr.FieldErrors)
				}
			}
		})
	}
}
