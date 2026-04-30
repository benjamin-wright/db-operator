package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "github.com/lib/pq"

	"github.com/benjamin-wright/db-operator/internal/migrations/artifactfetch"
	"github.com/benjamin-wright/db-operator/internal/migrations/discovery"
	"github.com/benjamin-wright/db-operator/internal/migrations/runner"
	"github.com/benjamin-wright/db-operator/internal/migrations/store"
)

func main() {
	// -1 sentinel means "unset" — distinguishes from a valid 0 target.
	var targetFlag int64
	var migrationsDir string
	var artifactRef string

	flag.Int64Var(&targetFlag, "target", -1, "Target migration revision to apply/rollback to (optional; omit to apply all)")
	flag.StringVar(&migrationsDir, "migrations-dir", "/migrations", "Directory containing migration SQL files")
	flag.StringVar(&artifactRef, "artifact", "", "OCI reference of a migrations artifact to fetch; overrides --migrations-dir when set")
	flag.Parse()

	var target *int64
	if targetFlag >= 0 {
		target = &targetFlag
	}

	if artifactRef != "" {
		dir, err := os.MkdirTemp("", "db-migrations-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating workspace: %v\n", err)
			os.Exit(1)
		}
		defer os.RemoveAll(dir)

		digest, err := artifactfetch.Fetch(context.Background(), artifactRef, dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching artifact %s: %v\n", artifactRef, err)
			os.Exit(1)
		}
		fmt.Printf("Fetched artifact %s (%s)\n", artifactRef, digest)
		migrationsDir = dir
	}

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		envOrDefault("PGHOST", "localhost"),
		envOrDefault("PGPORT", "5432"),
		envOrDefault("PGUSER", "postgres"),
		envOrDefault("PGPASSWORD", "postgres"),
		envOrDefault("PGDATABASE", "postgres"),
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to database: %v\n", err)
		os.Exit(1)
	}

	migrations, err := discovery.Discover(migrationsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering migrations: %v\n", err)
		os.Exit(1)
	}

	s := store.New(db)

	if err := runner.Run(s, migrations, target); err != nil {
		fmt.Fprintf(os.Stderr, "Error running migrations: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Migrations completed successfully.")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
