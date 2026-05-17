package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Record represents a row in the _migrations tracking table.
type Record struct {
	ID           string
	Name         string
	AppliedAt    time.Time
	ApplyHash    string
	RollbackHash string
}

// Store manages the _migrations tracking table.
type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) EnsureTable() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS _migrations (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			apply_hash TEXT NOT NULL,
			rollback_hash TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("creating _migrations table: %w", err)
	}
	return nil
}

func (s *Store) Applied() ([]Record, error) {
	rows, err := s.db.Query(`
		SELECT id, name, applied_at, apply_hash, rollback_hash
		FROM _migrations
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("querying applied migrations: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.Name, &r.AppliedAt, &r.ApplyHash, &r.RollbackHash); err != nil {
			return nil, fmt.Errorf("scanning migration record: %w", err)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// Apply executes sqlContent in a transaction and records the migration in _migrations.
// Rolls back on any failure.
func (s *Store) Apply(id, name, sqlContent, applyHash, rollbackHash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(sqlContent); err != nil {
		return fmt.Errorf("executing migration %s: %w", id, err)
	}

	if _, err := tx.Exec(
		`INSERT INTO _migrations (id, name, apply_hash, rollback_hash) VALUES ($1, $2, $3, $4)`,
		id, name, applyHash, rollbackHash,
	); err != nil {
		return fmt.Errorf("recording migration %s: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration %s: %w", id, err)
	}
	return nil
}

// Rollback executes sqlContent in a transaction and removes the migration record from _migrations.
// Rolls back on any failure.
func (s *Store) Rollback(id, sqlContent string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(sqlContent); err != nil {
		return fmt.Errorf("executing rollback for migration %s: %w", id, err)
	}

	if _, err := tx.Exec(`DELETE FROM _migrations WHERE id = $1`, id); err != nil {
		return fmt.Errorf("removing record for migration %s: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing rollback for migration %s: %w", id, err)
	}
	return nil
}

// lockKey is the pinned advisory lock identifier for the _migrations table.
// It is derived from hashtext('_migrations') and kept as a constant to ensure
// all competing processes use the same lock.
const lockKey = int64(0x5f6d6967726174)

// Lock acquires a session-scoped PostgreSQL advisory lock. Concurrent callers
// block until the lock is released, ensuring that only one migration runner
// operates at a time.
func (s *Store) Lock() error {
	if _, err := s.db.Exec("SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return fmt.Errorf("acquiring advisory lock: %w", err)
	}
	return nil
}

func (s *Store) Unlock() error {
	if _, err := s.db.Exec("SELECT pg_advisory_unlock($1)", lockKey); err != nil {
		return fmt.Errorf("releasing advisory lock: %w", err)
	}
	return nil
}
