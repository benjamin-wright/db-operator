//go:build integration

package controller_test

import (
	"fmt"

	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

// initMigrations is the SQL for the initial migration (revision 1).
const initMigrations = `CREATE TABLE IF NOT EXISTS test_table (id SERIAL PRIMARY KEY, value TEXT);`

// rollbackMigrations is the SQL to undo revision 1.
const rollbackMigrations = `DROP TABLE IF EXISTS test_table;`

// newMigrationSet returns an unsaved PostgresMigrationSet CR in the given namespace.
func newMigrationSet(namespace, name, dbRef, database, artifact string, target int64) *v1alpha1.PostgresMigrationSet {
	return &v1alpha1.PostgresMigrationSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"db-operator.benjamin-wright.github.com/operator-instance": "test",
			},
		},
		Spec: v1alpha1.PostgresMigrationSetSpec{
			DatabaseRef:    dbRef,
			Database:       database,
			Artifact:       artifact,
			TargetRevision: target,
		},
	}
}

var _ = Describe("PostgresMigrationSetReconciler", func() {

	// ── Basic lifecycle: apply → Ready ───────────────────────────────────────
	Context("basic apply lifecycle", Ordered, func() {
		var (
			ns     *corev1.Namespace
			pgdb   *v1alpha1.PostgresDatabase
			pgms   *v1alpha1.PostgresMigrationSet
			dbLook types.NamespacedName
			msLook types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLook, _ = NewDatabase("pgms-basic-db")
			WaitForDatabase(dbLook)

			artifact := PushMigrationArtifact("pgms-basic", "v1", map[string]string{
				"0001-init-apply.sql":    initMigrations,
				"0001-init-rollback.sql": rollbackMigrations,
			})

			pgms = newMigrationSet(ns.Name, "pgms-basic", pgdb.Name, "testdb", artifact, 1)
			Expect(K8sClient.Create(Ctx, pgms)).To(Succeed())
			msLook = types.NamespacedName{Name: pgms.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should transition to Running while the Job executes", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresMigrationSet
				g.Expect(K8sClient.Get(Ctx, msLook, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.MigrationSetPhaseRunning))
			}, Timeout, Interval).Should(Succeed())
		})

		It("should transition to Ready after the Job succeeds", func() {
			WaitForMigrationSet(msLook, v1alpha1.MigrationSetPhaseReady)
		})

		It("should set currentRevision to the targetRevision", func() {
			var fetched v1alpha1.PostgresMigrationSet
			Expect(K8sClient.Get(Ctx, msLook, &fetched)).To(Succeed())
			Expect(fetched.Status.CurrentRevision).NotTo(BeNil())
			Expect(*fetched.Status.CurrentRevision).To(Equal(int64(1)))
		})

		It("should set observedArtifact to a digest-pinned reference", func() {
			var fetched v1alpha1.PostgresMigrationSet
			Expect(K8sClient.Get(Ctx, msLook, &fetched)).To(Succeed())
			Expect(fetched.Status.ObservedArtifact).To(ContainSubstring("@sha256:"))
		})

		It("should create the Job with an owner reference to the migration set", func() {
			var jobList batchv1.JobList
			Expect(K8sClient.List(Ctx, &jobList,
				client.InNamespace(ns.Name),
				client.MatchingLabels{"db-operator.benjamin-wright.github.com/migration-set": pgms.Name},
			)).To(Succeed())
			Expect(jobList.Items).NotTo(BeEmpty())
			Expect(jobList.Items[0].OwnerReferences).To(HaveLen(1))
			Expect(jobList.Items[0].OwnerReferences[0].Name).To(Equal(pgms.Name))
		})
	})

	// ── Bump targetRevision → rollback Job ───────────────────────────────────
	Context("bumping targetRevision triggers a new Job", Ordered, func() {
		var (
			ns     *corev1.Namespace
			pgdb   *v1alpha1.PostgresDatabase
			pgms   *v1alpha1.PostgresMigrationSet
			dbLook types.NamespacedName
			msLook types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLook, _ = NewDatabase("pgms-bump-db")
			WaitForDatabase(dbLook)

			artifact := PushMigrationArtifact("pgms-bump", "v1", map[string]string{
				"0001-init-apply.sql":    initMigrations,
				"0001-init-rollback.sql": rollbackMigrations,
			})

			pgms = newMigrationSet(ns.Name, "pgms-bump", pgdb.Name, "testdb", artifact, 1)
			Expect(K8sClient.Create(Ctx, pgms)).To(Succeed())
			msLook = types.NamespacedName{Name: pgms.Name, Namespace: ns.Name}

			WaitForMigrationSet(msLook, v1alpha1.MigrationSetPhaseReady)
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should run a new Job when targetRevision is rolled back to 0", func() {
			var fetched v1alpha1.PostgresMigrationSet
			Expect(K8sClient.Get(Ctx, msLook, &fetched)).To(Succeed())
			fetched.Spec.TargetRevision = 0
			Expect(K8sClient.Update(Ctx, &fetched)).To(Succeed())

			// The reconciler should create a new Job for the rollback.
			Eventually(func(g Gomega) {
				var jobList batchv1.JobList
				g.Expect(K8sClient.List(Ctx, &jobList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{"db-operator.benjamin-wright.github.com/migration-set": pgms.Name},
				)).To(Succeed())
				g.Expect(jobList.Items).To(HaveLen(2), "expected original Job plus rollback Job")
			}, Timeout, Interval).Should(Succeed())

			WaitForMigrationSet(msLook, v1alpha1.MigrationSetPhaseReady)

			var fetched2 v1alpha1.PostgresMigrationSet
			Expect(K8sClient.Get(Ctx, msLook, &fetched2)).To(Succeed())
			Expect(*fetched2.Status.CurrentRevision).To(Equal(int64(0)))
		})
	})

	// ── Re-push same tag with new digest → new Job ───────────────────────────
	Context("new digest on same tag triggers a new Job", Ordered, func() {
		var (
			ns     *corev1.Namespace
			pgdb   *v1alpha1.PostgresDatabase
			pgms   *v1alpha1.PostgresMigrationSet
			dbLook types.NamespacedName
			msLook types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLook, _ = NewDatabase("pgms-digest-db")
			WaitForDatabase(dbLook)

			artifact := PushMigrationArtifact("pgms-digest", "v1", map[string]string{
				"0001-init-apply.sql":    initMigrations,
				"0001-init-rollback.sql": rollbackMigrations,
			})

			pgms = newMigrationSet(ns.Name, "pgms-digest", pgdb.Name, "testdb", artifact, 1)
			Expect(K8sClient.Create(Ctx, pgms)).To(Succeed())
			msLook = types.NamespacedName{Name: pgms.Name, Namespace: ns.Name}
			WaitForMigrationSet(msLook, v1alpha1.MigrationSetPhaseReady)
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should create a new Job when the same tag is re-pushed with new content", func() {
			// Push a new artifact with the same repo:tag but different digest.
			// We add a new migration (0002) rather than modifying 0001 — modifying
			// an already-applied migration would trigger the runner's integrity check
			// and cause the job to fail. The targetRevision remains 1, so the second
			// job is a no-op (0001 is already applied) but still succeeds → Ready.
			newArtifact := PushMigrationArtifact("pgms-digest", "v1", map[string]string{
				"0001-init-apply.sql":      initMigrations,
				"0001-init-rollback.sql":   rollbackMigrations,
				"0002-update-apply.sql":    "COMMENT ON TABLE test_table IS 'v2';",
				"0002-update-rollback.sql": "COMMENT ON TABLE test_table IS NULL;",
			})

			// Point the migration set at the tag (not the old digest) so the
			// reconciler will re-resolve and find a different digest.
			tagRef := MigrationRegistry + "/pgms-digest:v1"
			var fetched v1alpha1.PostgresMigrationSet
			Expect(K8sClient.Get(Ctx, msLook, &fetched)).To(Succeed())
			fetched.Spec.Artifact = tagRef
			Expect(K8sClient.Update(Ctx, &fetched)).To(Succeed())

			_ = newArtifact // the digest was already pushed to the registry above

			Eventually(func(g Gomega) {
				var jobList batchv1.JobList
				g.Expect(K8sClient.List(Ctx, &jobList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{"db-operator.benjamin-wright.github.com/migration-set": pgms.Name},
				)).To(Succeed())
				g.Expect(jobList.Items).To(HaveLen(2), "expected original Job plus new-digest Job")
			}, Timeout, Interval).Should(Succeed())

			WaitForMigrationSet(msLook, v1alpha1.MigrationSetPhaseReady)
		})
	})

	// ── paused: true short-circuits ──────────────────────────────────────────
	Context("paused migration set", Ordered, func() {
		var (
			ns     *corev1.Namespace
			pgdb   *v1alpha1.PostgresDatabase
			pgms   *v1alpha1.PostgresMigrationSet
			dbLook types.NamespacedName
			msLook types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLook, _ = NewDatabase("pgms-paused-db")
			WaitForDatabase(dbLook)

			artifact := PushMigrationArtifact("pgms-paused", "v1", map[string]string{
				"0001-init-apply.sql":    initMigrations,
				"0001-init-rollback.sql": rollbackMigrations,
			})

			pgms = newMigrationSet(ns.Name, "pgms-paused", pgdb.Name, "testdb", artifact, 1)
			pgms.Spec.Paused = true
			Expect(K8sClient.Create(Ctx, pgms)).To(Succeed())
			msLook = types.NamespacedName{Name: pgms.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should stay Pending with reason Paused and create no Jobs", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresMigrationSet
				g.Expect(K8sClient.Get(Ctx, msLook, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.MigrationSetPhasePending))
				for _, c := range fetched.Status.Conditions {
					if c.Type == "Ready" {
						g.Expect(c.Reason).To(Equal("Paused"))
					}
				}
			}, Timeout, Interval).Should(Succeed())

			var jobList batchv1.JobList
			Expect(K8sClient.List(Ctx, &jobList,
				client.InNamespace(ns.Name),
				client.MatchingLabels{"db-operator.benjamin-wright.github.com/migration-set": pgms.Name},
			)).To(Succeed())
			Expect(jobList.Items).To(BeEmpty(), "paused migration set should not create Jobs")
		})

		It("should create a Job and reach Ready once unpaused", func() {
			var fetched v1alpha1.PostgresMigrationSet
			Expect(K8sClient.Get(Ctx, msLook, &fetched)).To(Succeed())
			fetched.Spec.Paused = false
			Expect(K8sClient.Update(Ctx, &fetched)).To(Succeed())

			WaitForMigrationSet(msLook, v1alpha1.MigrationSetPhaseReady)
		})
	})

	// ── Sibling credential enqueued after migration Ready (Fix C) ────────────
	Context("sibling credential blocked on WaitingForTable becomes Ready post-migration", Ordered, func() {
		var (
			ns       *corev1.Namespace
			pgdb     *v1alpha1.PostgresDatabase
			pgms     *v1alpha1.PostgresMigrationSet
			pgcred   *v1alpha1.PostgresCredential
			dbLook   types.NamespacedName
			msLook   types.NamespacedName
			credLook types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLook, _ = NewDatabase("pgms-fixc-db")
			WaitForDatabase(dbLook)

			// Create the credential BEFORE running the migration.
			// It references a table that the migration will create, so it will
			// initially enter WaitingForTable.
			pgcred = &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pgms-fixc-cred",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "fixcuser",
					SecretName:  "pgms-fixc-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"testdb"},
							Tables:      []string{"test_table"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
						},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, pgcred)).To(Succeed())
			credLook = types.NamespacedName{Name: pgcred.Name, Namespace: ns.Name}

			// Credential should be Pending/WaitingForTable because the table doesn't exist yet.
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, credLook, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhasePending))
			}, Timeout, Interval).Should(Succeed())

			artifact := PushMigrationArtifact("pgms-fixc", "v1", map[string]string{
				"0001-init-apply.sql":    initMigrations,
				"0001-init-rollback.sql": rollbackMigrations,
			})

			pgms = newMigrationSet(ns.Name, "pgms-fixc", pgdb.Name, "testdb", artifact, 1)
			Expect(K8sClient.Create(Ctx, pgms)).To(Succeed())
			msLook = types.NamespacedName{Name: pgms.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("migration set should reach Ready", func() {
			WaitForMigrationSet(msLook, v1alpha1.MigrationSetPhaseReady)
		})

		It("sibling credential should become Ready after migration completes", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, credLook, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady),
					fmt.Sprintf("expected Ready, got %s", fetched.Status.Phase))
			}, Timeout, Interval).Should(Succeed())
		})
	})
})
