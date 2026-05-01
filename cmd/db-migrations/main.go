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

	fmt.Printf("db-migrations starting: artifact=%q migrationsDir=%q target=%d\n", artifactRef, migrationsDir, targetFlag)

	var target *int64
	if targetFlag >= 0 {
		target = &targetFlag
	}

	if artifactRef != "" {
		fmt.Printf("Fetching OCI artifact: %s\n", artifactRef)
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
		fmt.Printf("Fetched artifact %s (digest: %s)\n", artifactRef, digest)
		migrationsDir = dir
	}

	pgHost := envOrDefault("PGHOST", "localhost")
	pgPort := envOrDefault("PGPORT", "5432")
	pgUser := envOrDefault("PGUSER", "postgres")
	pgDatabase := envOrDefault("PGDATABASE", "postgres")
	fmt.Printf("Connecting to PostgreSQL: host=%s port=%s user=%s dbname=%s\n", pgHost, pgPort, pgUser, pgDatabase)

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		pgHost,
		pgPort,
		pgUser,
		envOrDefault("PGPASSWORD", "postgres"),
		pgDatabase,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Println("Pinging database...")
	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to database: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Database connection established.")

	fmt.Printf("Discovering migrations in: %s\n", migrationsDir)
	migrations, err := discovery.Discover(migrationsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering migrations: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Discovered %d migration(s).\n", len(migrations))
	for _, m := range migrations {
		fmt.Printf("  [%s] %s\n", m.ID, m.Name)
	}

	s := store.New(db)

	fmt.Println("Running migrations...")
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
