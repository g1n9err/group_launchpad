// Package migrations embeds the SQL migrations so every binary (api, worker,
// migrate) ships with the schema version it expects — no external files needed
// in the container image.
package migrations

import (
	"embed"

	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed *.sql
var fsys embed.FS

// Source returns a golang-migrate source over the embedded SQL files.
func Source() (source.Driver, error) {
	return iofs.New(fsys, ".")
}
