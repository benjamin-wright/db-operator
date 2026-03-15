//go:build integration

package store

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	_ "github.com/lib/pq"
	. "github.com/onsi/gomega"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()

	host := envOrDefault("PGHOST", "localhost")
	port := envOrDefault("PGPORT", "5432")
	user := envOrDefault("PGUSER", "postgres")
	pass := envOrDefault("PGPASSWORD", "postgres")
	name := envOrDefault("PGDATABASE", "postgres")

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", host, port, user, pass, name)
	db, err := sql.Open("postgres", dsn)
	Expect(err).NotTo(HaveOccurred())
	Expect(db.Ping()).To(Succeed())

	// Clean up before each test
	_, _ = db.Exec("DROP TABLE IF EXISTS _migrations")
	_, _ = db.Exec("DROP TABLE IF EXISTS test_table")

	t.Cleanup(func() {
		_, _ = db.Exec("DROP TABLE IF EXISTS _migrations")
		_, _ = db.Exec("DROP TABLE IF EXISTS test_table")
		db.Close()
	})

	return db
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func tableExists(db *sql.DB, name string) bool {
	var exists bool
	err := db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
		name,
	).Scan(&exists)
	Expect(err).NotTo(HaveOccurred())
	return exists
}

func TestEnsureTable(t *testing.T) {
	RegisterTestingT(t)

	s := New(testDB(t))
	Expect(s.EnsureTable()).To(Succeed())
	// Idempotent
	Expect(s.EnsureTable()).To(Succeed())
}

func TestApplyAndApplied(t *testing.T) {
	RegisterTestingT(t)

	db := testDB(t)
	s := New(db)
	Expect(s.EnsureTable()).To(Succeed())

	Expect(s.Apply("001", "create-test", "CREATE TABLE test_table (id INT)", "abc123", "def456")).To(Succeed())

	records, err := s.Applied()
	Expect(err).NotTo(HaveOccurred())
	Expect(records).To(HaveLen(1))

	Expect(records[0].ID).To(Equal("001"))
	Expect(records[0].Name).To(Equal("create-test"))
	Expect(records[0].ApplyHash).To(Equal("abc123"))
	Expect(records[0].RollbackHash).To(Equal("def456"))

	Expect(tableExists(db, "test_table")).To(BeTrue(), "expected test_table to exist after Apply")
}

func TestRollback(t *testing.T) {
	RegisterTestingT(t)

	db := testDB(t)
	s := New(db)
	Expect(s.EnsureTable()).To(Succeed())

	Expect(s.Apply("001", "create-test", "CREATE TABLE test_table (id INT)", "abc123", "def456")).To(Succeed())
	Expect(s.Rollback("001", "DROP TABLE test_table")).To(Succeed())

	records, err := s.Applied()
	Expect(err).NotTo(HaveOccurred())
	Expect(records).To(BeEmpty())

	Expect(tableExists(db, "test_table")).To(BeFalse(), "expected test_table to not exist after Rollback")
}

func TestApplyRollsBackOnBadSQL(t *testing.T) {
	RegisterTestingT(t)

	s := New(testDB(t))
	Expect(s.EnsureTable()).To(Succeed())

	Expect(s.Apply("001", "bad-migration", "INVALID SQL HERE", "abc", "def")).To(HaveOccurred())

	records, err := s.Applied()
	Expect(err).NotTo(HaveOccurred())
	Expect(records).To(BeEmpty(), "expected no records after failed Apply")
}
