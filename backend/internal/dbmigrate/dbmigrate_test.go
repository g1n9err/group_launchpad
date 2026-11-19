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
		checkVersion(11)
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
	down(6, 5)
	if _, err := conn.Exec(ctx, "INSERT INTO groups (name, course_name, start_date) VALUES ('preserved', 'Go', CURRENT_DATE)"); err != nil {
		t.Fatal(err)
	}

	up()
	if _, err := conn.Exec(ctx, "INSERT INTO river_job (state, args, kind) VALUES ('pending', '{}', 'ping')"); err != nil {
		t.Fatalf("insert pending job: %v", err)
	}
	down(6, 5)

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

	down(5, 0)
	up()
}
