// Package dbmigrate applies embedded SQL migrations via golang-migrate.
// Migrations run in transactions; golang-migrate takes an advisory lock, so
// concurrent boots (e.g. api replicas) are safe. Tracked in schema_migrations.
package dbmigrate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"

	// register the pgx/v5 database driver (scheme: pgx5://)
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"

	"launchpad/migrations"
)

func newMigrate(databaseURL string) (*migrate.Migrate, error) {
	src, err := migrations.Source()
	if err != nil {
		return nil, fmt.Errorf("dbmigrate: embed source: %w", err)
	}
	url := strings.Replace(databaseURL, "postgres://", "pgx5://", 1)
	m, err := migrate.NewWithSourceInstance("iofs", src, url)
	if err != nil {
		return nil, fmt.Errorf("dbmigrate: open: %w", err)
	}
	return m, nil
}

// Up applies all pending migrations. No-op when already current.
func Up(databaseURL string) error {
	m, err := newMigrate(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("dbmigrate: up: %w", err)
	}
	return nil
}

// Down rolls back the last N migrations.
func Down(databaseURL string, steps int) error {
	m, err := newMigrate(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("dbmigrate: down: %w", err)
	}
	return nil
}

// Version reports the current schema version and whether it is dirty.
func Version(databaseURL string) (uint, bool, error) {
	m, err := newMigrate(databaseURL)
	if err != nil {
		return 0, false, err
	}
	defer m.Close()
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, dirty, nil
}
