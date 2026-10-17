package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
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
	db             *sql.DB
	session        *sql.Conn
	authorityToken string
	operationToken string
	readOnly       bool
}

func (s *Store) RequireAuthority(token string) { s.authorityToken = token }
func (s *Store) RequireOperation(token string) { s.operationToken = token }

func (s *Store) ReadOnly(enabled bool) { s.readOnly = enabled }

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func (s *Store) executor() executor {
	if s.session != nil {
		return s.session
	}
	return s.db
}

func (s *Store) EnsureTable() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, err := s.executor().ExecContext(ctx, `
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rows, err := s.executor().QueryContext(ctx, `
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tx, err := s.executor().BeginTx(ctx, nil)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tx, err := s.executor().BeginTx(ctx, nil)
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
	if s.session != nil {
		return fmt.Errorf("migration store already holds a session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquiring migration session: %w", err)
	}
	if _, err := session.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		_ = session.Raw(func(any) error { return driver.ErrBadConn })
		_ = session.Close()
		return fmt.Errorf("acquiring advisory lock: %w", err)
	}
	s.session = session
	if s.readOnly {
		if _, err := session.ExecContext(ctx, "SET default_transaction_read_only = on"); err != nil {
			_ = s.Unlock()
			return err
		}
	}
	if s.authorityToken != "" {
		var token string
		if err := session.QueryRowContext(ctx, "SELECT claim_uid FROM __dbop_control.migration_authority WHERE singleton=TRUE").Scan(&token); err != nil || token != s.authorityToken {
			_ = s.Unlock()
			return fmt.Errorf("migration execution authority is absent or revoked")
		}
	}
	if s.operationToken != "" {
		var operation string
		if err := session.QueryRowContext(ctx, "SELECT operation_name FROM __dbop_control.migration_authority WHERE singleton=TRUE").Scan(&operation); err != nil || operation != s.operationToken {
			_ = s.Unlock()
			return fmt.Errorf("migration operation authority is absent or superseded")
		}
	}
	return nil
}

func (s *Store) Unlock() error {
	if s.session == nil {
		return fmt.Errorf("migration store has no locked session")
	}
	session := s.session
	s.session = nil
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if s.readOnly {
		if _, err := session.ExecContext(ctx, "RESET default_transaction_read_only"); err != nil {
			_ = session.Raw(func(any) error { return driver.ErrBadConn })
			return fmt.Errorf("resetting read-only verification session: %w", err)
		}
	}
	var unlocked bool
	err := session.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", lockKey).Scan(&unlocked)
	if err != nil || !unlocked {
		_ = session.Raw(func(any) error { return driver.ErrBadConn })
		if err != nil {
			return fmt.Errorf("releasing advisory lock: %w", err)
		}
		return fmt.Errorf("migration advisory lock was lost")
	}
	return nil
}
