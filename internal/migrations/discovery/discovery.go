package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Migration represents a discovered migration pair (apply + rollback SQL files).
type Migration struct {
	ID           string
	Name         string
	ApplyPath    string
	RollbackPath string
}

// filenamePattern matches migration filenames: <id>-<name>-apply.sql or <id>-<name>-rollback.sql
var filenamePattern = regexp.MustCompile(`^(\d+)-(.+)-(apply|rollback)\.sql$`)

// Discover reads the given directory and returns all valid migration pairs
// sorted by ID. It returns an error if any file is malformed or if any
// migration is missing its apply or rollback counterpart.
func Discover(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading migrations directory: %w", err)
	}

	type pair struct {
		name         string
		applyPath    string
		rollbackPath string
	}

	pairs := make(map[string]*pair)
	var ids []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fname := entry.Name()
		matches := filenamePattern.FindStringSubmatch(fname)
		if matches == nil {
			return nil, fmt.Errorf("malformed migration filename: %s (expected <id>-<name>-apply.sql or <id>-<name>-rollback.sql)", fname)
		}

		id := matches[1]
		name := matches[2]
		kind := matches[3]
		fullPath := filepath.Join(dir, fname)

		p, exists := pairs[id]
		if !exists {
			p = &pair{name: name}
			pairs[id] = p
			ids = append(ids, id)
		} else if p.name != name {
			return nil, fmt.Errorf("migration ID %s has inconsistent names: %q and %q", id, p.name, name)
		}

		switch kind {
		case "apply":
			if p.applyPath != "" {
				return nil, fmt.Errorf("duplicate apply file for migration ID %s", id)
			}
			p.applyPath = fullPath
		case "rollback":
			if p.rollbackPath != "" {
				return nil, fmt.Errorf("duplicate rollback file for migration ID %s", id)
			}
			p.rollbackPath = fullPath
		}
	}

	// Sort IDs numerically by zero-padded string comparison, which works for
	// same-length numeric IDs. For mixed lengths we sort by length first (shorter
	// numeric strings are smaller), then lexicographically.
	sort.Slice(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		return strings.Compare(a, b) < 0
	})

	migrations := make([]Migration, 0, len(ids))
	for _, id := range ids {
		p := pairs[id]
		if p.applyPath == "" {
			return nil, fmt.Errorf("migration ID %s (%s) is missing an apply file", id, p.name)
		}
		if p.rollbackPath == "" {
			return nil, fmt.Errorf("migration ID %s (%s) is missing a rollback file", id, p.name)
		}
		migrations = append(migrations, Migration{
			ID:           id,
			Name:         p.name,
			ApplyPath:    p.applyPath,
			RollbackPath: p.rollbackPath,
		})
	}

	return migrations, nil
}
