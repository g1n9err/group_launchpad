// Package testdb creates throwaway databases for PostgreSQL integration tests.
package testdb

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// New returns the DSN of a newly created database. TEST_DATABASE_URL must
// point to a test server and grant permission to create databases.
func New(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	dsn, err := url.Parse(base)
	if err != nil || (dsn.Scheme != "postgres" && dsn.Scheme != "postgresql") {
		t.Fatal("invalid TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect to PostgreSQL test server: %v", err)
	}
	name := "launchpad_test_" + uuid.NewString()
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+identifier); err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE "+identifier); err != nil {
			t.Errorf("drop test database: %v", err)
		}
	})
	// Preserve the base DSN's options while changing the target database.
	dsn.Path = "/" + name
	dsn.RawPath = ""
	query := dsn.Query()
	query.Del("dbname")
	query.Del("database")
	dsn.RawQuery = query.Encode()
	return dsn.String()
}
