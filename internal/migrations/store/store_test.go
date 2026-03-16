//go:build integration

package store_test

// import (
// 	"database/sql"
// 	"fmt"
// 	"testing"

// 	"github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
// 	_ "github.com/lib/pq"

// 	. "github.com/benjamin-wright/db-operator/internal/test_utils"
// 	. "github.com/onsi/ginkgo/v2"
// 	. "github.com/onsi/gomega"
// 	corev1 "k8s.io/api/core/v1"
// 	"k8s.io/apimachinery/pkg/types"
// )

// var _ = Describe("Store", func() {
// 	Describe("migrations tracking", Ordered, func() {
// 		var (
// 			ns       *corev1.Namespace
// 			pgdb     *v1alpha1.PostgresDatabase
// 			dbLookup types.NamespacedName
// 		)

// 		BeforeAll(func() {
// 			ns, pgdb, dbLookup, _ = NewDatabase("migration-test-db")
// 			WaitForDatabase(dbLookup)
// 		})

// 		AfterAll(func() {
// 			_ = K8sClient.Delete(Ctx, ns)
// 		})

// 		BeforeEach(func() {
// 			Eventually(func(g Gomega) {
// 				podName := fmt.Sprintf("%s-0", pgdb.Name)
// 				for _, cmd := range []string{
// 					"DROP TABLE IF EXISTS _migrations",
// 					"DROP TABLE IF EXISTS test_table",
// 				} {
// 					stdout, stderr, err := PodExec(ns.Name, podName, []string{
// 						"psql", "-U", "postgres", "-d", dbLookup.Name, cmd,
// 					})

// 					Expect(err).NotTo(HaveOccurred(), "psql query failed: stdout=%s stderr=%s", stdout, stderr)
// 					Expect(stdout).To(ContainSubstring("f"), "Postgres role 'deleteuser' should have been dropped")
// 				}
// 			}, Timeout, Interval).Should(Succeed())
// 		})
// 	})
// })

// func tableExists(db *sql.DB, name string) bool {
// 	var exists bool
// 	err := db.QueryRow(
// 		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
// 		name,
// 	).Scan(&exists)
// 	Expect(err).NotTo(HaveOccurred())
// 	return exists
// }

// func TestEnsureTable(t *testing.T) {
// 	RegisterTestingT(t)

// 	s := New(testDB(t))
// 	Expect(s.EnsureTable()).To(Succeed())
// 	// Idempotent
// 	Expect(s.EnsureTable()).To(Succeed())
// }

// func TestApplyAndApplied(t *testing.T) {
// 	RegisterTestingT(t)

// 	db := testDB(t)
// 	s := New(db)
// 	Expect(s.EnsureTable()).To(Succeed())

// 	Expect(s.Apply("001", "create-test", "CREATE TABLE test_table (id INT)", "abc123", "def456")).To(Succeed())

// 	records, err := s.Applied()
// 	Expect(err).NotTo(HaveOccurred())
// 	Expect(records).To(HaveLen(1))

// 	Expect(records[0].ID).To(Equal("001"))
// 	Expect(records[0].Name).To(Equal("create-test"))
// 	Expect(records[0].ApplyHash).To(Equal("abc123"))
// 	Expect(records[0].RollbackHash).To(Equal("def456"))

// 	Expect(tableExists(db, "test_table")).To(BeTrue(), "expected test_table to exist after Apply")
// }

// func TestRollback(t *testing.T) {
// 	RegisterTestingT(t)

// 	db := testDB(t)
// 	s := New(db)
// 	Expect(s.EnsureTable()).To(Succeed())

// 	Expect(s.Apply("001", "create-test", "CREATE TABLE test_table (id INT)", "abc123", "def456")).To(Succeed())
// 	Expect(s.Rollback("001", "DROP TABLE test_table")).To(Succeed())

// 	records, err := s.Applied()
// 	Expect(err).NotTo(HaveOccurred())
// 	Expect(records).To(BeEmpty())

// 	Expect(tableExists(db, "test_table")).To(BeFalse(), "expected test_table to not exist after Rollback")
// }

// func TestApplyRollsBackOnBadSQL(t *testing.T) {
// 	RegisterTestingT(t)

// 	s := New(testDB(t))
// 	Expect(s.EnsureTable()).To(Succeed())

// 	Expect(s.Apply("001", "bad-migration", "INVALID SQL HERE", "abc", "def")).To(HaveOccurred())

// 	records, err := s.Applied()
// 	Expect(err).NotTo(HaveOccurred())
// 	Expect(records).To(BeEmpty(), "expected no records after failed Apply")
// }
