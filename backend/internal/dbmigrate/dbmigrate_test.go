package dbmigrate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"launchpad/internal/dbmigrate"
	"launchpad/internal/testdb"
)

func TestMigrationRoundTrip(t *testing.T) {
	dsn := testdb.New(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })

	checkVersion := func(want uint) {
		t.Helper()
		version, dirty, err := dbmigrate.Version(dsn)
		if err != nil || dirty || version != want {
			t.Fatalf("schema: version=%d dirty=%t err=%v; want clean version %d", version, dirty, err, want)
		}
	}
	up := func() {
		t.Helper()
		if err := dbmigrate.Up(dsn); err != nil {
			t.Fatal(err)
		}
		checkVersion(16)
	}
	down := func(steps int, want uint) {
		t.Helper()
		if err := dbmigrate.Down(dsn, steps); err != nil {
			t.Fatal(err)
		}
		checkVersion(want)
	}

	up()
	up()
	var dedupeColumn, dedupeIndex bool
	err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='notifications' AND column_name='dedupe_key'),
		to_regclass('uq_notifications_dedupe_key') IS NOT NULL`).Scan(&dedupeColumn, &dedupeIndex)
	if err != nil || !dedupeColumn || !dedupeIndex {
		t.Fatalf("migration 0015 dedupe: column=%t index=%t err=%v", dedupeColumn, dedupeIndex, err)
	}
	var credentialsTable, sessionsTable, predefinedStaffTable bool
	err = conn.QueryRow(ctx, `SELECT to_regclass('user_credentials') IS NOT NULL,
		to_regclass('user_sessions') IS NOT NULL, to_regclass('predefined_staff') IS NOT NULL`).
		Scan(&credentialsTable, &sessionsTable, &predefinedStaffTable)
	if err != nil || !credentialsTable || !sessionsTable || !predefinedStaffTable {
		t.Fatalf("migration 0016 tables: credentials=%t sessions=%t staff=%t err=%v",
			credentialsTable, sessionsTable, predefinedStaffTable, err)
	}
	down(1, 15)
	err = conn.QueryRow(ctx, `SELECT to_regclass('user_credentials') IS NULL,
		to_regclass('user_sessions') IS NULL, to_regclass('predefined_staff') IS NULL`).
		Scan(&credentialsTable, &sessionsTable, &predefinedStaffTable)
	if err != nil || !credentialsTable || !sessionsTable || !predefinedStaffTable {
		t.Fatalf("migration 0016 rollback: credentials gone=%t sessions gone=%t staff gone=%t err=%v",
			credentialsTable, sessionsTable, predefinedStaffTable, err)
	}
	up()
	down(2, 14)
	err = conn.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='notifications' AND column_name='dedupe_key'),
		to_regclass('uq_notifications_dedupe_key') IS NULL`).Scan(&dedupeColumn, &dedupeIndex)
	if err != nil || !dedupeColumn || !dedupeIndex {
		t.Fatalf("migration 0015 rollback: column gone=%t index gone=%t err=%v", dedupeColumn, dedupeIndex, err)
	}
	up()
	var staffTable, notificationsTable, templatesTable bool
	err = conn.QueryRow(ctx, `SELECT to_regclass('staff_assignments') IS NOT NULL,
		to_regclass('notifications') IS NOT NULL, to_regclass('announcement_templates') IS NOT NULL`).
		Scan(&staffTable, &notificationsTable, &templatesTable)
	if err != nil || !staffTable || !notificationsTable || !templatesTable {
		t.Fatalf("migration 0014 tables: staff=%t notifications=%t templates=%t err=%v", staffTable, notificationsTable, templatesTable, err)
	}
	down(3, 13)
	err = conn.QueryRow(ctx, `SELECT to_regclass('staff_assignments') IS NULL,
		to_regclass('notifications') IS NULL, to_regclass('announcement_templates') IS NULL`).
		Scan(&staffTable, &notificationsTable, &templatesTable)
	if err != nil || !staffTable || !notificationsTable || !templatesTable {
		t.Fatalf("migration 0014 rollback: staff=%t notifications=%t templates=%t err=%v", staffTable, notificationsTable, templatesTable, err)
	}
	up()
	down(5, 11) // roll back migrations 0016 through 0012.
	var announcementColumnGone bool
	err = conn.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM information_schema.columns
	    WHERE table_schema=current_schema() AND table_name='scheduled_announcements' AND column_name='request_id')`).Scan(&announcementColumnGone)
	if err != nil || !announcementColumnGone {
		t.Fatalf("announcement rollback: column gone=%t err=%v", announcementColumnGone, err)
	}
	down(1, 10)
	var notificationTableGone bool
	err = conn.QueryRow(ctx, "SELECT to_regclass('audit_notifications') IS NULL").Scan(&notificationTableGone)
	if err != nil || !notificationTableGone {
		t.Fatalf("audit notifications rollback: table gone=%t err=%v", notificationTableGone, err)
	}
	down(1, 9)
	var auditJobColumnGone bool
	err = conn.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM information_schema.columns
	    WHERE table_schema=current_schema() AND table_name='audit_logs' AND column_name='river_job_id')`).Scan(&auditJobColumnGone)
	if err != nil || !auditJobColumnGone {
		t.Fatalf("audit job rollback: column gone=%t err=%v", auditJobColumnGone, err)
	}
	down(1, 8)
	var responseTableGone bool
	err = conn.QueryRow(ctx, "SELECT to_regclass('intake_form_responses') IS NULL").Scan(&responseTableGone)
	if err != nil || !responseTableGone {
		t.Fatalf("intake response rollback: table gone=%t err=%v", responseTableGone, err)
	}
	down(1, 7)
	var deliveryTableGone bool
	err = conn.QueryRow(ctx, "SELECT to_regclass('intake_form_deliveries') IS NULL").Scan(&deliveryTableGone)
	if err != nil || !deliveryTableGone {
		t.Fatalf("intake delivery rollback: table gone=%t err=%v", deliveryTableGone, err)
	}
	down(1, 6)
	var questionsColumn bool
	err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='intake_forms' AND column_name='questions')`).Scan(&questionsColumn)
	if err != nil || questionsColumn {
		t.Fatalf("intake question rollback: column exists=%t err=%v", questionsColumn, err)
	}
	up()
	down(11, 5)
	if _, err := conn.Exec(ctx, "INSERT INTO groups (name, course_name, start_date) VALUES ('preserved', 'Go', CURRENT_DATE)"); err != nil {
		t.Fatal(err)
	}

	up()
	if _, err := conn.Exec(ctx, "INSERT INTO river_job (state, args, kind) VALUES ('pending', '{}', 'ping')"); err != nil {
		t.Fatalf("insert pending job: %v", err)
	}
	down(11, 5)

	var riverGone, groupPresent, emailIndexPresent bool
	err = conn.QueryRow(ctx, `
		SELECT to_regclass('river_job') IS NULL
		   AND to_regclass('river_job_id_seq') IS NULL
		   AND to_regclass('river_queue') IS NULL
		   AND to_regclass('river_leader') IS NULL
		   AND to_regclass('river_notification') IS NULL
		   AND to_regtype('river_job_state') IS NULL,
		   EXISTS (SELECT 1 FROM groups WHERE name = 'preserved'),
		   to_regclass('uq_users_email_normalized') IS NOT NULL
	`).Scan(&riverGone, &groupPresent, &emailIndexPresent)
	if err != nil || !riverGone || !groupPresent || !emailIndexPresent {
		t.Fatalf("rollback: river removed=%t group preserved=%t email index preserved=%t err=%v", riverGone, groupPresent, emailIndexPresent, err)
	}

	down(3, 2) // down to version 2 (not 1/0 — full teardown slows PostgreSQL for other test packages)
	up()
}
