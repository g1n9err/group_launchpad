// cmd/migrate — schema migrations as a standalone command. Works identically
// in dev, CI, and containers because migrations are embedded in the binary.
//
// Usage:
//
//	go run ./cmd/migrate up          # apply all pending
//	go run ./cmd/migrate down 1      # roll back N migrations (default 1)
//	go run ./cmd/migrate version     # print current version
package main

import (
	"fmt"
	"os"

	"launchpad/internal/config"
	"launchpad/internal/dbmigrate"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}

	cmd := "up"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "up":
		if err := dbmigrate.Up(cfg.DatabaseURL); err != nil {
			fmt.Fprintln(os.Stderr, "fatal:", err)
			os.Exit(1)
		}
		fmt.Println("migrations: up to date")
	case "down":
		steps := 1
		if len(os.Args) > 2 {
			fmt.Sscanf(os.Args[2], "%d", &steps) //nolint:errcheck — default 1 on parse failure
		}
		if err := dbmigrate.Down(cfg.DatabaseURL, steps); err != nil {
			fmt.Fprintln(os.Stderr, "fatal:", err)
			os.Exit(1)
		}
		fmt.Printf("migrations: rolled back %d step(s)\n", steps)
	case "version":
		v, dirty, err := dbmigrate.Version(cfg.DatabaseURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fatal:", err)
			os.Exit(1)
		}
		fmt.Printf("schema version: %d (dirty=%v)\n", v, dirty)
	default:
		fmt.Fprintln(os.Stderr, "usage: migrate [up|down N|version]")
		os.Exit(2)
	}
}
