//go:build integration

package store_test

import (
	"database/sql"

	"github.com/benjamin-wright/db-operator/internal/migrations/store"
	_ "github.com/lib/pq"

	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Store", func() {
	Describe("migrations tracking", Ordered, func() {
		var (
			ns                *corev1.Namespace
			dbLookup          types.NamespacedName
			adminSecretLookup types.NamespacedName
			db                *sql.DB
			closeDB           func()
		)

		BeforeAll(func() {
			ns, _, dbLookup, adminSecretLookup = NewDatabase("migration-test-db")
			WaitForDatabase(dbLookup)
			db, closeDB = ConnectToDatabase(dbLookup, adminSecretLookup)
		})

		AfterAll(func() {
			closeDB()
			_ = K8sClient.Delete(Ctx, ns)
		})

		BeforeEach(func() {
			for _, stmt := range []string{
				"DROP TABLE IF EXISTS _migrations",
				"DROP TABLE IF EXISTS test_table",
			} {
				_, err := db.Exec(stmt)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("keeps all migration work on the lock-owning backend and rejects revoked authority", func() {
			_, err := db.Exec("CREATE SCHEMA IF NOT EXISTS __dbop_control; CREATE TABLE IF NOT EXISTS __dbop_control.migration_authority(singleton BOOLEAN PRIMARY KEY,claim_uid TEXT,operation_name TEXT); INSERT INTO __dbop_control.migration_authority VALUES(TRUE,'claim-one','operation-one') ON CONFLICT(singleton) DO UPDATE SET claim_uid=EXCLUDED.claim_uid")
			Expect(err).NotTo(HaveOccurred())
			s := store.New(db)
			s.RequireAuthority("claim-one")
			s.RequireOperation("operation-one")
			Expect(s.Lock()).To(Succeed())
			defer func() { _ = s.Unlock() }()
			Expect(s.EnsureTable()).To(Succeed())
			Expect(s.Apply("001", "session", "CREATE TEMP TABLE pinned_session AS SELECT pg_backend_pid() AS pid", "a", "b")).To(Succeed())
			Expect(s.Apply("002", "same-session", "DO $$ BEGIN IF (SELECT pid FROM pinned_session) <> pg_backend_pid() THEN RAISE EXCEPTION 'session changed'; END IF; END $$", "c", "d")).To(Succeed())
			Expect(s.Unlock()).To(Succeed())
			revoked := store.New(db)
			superseded := store.New(db)
			superseded.RequireAuthority("claim-one")
			superseded.RequireOperation("operation-old")
			Expect(superseded.Lock()).NotTo(Succeed())
			revoked.RequireAuthority("claim-other")
			Expect(revoked.Lock()).NotTo(Succeed())
		})

		It("should create and idempotently ensure the migrations table", func() {
			s := store.New(db)
			Expect(s.EnsureTable()).To(Succeed())
			Expect(s.EnsureTable()).To(Succeed())
		})

		It("should apply a migration and record it", func() {
			s := store.New(db)
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
		})

		It("should rollback a migration and remove the record", func() {
			s := store.New(db)
			Expect(s.EnsureTable()).To(Succeed())

			Expect(s.Apply("001", "create-test", "CREATE TABLE test_table (id INT)", "abc123", "def456")).To(Succeed())
			Expect(s.Rollback("001", "DROP TABLE test_table")).To(Succeed())

			records, err := s.Applied()
			Expect(err).NotTo(HaveOccurred())
			Expect(records).To(BeEmpty())

			Expect(tableExists(db, "test_table")).To(BeFalse(), "expected test_table to not exist after Rollback")
		})

		It("should roll back the transaction when Apply receives bad SQL", func() {
			s := store.New(db)
			Expect(s.EnsureTable()).To(Succeed())

			err := s.Apply("001", "bad-migration", "INVALID SQL HERE", "abc", "def")
			Expect(err).To(HaveOccurred())

			records, err := s.Applied()
			Expect(err).NotTo(HaveOccurred())
			Expect(records).To(BeEmpty(), "expected no records after failed Apply")
		})
	})
})

func tableExists(db *sql.DB, name string) bool {
	var exists bool
	err := db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
		name,
	).Scan(&exists)
	Expect(err).NotTo(HaveOccurred())
	return exists
}
