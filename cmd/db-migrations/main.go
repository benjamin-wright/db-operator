package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strconv"

	_ "github.com/lib/pq"

	"github.com/benjamin-wright/db-operator/internal/migrations/artifactfetch"
	"github.com/benjamin-wright/db-operator/internal/migrations/discovery"
	"github.com/benjamin-wright/db-operator/internal/migrations/runner"
	"github.com/benjamin-wright/db-operator/internal/migrations/store"
)

func main() {
	var target *int64
	var migrationsDir string
	var artifactRef string

	flag.Func("target", "Target migration revision to apply/rollback to (optional; omit to apply all)", func(s string) error {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		target = &v
		return nil
	})
	flag.StringVar(&migrationsDir, "migrations-dir", "/migrations", "Directory containing migration SQL files")
	flag.StringVar(&artifactRef, "artifact", "", "OCI reference of a migrations artifact to fetch; overrides --migrations-dir when set")
	flag.Parse()

	targetStr := "<unset>"
	if target != nil {
		targetStr = fmt.Sprintf("%d", *target)
	}
	fmt.Printf("db-migrations starting: artifact=%q migrationsDir=%q target=%s\n", artifactRef, migrationsDir, targetStr)

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
			fatal(fmt.Sprintf("Error fetching artifact %s: %v", artifactRef, err))
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
		fatal(fmt.Sprintf("Error opening database: %v", err))
	}
	defer db.Close()

	fmt.Println("Pinging database...")
	if err := db.Ping(); err != nil {
		fatal(fmt.Sprintf("Error connecting to database host=%s port=%s dbname=%s: %v", pgHost, pgPort, pgDatabase, err))
	}
	fmt.Println("Database connection established.")

	fmt.Printf("Discovering migrations in: %s\n", migrationsDir)
	migrations, err := discovery.Discover(migrationsDir)
	if err != nil {
		fatal(fmt.Sprintf("Error discovering migrations in %s: %v", migrationsDir, err))
	}
	fmt.Printf("Discovered %d migration(s).\n", len(migrations))
	for _, m := range migrations {
		fmt.Printf("  [%s] %s\n", m.ID, m.Name)
	}

	s := store.New(db)

	fmt.Println("Running migrations...")
	if err := runner.Run(s, migrations, target); err != nil {
		fatal(fmt.Sprintf("Error running migrations: %v", err))
	}

	fmt.Println("Migrations completed successfully.")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// fatal writes msg to both stderr and /dev/termination-log (so the Kubernetes
// controller can surface it in the CR status) then exits with code 1.
func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	if f, err := os.OpenFile("/dev/termination-log", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644); err == nil {
		fmt.Fprintln(f, msg)
		f.Close()
	}
	os.Exit(1)
}
