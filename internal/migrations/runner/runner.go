package runner

import (
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/benjamin-wright/db-operator/internal/migrations/discovery"
	"github.com/benjamin-wright/db-operator/internal/migrations/store"
)

// MigrationStore abstracts all database interaction required by the runner,
// enabling unit testing with a fake implementation.
type MigrationStore interface {
	EnsureTable() error
	Applied() ([]store.Record, error)
	Apply(id, name, sqlContent, applyHash, rollbackHash string) error
	Rollback(id, sqlContent string) error
	// Lock acquires a session-scoped advisory lock so that concurrent migration
	// Job pods serialise rather than racing on the _migrations table.
	Lock() error
	// Unlock releases the advisory lock acquired by Lock.
	Unlock() error
}

// action represents the type of operation to perform on a single migration.
type action int

const (
	actionApply    action = iota
	actionRollback action = iota
)

// step describes a single migration action to execute.
type step struct {
	action    action
	migration discovery.Migration
}

// plan determines the sequence of steps needed to reach the desired state.
func plan(migrations []discovery.Migration, applied []store.Record, target string) ([]step, error) {
	appliedSet := make(map[string]store.Record)
	for _, r := range applied {
		appliedSet[r.ID] = r
	}

	// Build index of discovered migrations by ID
	migrationIdx := make(map[string]discovery.Migration)
	for _, m := range migrations {
		migrationIdx[m.ID] = m
	}

	// Validate integrity: every applied migration must still exist on disk with
	// the same content hashes.
	for _, r := range applied {
		m, exists := migrationIdx[r.ID]
		if !exists {
			return nil, fmt.Errorf("applied migration %s (%s) not found on disk", r.ID, r.Name)
		}

		applyHash, err := hashFile(m.ApplyPath)
		if err != nil {
			return nil, fmt.Errorf("hashing apply file for migration %s: %w", r.ID, err)
		}
		if applyHash != r.ApplyHash {
			return nil, fmt.Errorf("integrity error: apply file for migration %s (%s) has been modified (expected %s, got %s)", r.ID, r.Name, r.ApplyHash, applyHash)
		}

		rollbackHash, err := hashFile(m.RollbackPath)
		if err != nil {
			return nil, fmt.Errorf("hashing rollback file for migration %s: %w", r.ID, err)
		}
		if rollbackHash != r.RollbackHash {
			return nil, fmt.Errorf("integrity error: rollback file for migration %s (%s) has been modified (expected %s, got %s)", r.ID, r.Name, r.RollbackHash, rollbackHash)
		}
	}

	// Determine direction
	var steps []step

	if target == "" {
		// Apply all unapplied migrations in order
		for _, m := range migrations {
			if _, applied := appliedSet[m.ID]; !applied {
				steps = append(steps, step{action: actionApply, migration: m})
			}
		}
		return steps, nil
	}

	// Find target index in the discovered migrations list
	targetIdx := -1
	for i, m := range migrations {
		if m.ID == target {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		return nil, fmt.Errorf("target migration %s not found in discovered migrations", target)
	}

	// Find current state: the last applied migration in discovered order
	currentIdx := -1
	for i := len(migrations) - 1; i >= 0; i-- {
		if _, ok := appliedSet[migrations[i].ID]; ok {
			currentIdx = i
			break
		}
	}

	if targetIdx > currentIdx {
		// Apply forward: apply unapplied migrations from currentIdx+1 to targetIdx (inclusive)
		for i := currentIdx + 1; i <= targetIdx; i++ {
			if _, ok := appliedSet[migrations[i].ID]; !ok {
				steps = append(steps, step{action: actionApply, migration: migrations[i]})
			}
		}
	} else if targetIdx < currentIdx {
		// Rollback: rollback from currentIdx down to targetIdx+1 (exclusive of target) in reverse
		for i := currentIdx; i > targetIdx; i-- {
			if _, ok := appliedSet[migrations[i].ID]; ok {
				steps = append(steps, step{action: actionRollback, migration: migrations[i]})
			}
		}
	}
	// targetIdx == currentIdx => no-op

	return steps, nil
}

// Run discovers the required migration direction, validates file integrity,
// and executes each step via the provided store.
func Run(s MigrationStore, migrations []discovery.Migration, target string) error {
	if err := s.Lock(); err != nil {
		return fmt.Errorf("acquiring advisory lock: %w", err)
	}
	defer s.Unlock() //nolint:errcheck

	if err := s.EnsureTable(); err != nil {
		return err
	}

	applied, err := s.Applied()
	if err != nil {
		return fmt.Errorf("fetching applied migrations: %w", err)
	}

	steps, err := plan(migrations, applied, target)
	if err != nil {
		return err
	}

	if len(steps) == 0 {
		fmt.Println("No migrations to apply.")
		return nil
	}

	for _, st := range steps {
		switch st.action {
		case actionApply:
			fmt.Printf("Applying migration %s (%s)...\n", st.migration.ID, st.migration.Name)
			if err := applyMigration(s, st.migration); err != nil {
				return err
			}
		case actionRollback:
			fmt.Printf("Rolling back migration %s (%s)...\n", st.migration.ID, st.migration.Name)
			if err := rollbackMigration(s, st.migration); err != nil {
				return err
			}
		}
	}

	return nil
}

// applyMigration reads the apply file, computes hashes, and delegates to the
// store's transactional Apply method.
func applyMigration(s MigrationStore, m discovery.Migration) error {
	sqlContent, err := os.ReadFile(m.ApplyPath)
	if err != nil {
		return fmt.Errorf("reading apply file %s: %w", m.ApplyPath, err)
	}

	applyHash, err := hashFile(m.ApplyPath)
	if err != nil {
		return err
	}
	rollbackHash, err := hashFile(m.RollbackPath)
	if err != nil {
		return err
	}

	return s.Apply(m.ID, m.Name, string(sqlContent), applyHash, rollbackHash)
}

// rollbackMigration reads the rollback file and delegates to the store's
// transactional Rollback method.
func rollbackMigration(s MigrationStore, m discovery.Migration) error {
	sqlContent, err := os.ReadFile(m.RollbackPath)
	if err != nil {
		return fmt.Errorf("reading rollback file %s: %w", m.RollbackPath, err)
	}

	return s.Rollback(m.ID, string(sqlContent))
}

// hashFile computes the SHA-256 hex digest of the given file's contents.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading file for hashing: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
