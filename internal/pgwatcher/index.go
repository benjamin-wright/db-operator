package pgwatcher

import "sync"

// ClusterKey identifies a PostgresDatabase by namespace and name.
type ClusterKey struct {
	Namespace string
	Name      string
}

// ClusterInfo holds the connection details and database list for a PostgresDatabase.
type ClusterInfo struct {
	Namespace string
	Name      string
	Host      string
	Port      string
	User      string
	Password  string
	Databases []string
	// Ready is true once the operator-produced credential Secret is present and populated.
	Ready bool
}

// Index is a concurrency-safe in-memory store of discovered clusters.
type Index struct {
	mu      sync.RWMutex
	entries map[ClusterKey]*ClusterInfo
}

// NewIndex creates an empty Index.
func NewIndex() *Index {
	return &Index{entries: make(map[ClusterKey]*ClusterInfo)}
}

// Set inserts or replaces the entry for key.
func (idx *Index) Set(key ClusterKey, info ClusterInfo) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	copied := info
	idx.entries[key] = &copied
}

// Delete removes the entry for key.
func (idx *Index) Delete(key ClusterKey) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.entries, key)
}

// Get returns the entry for key and whether it exists.
func (idx *Index) Get(key ClusterKey) (ClusterInfo, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	e, ok := idx.entries[key]
	if !ok {
		return ClusterInfo{}, false
	}
	return *e, true
}

// List returns all entries currently in the index.
func (idx *Index) List() []ClusterInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	result := make([]ClusterInfo, 0, len(idx.entries))
	for _, e := range idx.entries {
		result = append(result, *e)
	}
	return result
}
