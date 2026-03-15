package discovery

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

// setupDir creates the listed files in a temp directory and returns its path.
func setupDir(t *testing.T, files []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		err := os.WriteFile(filepath.Join(dir, f), []byte("-- sql"), 0644)
		Expect(err).NotTo(HaveOccurred())
	}
	return dir
}

func TestDiscover_CorrectParsing(t *testing.T) {
	RegisterTestingT(t)

	dir := setupDir(t, []string{
		"001-create-users-apply.sql",
		"001-create-users-rollback.sql",
		"002-add-email-apply.sql",
		"002-add-email-rollback.sql",
	})

	migrations, err := Discover(dir)
	Expect(err).NotTo(HaveOccurred())
	Expect(migrations).To(HaveLen(2))

	Expect(migrations[0].ID).To(Equal("001"))
	Expect(migrations[0].Name).To(Equal("create-users"))
	Expect(migrations[0].ApplyPath).To(Equal(filepath.Join(dir, "001-create-users-apply.sql")))
	Expect(migrations[0].RollbackPath).To(Equal(filepath.Join(dir, "001-create-users-rollback.sql")))

	Expect(migrations[1].ID).To(Equal("002"))
	Expect(migrations[1].Name).To(Equal("add-email"))
}

func TestDiscover_IDOrdering(t *testing.T) {
	RegisterTestingT(t)

	dir := setupDir(t, []string{
		"003-third-apply.sql", "003-third-rollback.sql",
		"001-first-apply.sql", "001-first-rollback.sql",
		"002-second-apply.sql", "002-second-rollback.sql",
	})

	migrations, err := Discover(dir)
	Expect(err).NotTo(HaveOccurred())
	Expect(migrations).To(HaveLen(3))

	ids := make([]string, len(migrations))
	for i, m := range migrations {
		ids[i] = m.ID
	}
	Expect(ids).To(Equal([]string{"001", "002", "003"}))
}

func TestDiscover_MixedLengthIDs(t *testing.T) {
	RegisterTestingT(t)

	dir := setupDir(t, []string{
		"1-first-apply.sql", "1-first-rollback.sql",
		"2-second-apply.sql", "2-second-rollback.sql",
		"10-tenth-apply.sql", "10-tenth-rollback.sql",
	})

	migrations, err := Discover(dir)
	Expect(err).NotTo(HaveOccurred())

	ids := make([]string, len(migrations))
	for i, m := range migrations {
		ids[i] = m.ID
	}
	Expect(ids).To(Equal([]string{"1", "2", "10"}))
}

func TestDiscover_ErrorOnMissingApply(t *testing.T) {
	RegisterTestingT(t)

	dir := setupDir(t, []string{"001-create-users-rollback.sql"})
	_, err := Discover(dir)
	Expect(err).To(HaveOccurred())
}

func TestDiscover_ErrorOnMissingRollback(t *testing.T) {
	RegisterTestingT(t)

	dir := setupDir(t, []string{"001-create-users-apply.sql"})
	_, err := Discover(dir)
	Expect(err).To(HaveOccurred())
}

func TestDiscover_ErrorOnMalformedFilename(t *testing.T) {
	RegisterTestingT(t)

	dir := setupDir(t, []string{"not-a-migration.sql"})
	_, err := Discover(dir)
	Expect(err).To(HaveOccurred())
}

func TestDiscover_ErrorOnInconsistentNames(t *testing.T) {
	RegisterTestingT(t)

	dir := setupDir(t, []string{
		"001-create-users-apply.sql",
		"001-drop-users-rollback.sql",
	})
	_, err := Discover(dir)
	Expect(err).To(HaveOccurred())
}

func TestDiscover_EmptyDirectory(t *testing.T) {
	RegisterTestingT(t)

	migrations, err := Discover(t.TempDir())
	Expect(err).NotTo(HaveOccurred())
	Expect(migrations).To(BeEmpty())
}

func TestDiscover_IgnoresSubdirectories(t *testing.T) {
	RegisterTestingT(t)

	dir := t.TempDir()
	Expect(os.Mkdir(filepath.Join(dir, "subdir"), 0755)).To(Succeed())

	for _, f := range []string{"001-init-apply.sql", "001-init-rollback.sql"} {
		Expect(os.WriteFile(filepath.Join(dir, f), []byte("-- sql"), 0644)).To(Succeed())
	}

	migrations, err := Discover(dir)
	Expect(err).NotTo(HaveOccurred())
	Expect(migrations).To(HaveLen(1))
}
