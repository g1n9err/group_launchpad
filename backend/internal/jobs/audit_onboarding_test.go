package jobs_test

// Onboarding notification tests (PRD §4.9 "Incomplete student onboarding"):
// audits that run at or after the group's start date with pending students
// create exactly one warning notification per audit run; future groups and
// fully joined groups create none.

import (
	"context"
	"strings"
	"testing"
	"time"

	"launchpad/internal/core/audit"
	"launchpad/internal/core/groups"
	"launchpad/internal/store"
)

func TestAuditEmitsOnboardingIncompleteNotification(t *testing.T) {
	pool := testPool(t)
	repo := store.New(pool)
	groupsSvc := groups.New(repo)
	ctx := context.Background()

	// A started group with one joined and one pending student.
	group, err := groupsSvc.Create(ctx, groups.CreateInput{
		Name: "Onboarding Notify", CourseName: "Course", StartDate: time.Now().AddDate(0, 0, -1).Format("2006-01-02"),
		StudentEmails:  []string{"joined@test.example", "pending@test.example"},
		StaffEmails:    []string{"staff@test.example"},
		LecturerEmails: []string{"lecturer@test.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE group_members SET onboarding_status='joined_slack'
		WHERE group_id=$1 AND member_role='student'`, group.ID); err != nil {
		t.Fatal(err)
	}
	// Undo for exactly one student (the roster order is deterministic by email).
	if _, err := pool.Exec(ctx, `UPDATE group_members m SET onboarding_status='pending'
		FROM users u WHERE m.user_id=u.id AND m.group_id=$1 AND u.email='pending@test.example'`, group.ID); err != nil {
		t.Fatal(err)
	}

	svc := &audit.Service{Store: repo} // no checkers: results report "unavailable"
	if _, err := svc.Run(ctx, group.ID, 9001, "daily_sync", false); err != nil {
		t.Fatalf("audit run: %v", err)
	}

	var count int
	var detail, dedupe string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(MAX(detail),''), COALESCE(MAX(dedupe_key),'')
		 FROM notifications WHERE group_id=$1 AND kind='onboarding_incomplete'`, group.ID).
		Scan(&count, &detail, &dedupe); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("onboarding notifications=%d, want 1", count)
	}
	if dedupe != "group_audit:9001:onboarding" {
		t.Errorf("dedupe key=%q", dedupe)
	}
	// Aggregate, PII-safe detail: counts only, no student emails.
	wantPrefix := "1 of 2 students have not joined Slack"
	if len(detail) < len(wantPrefix) || detail[:len(wantPrefix)] != wantPrefix {
		t.Errorf("detail=%q, want prefix %q", detail, wantPrefix)
	}
	if detail != wantPrefix+" (group started "+time.Now().AddDate(0, 0, -1).Format("2006-01-02")+")" {
		t.Errorf("detail=%q", detail)
	}
	if strings.Contains(detail, "@test.example") {
		t.Errorf("detail leaks student emails: %q", detail)
	}

	// A future group must NOT receive the notification.
	future, err := groupsSvc.Create(ctx, groups.CreateInput{
		Name: "Future Onboarding", CourseName: "Course", StartDate: time.Now().AddDate(1, 0, 0).Format("2006-01-02"),
		StudentEmails:  []string{"future-pending@test.example"},
		StaffEmails:    []string{"staff@test.example"},
		LecturerEmails: []string{"lecturer@test.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Run(ctx, future.ID, 9002, "daily_sync", false); err != nil {
		t.Fatal(err)
	}
	var futureCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE group_id=$1 AND kind='onboarding_incomplete'`, future.ID).Scan(&futureCount); err != nil {
		t.Fatal(err)
	}
	if futureCount != 0 {
		t.Errorf("future group got %d onboarding notifications, want 0", futureCount)
	}

	// Re-running the same audit job must not duplicate (stable dedupe key).
	if _, err := svc.Run(ctx, group.ID, 9001, "daily_sync", false); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE group_id=$1 AND kind='onboarding_incomplete'`, group.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("onboarding notifications after re-run=%d, want 1", count)
	}
}

func TestAuditSkipsOnboardingNotificationWhenAllJoined(t *testing.T) {
	pool := testPool(t)
	repo := store.New(pool)
	groupsSvc := groups.New(repo)
	ctx := context.Background()

	// Started group where every student has joined: no notification.
	group, err := groupsSvc.Create(ctx, groups.CreateInput{
		Name: "All Joined", CourseName: "Course", StartDate: time.Now().AddDate(0, 0, -2).Format("2006-01-02"),
		StudentEmails:  []string{"complete@test.example"},
		StaffEmails:    []string{"staff@test.example"},
		LecturerEmails: []string{"lecturer@test.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE group_members SET onboarding_status='complete'
		WHERE group_id=$1 AND member_role='student'`, group.ID); err != nil {
		t.Fatal(err)
	}
	svc := &audit.Service{Store: repo}
	if _, err := svc.Run(ctx, group.ID, 9003, "daily_sync", false); err != nil {
		t.Fatalf("audit run: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE group_id=$1 AND kind='onboarding_incomplete'`, group.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("fully joined group got %d onboarding notifications, want 0", count)
	}
}
