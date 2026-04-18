package runner

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/benjamin-wright/db-operator/internal/migrations/discovery"
	"github.com/benjamin-wright/db-operator/internal/migrations/store"
)

// call records a single Apply or Rollback invocation.
type call struct {
	op         string // "apply" or "rollback"
	id         string
	name       string
	sqlContent string
}

// fakeStore implements MigrationStore for unit testing.
type fakeStore struct {
	records []store.Record
	calls   []call
}

func (f *fakeStore) EnsureTable() error { return nil }
func (f *fakeStore) Lock() error        { return nil }
func (f *fakeStore) Unlock() error      { return nil }

func (f *fakeStore) Applied() ([]store.Record, error) {
	return f.records, nil
}

func (f *fakeStore) Apply(id, name, sqlContent, applyHash, rollbackHash string) error {
	f.calls = append(f.calls, call{op: "apply", id: id, name: name, sqlContent: sqlContent})
	f.records = append(f.records, store.Record{
		ID:           id,
		Name:         name,
		ApplyHash:    applyHash,
		RollbackHash: rollbackHash,
	})
	return nil
}

func (f *fakeStore) Rollback(id, sqlContent string) error {
	f.calls = append(f.calls, call{op: "rollback", id: id, sqlContent: sqlContent})
	var updated []store.Record
	for _, r := range f.records {
		if r.ID != id {
			updated = append(updated, r)
		}
	}
	f.records = updated
	return nil
}

// setupMigrationFiles creates migration files on disk and returns the migrations slice.
func setupMigrationFiles(t *testing.T, ids, names []string) []discovery.Migration {
	t.Helper()
	dir := t.TempDir()
	migrations := make([]discovery.Migration, len(ids))
	for i, id := range ids {
		name := names[i]
		applyFile := filepath.Join(dir, id+"-"+name+"-apply.sql")
		rollbackFile := filepath.Join(dir, id+"-"+name+"-rollback.sql")
		Expect(os.WriteFile(applyFile, []byte("-- apply "+id), 0644)).To(Succeed())
		Expect(os.WriteFile(rollbackFile, []byte("-- rollback "+id), 0644)).To(Succeed())
		migrations[i] = discovery.Migration{
			ID:           id,
			Name:         name,
			ApplyPath:    applyFile,
			RollbackPath: rollbackFile,
		}
	}
	return migrations
}

// hashTestFile computes the hash of a test file for setting up applied records.
func hashTestFile(t *testing.T, path string) string {
	t.Helper()
	h, err := hashFile(path)
	Expect(err).NotTo(HaveOccurred())
	return h
}

// appliedRecords builds store.Record entries for the given migrations (all hashes valid).
func appliedRecords(t *testing.T, migrations []discovery.Migration) []store.Record {
	t.Helper()
	records := make([]store.Record, len(migrations))
	for i, m := range migrations {
		records[i] = store.Record{
			ID:           m.ID,
			Name:         m.Name,
			ApplyHash:    hashTestFile(t, m.ApplyPath),
			RollbackHash: hashTestFile(t, m.RollbackPath),
		}
	}
	return records
}

func TestRun_ApplyAll_NoneApplied(t *testing.T) {
	RegisterTestingT(t)

	migrations := setupMigrationFiles(t,
		[]string{"001", "002", "003"},
		[]string{"first", "second", "third"},
	)

	fs := &fakeStore{}
	Expect(Run(fs, migrations, "")).To(Succeed())
	Expect(fs.calls).To(HaveLen(3))

	for i, c := range fs.calls {
		Expect(c.op).To(Equal("apply"))
		Expect(c.id).To(Equal(migrations[i].ID))
		Expect(c.sqlContent).To(Equal("-- apply " + migrations[i].ID))
	}
}

func TestRun_NoOp_AllApplied(t *testing.T) {
	RegisterTestingT(t)

	migrations := setupMigrationFiles(t, []string{"001", "002"}, []string{"first", "second"})
	fs := &fakeStore{records: appliedRecords(t, migrations)}

	Expect(Run(fs, migrations, "")).To(Succeed())
	Expect(fs.calls).To(BeEmpty())
}

func TestRun_ApplyToTarget(t *testing.T) {
	RegisterTestingT(t)

	migrations := setupMigrationFiles(t,
		[]string{"001", "002", "003"},
		[]string{"first", "second", "third"},
	)
	fs := &fakeStore{records: appliedRecords(t, migrations[:1])}

	Expect(Run(fs, migrations, "002")).To(Succeed())
	Expect(fs.calls).To(HaveLen(1))
	Expect(fs.calls[0].op).To(Equal("apply"))
	Expect(fs.calls[0].id).To(Equal("002"))
}

func TestRun_RollbackToTarget(t *testing.T) {
	RegisterTestingT(t)

	migrations := setupMigrationFiles(t,
		[]string{"001", "002", "003"},
		[]string{"first", "second", "third"},
	)
	fs := &fakeStore{records: appliedRecords(t, migrations)}

	Expect(Run(fs, migrations, "001")).To(Succeed())
	Expect(fs.calls).To(HaveLen(2))

	// Reverse order: 003 then 002
	Expect(fs.calls[0].op).To(Equal("rollback"))
	Expect(fs.calls[0].id).To(Equal("003"))
	Expect(fs.calls[0].sqlContent).To(Equal("-- rollback 003"))

	Expect(fs.calls[1].op).To(Equal("rollback"))
	Expect(fs.calls[1].id).To(Equal("002"))
}

func TestRun_NoOpWhenAtTarget(t *testing.T) {
	RegisterTestingT(t)

	migrations := setupMigrationFiles(t, []string{"001", "002"}, []string{"first", "second"})
	fs := &fakeStore{records: appliedRecords(t, migrations)}

	Expect(Run(fs, migrations, "002")).To(Succeed())
	Expect(fs.calls).To(BeEmpty())
}

func TestRun_HashMismatch(t *testing.T) {
	RegisterTestingT(t)

	migrations := setupMigrationFiles(t, []string{"001"}, []string{"first"})
	fs := &fakeStore{
		records: []store.Record{
			{ID: "001", Name: "first", ApplyHash: "wronghash", RollbackHash: hashTestFile(t, migrations[0].RollbackPath)},
		},
	}

	Expect(Run(fs, migrations, "")).To(MatchError(ContainSubstring("integrity error")))
	Expect(fs.calls).To(BeEmpty())
}

func TestRun_RollbackHashMismatch(t *testing.T) {
	RegisterTestingT(t)

	migrations := setupMigrationFiles(t, []string{"001"}, []string{"first"})
	fs := &fakeStore{
		records: []store.Record{
			{ID: "001", Name: "first", ApplyHash: hashTestFile(t, migrations[0].ApplyPath), RollbackHash: "wronghash"},
		},
	}

	Expect(Run(fs, migrations, "")).To(MatchError(ContainSubstring("integrity error")))
}

func TestRun_TargetNotFound(t *testing.T) {
	RegisterTestingT(t)

	migrations := setupMigrationFiles(t, []string{"001"}, []string{"first"})
	fs := &fakeStore{}

	Expect(Run(fs, migrations, "999")).To(MatchError(ContainSubstring("not found")))
}
