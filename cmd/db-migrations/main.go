package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "github.com/lib/pq"

	"github.com/benjamin-wright/db-operator/internal/migrations/discovery"
	"github.com/benjamin-wright/db-operator/internal/migrations/runner"
	"github.com/benjamin-wright/db-operator/internal/migrations/store"
)

func main() {
	var target string
	var migrationsDir string

	flag.StringVar(&target, "target", "", "Target migration ID to apply/rollback to (optional; omit to apply all)")
	flag.StringVar(&migrationsDir, "migrations-dir", "/migrations", "Directory containing migration SQL files")
	flag.Parse()

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
