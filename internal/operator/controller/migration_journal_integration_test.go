//go:build integration

package controller_test

import (
	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	api "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

var _ = Describe("PostgresMigrationSet durable protocol", func() {
	It("withdraws an uncertain pending launch under the SQL fence and rejects its delayed Job", func() {
		ns, db, dbKey, adminKey := NewDatabase("pending-fence")
		DeferCleanup(func() { _ = K8sClient.Delete(Ctx, ns) })
		WaitForDatabase(dbKey)
		artifact := PushMigrationArtifact("pending-fence", "v1", map[string]string{
			"0001-init-apply.sql": initMigrations, "0001-init-rollback.sql": rollbackMigrations,
			"0002-late-apply.sql":    "INSERT INTO test_table(value) VALUES ('must not execute')",
			"0002-late-rollback.sql": "DELETE FROM test_table WHERE value = 'must not execute'",
		})
		ms := newMigrationSet(ns.Name, "pending", db.Name, "pending", artifact, 1)
		Expect(K8sClient.Create(Ctx, ms)).To(Succeed())
		key := client.ObjectKeyFromObject(ms)
		WaitForMigrationSet(key, api.MigrationSetPhaseReady)
		Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
		completed := *ms.Status.Operation
		var original batchv1.Job
		Expect(K8sClient.Get(Ctx, client.ObjectKey{Namespace: ns.Name, Name: completed.JobRef.Name}, &original)).To(Succeed())
		late := original.DeepCopy()
		lateName := completed.JobRef.Name + "-late"
		late.ObjectMeta = metav1.ObjectMeta{Name: lateName, Namespace: ns.Name, OwnerReferences: original.OwnerReferences, Labels: map[string]string{
			"db-operator.benjamin-wright.github.com/migration-set": ms.Name,
			"db-operator.benjamin-wright.github.com/operation":     lateName,
		}}
		late.Status = batchv1.JobStatus{}
		late.Spec.Selector = nil
		late.Spec.ManualSelector = nil
		late.Spec.Template.ObjectMeta = metav1.ObjectMeta{Labels: late.Labels}
		late.Spec.Template.Spec.Containers[0].Args = []string{"--artifact", original.Spec.Template.Spec.Containers[0].Args[1], "--target", "2"}
		for i := range late.Spec.Template.Spec.Containers[0].Env {
			if late.Spec.Template.Spec.Containers[0].Env[i].Name == "MIGRATION_OPERATION" {
				late.Spec.Template.Spec.Containers[0].Env[i].Value = lateName
			}
		}
		admin, closeAdmin := ConnectToDatabaseNamed(dbKey, adminKey, "pending")
		defer closeAdmin()
		guard, err := admin.Conn(Ctx)
		Expect(err).NotTo(HaveOccurred())
		defer guard.Close()
		_, err = guard.ExecContext(Ctx, "SELECT pg_advisory_lock($1)", int64(0x5f6d6967726174))
		Expect(err).NotTo(HaveOccurred())
		defer guard.ExecContext(Ctx, "SELECT pg_advisory_unlock($1)", int64(0x5f6d6967726174))
		_, err = guard.ExecContext(Ctx, "UPDATE __dbop_control.migration_authority SET operation_name=$1", lateName)
		Expect(err).NotTo(HaveOccurred())
		before := ms.DeepCopy()
		ms.Spec.Paused = true
		Expect(K8sClient.Patch(Ctx, ms, client.MergeFrom(before))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
			before := ms.DeepCopy()
			ms.Status.Operation = &api.MigrationOperation{Generation: ms.Generation, Artifact: artifact, ResolvedArtifact: completed.ResolvedArtifact, TargetRevision: 2, Phase: "Pending", Reason: "AuthorityArming", JobRef: api.MigrationJobReference{Name: lateName}}
			g.Expect(K8sClient.Status().Patch(Ctx, ms, client.MergeFrom(before))).To(Succeed())
		}, Timeout, Interval).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
			g.Expect(ms.Status.Operation.Phase).To(Equal("Pending"))
			var name string
			g.Expect(guard.QueryRowContext(Ctx, "SELECT operation_name FROM __dbop_control.migration_authority").Scan(&name)).To(Succeed())
			g.Expect(name).To(Equal(lateName))
		}, 2*time.Second, Interval).Should(Succeed(), "an executing SQL session must not be disarmed")
		_, err = guard.ExecContext(Ctx, "SELECT pg_advisory_unlock($1)", int64(0x5f6d6967726174))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
			g.Expect(ms.Status.Operation.Reason).To(Equal("OperationWithdrawn"))
			g.Expect(ms.Status.Operation.JobRef.Name).To(Equal(lateName))
			g.Expect(ms.Status.LastSuccessfulOperation.JobRef).To(Equal(completed.JobRef))
		}, Timeout, Interval).Should(Succeed())
		var count int
		Expect(admin.QueryRow("SELECT count(*) FROM __dbop_control.migration_authority").Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		// Model a successful API create whose response arrived after withdrawal.
		Expect(K8sClient.Create(Ctx, late)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, client.ObjectKeyFromObject(late), late)).To(Succeed())
			g.Expect(late.Status.Conditions).To(ContainElement(And(HaveField("Type", batchv1.JobFailed), HaveField("Status", corev1.ConditionTrue))))
		}, Timeout, Interval).Should(Succeed())
		Expect(admin.QueryRow("SELECT count(*) FROM test_table").Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
		before = ms.DeepCopy()
		ms.Spec.Paused = false
		Expect(K8sClient.Patch(Ctx, ms, client.MergeFrom(before))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
			g.Expect(ms.Status.Phase).To(Equal(api.MigrationSetPhaseReady))
			g.Expect(ms.Status.Operation.JobRef.Name).NotTo(Equal(lateName))
			g.Expect(ms.Status.Operation.TargetRevision).To(Equal(int64(1)))
		}, Timeout, Interval).Should(Succeed())
	})

	It("reserves one target and retains exact successful execution after TTL and tag movement", func() {
		ns, db, dbKey, adminKey := NewDatabase("migration-journal")
		DeferCleanup(func() { _ = K8sClient.Delete(Ctx, ns) })
		WaitForDatabase(dbKey)
		maintenance, closeMaintenance := ConnectToDatabase(dbKey, adminKey)
		defer closeMaintenance()
		_, err := maintenance.Exec("CREATE DATABASE journal OWNER __dbop_migrations")
		Expect(err).NotTo(HaveOccurred())
		admin, closeAdmin := ConnectToDatabaseNamed(dbKey, adminKey, "journal")
		defer closeAdmin()
		_, err = admin.Exec("CREATE TABLE retained_legacy (value text); ALTER TABLE retained_legacy OWNER TO __dbop_migrations; GRANT ALL ON SCHEMA public TO __dbop_migrations; GRANT ALL ON retained_legacy TO __dbop_migrations")
		Expect(err).NotTo(HaveOccurred())
		PushMigrationArtifact("journal", "current", map[string]string{"0001-init-apply.sql": initMigrations, "0001-init-rollback.sql": rollbackMigrations})
		ms := newMigrationSet(ns.Name, "journal", db.Name, "journal", MigrationRegistry+"/journal:current", 1)
		ms.Spec.Paused = true
		Expect(K8sClient.Create(Ctx, ms)).To(Succeed())
		key := types.NamespacedName{Namespace: ns.Name, Name: ms.Name}
		conflict := newMigrationSet(ns.Name, "competitor", db.Name, "journal", ms.Spec.Artifact, 1)
		Expect(K8sClient.Create(Ctx, conflict, client.DryRunAll)).NotTo(Succeed())
		Expect(K8sClient.Create(Ctx, conflict)).NotTo(Succeed())
		Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
		before := ms.DeepCopy()
		ms.Spec.Paused = false
		ms.Spec.JobTTL = &metav1.Duration{Duration: time.Second}
		Expect(K8sClient.Patch(Ctx, ms, client.MergeFrom(before))).To(Succeed())
		WaitForMigrationSet(key, api.MigrationSetPhaseReady)
		Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
		Expect(ms.Status.Operation).NotTo(BeNil())
		Expect(ms.Status.Operation.Phase).To(Equal("Succeeded"))
		Expect(ms.Status.Operation.JobRef.UID).NotTo(BeEmpty())
		completed := *ms.Status.Operation
		var legacyAllowed bool
		Expect(admin.QueryRow("SELECT has_table_privilege('__dbop_migrations','retained_legacy','SELECT') OR has_schema_privilege('__dbop_migrations','public','CREATE')").Scan(&legacyAllowed)).To(Succeed())
		Expect(legacyAllowed).To(BeFalse(), "converting a database must revoke the old shared migration identity")
		Eventually(func(g Gomega) {
			var jobs batchv1.JobList
			g.Expect(K8sClient.List(Ctx, &jobs, client.InNamespace(ns.Name), client.MatchingLabels{"db-operator.benjamin-wright.github.com/migration-set": ms.Name})).To(Succeed())
			g.Expect(jobs.Items).To(BeEmpty())
		}, Timeout, Interval).Should(Succeed())
		PushMigrationArtifact("journal", "current", map[string]string{"0001-init-apply.sql": initMigrations, "0001-init-rollback.sql": rollbackMigrations, "0002-add-apply.sql": "ALTER TABLE test_table ADD COLUMN extra text", "0002-add-rollback.sql": "ALTER TABLE test_table DROP COLUMN extra"})
		Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
		before = ms.DeepCopy()
		ms.Annotations = map[string]string{"db-operator.benjamin-wright.github.com/retryAt": "ack-success"}
		ms.Spec.JobTTL = &metav1.Duration{Duration: 2 * time.Second}
		Expect(K8sClient.Patch(Ctx, ms, client.MergeFrom(before))).To(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
			g.Expect(ms.Status.Operation.JobRef).To(Equal(completed.JobRef))
			g.Expect(ms.Status.Operation.ResolvedArtifact).To(Equal(completed.ResolvedArtifact))
		}, 3*time.Second, Interval).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
			g.Expect(ms.Status.LastHandledRetryAt).To(Equal("ack-success"))
			g.Expect(ms.Status.ObservedGeneration).To(Equal(ms.Generation))
		}, Timeout, Interval).Should(Succeed())
		before = ms.DeepCopy()
		ms.Spec.TargetRevision = 2
		Expect(K8sClient.Patch(Ctx, ms, client.MergeFrom(before))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
			g.Expect(ms.Status.Phase).To(Equal(api.MigrationSetPhaseReady))
			g.Expect(ms.Status.Operation.TargetRevision).To(Equal(int64(2)))
			g.Expect(ms.Status.Operation.ResolvedArtifact).NotTo(Equal(completed.ResolvedArtifact))
		}, Timeout, Interval).Should(Succeed())
		before = ms.DeepCopy()
		ms.Spec.TargetRevision = 0
		Expect(K8sClient.Patch(Ctx, ms, client.MergeFrom(before))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
			g.Expect(ms.Status.Phase).To(Equal(api.MigrationSetPhaseReady))
			g.Expect(ms.Status.Operation.TargetRevision).To(Equal(int64(0)))
			g.Expect(ms.Status.LastSuccessfulOperation.TargetRevision).To(Equal(int64(0)))
		}, Timeout, Interval).Should(Succeed())
		Expect(K8sClient.Delete(Ctx, ms)).To(Succeed())
		Eventually(func() bool { return apierrors.IsNotFound(K8sClient.Get(Ctx, key, &api.PostgresMigrationSet{})) }, Timeout, Interval).Should(BeTrue())
		replacement := newMigrationSet(ns.Name, "new-owner", db.Name, "journal", ms.Spec.Artifact, 0)
		replacement.Spec.Paused = true
		Eventually(func() error { return K8sClient.Create(Ctx, replacement) }, 2*time.Minute, Interval).Should(Succeed(), "an orphan target claim must be released after SQL fencing")
		Expect(K8sClient.Delete(Ctx, replacement)).To(Succeed())
	})
	It("imports legacy Ready only after read-only ledger verification", func() {
		ns, db, dbKey, adminKey := NewDatabase("legacy-ledger")
		DeferCleanup(func() { _ = K8sClient.Delete(Ctx, ns) })
		WaitForDatabase(dbKey)
		digest := PushMigrationArtifact("legacy-ledger", "v1", map[string]string{"0001-init-apply.sql": initMigrations, "0001-init-rollback.sql": rollbackMigrations})
		ms := newMigrationSet(ns.Name, "legacy", db.Name, "legacy", digest, 1)
		ms.Spec.JobTTL = &metav1.Duration{Duration: time.Second}
		Expect(K8sClient.Create(Ctx, ms)).To(Succeed())
		key := types.NamespacedName{Namespace: ns.Name, Name: ms.Name}
		WaitForMigrationSet(key, api.MigrationSetPhaseReady)
		Eventually(func(g Gomega) {
			var jobs batchv1.JobList
			g.Expect(K8sClient.List(Ctx, &jobs, client.InNamespace(ns.Name))).To(Succeed())
			g.Expect(jobs.Items).To(BeEmpty())
		}, Timeout, Interval).Should(Succeed())
		Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
		before := ms.DeepCopy()
		ms.Status.Operation = nil
		ms.Status.LastSuccessfulOperation = nil
		ms.Status.ActiveJob = "expired-legacy-job"
		Expect(K8sClient.Status().Patch(Ctx, ms, client.MergeFrom(before))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
			g.Expect(ms.Status.Operation).NotTo(BeNil())
			g.Expect(ms.Status.Operation.VerificationOnly).To(BeTrue())
			g.Expect(ms.Status.Operation.Phase).To(Equal("Succeeded"))
			g.Expect(ms.Status.Phase).To(Equal(api.MigrationSetPhaseReady))
		}, Timeout, Interval).Should(Succeed())
		admin, closeAdmin := ConnectToDatabaseNamed(dbKey, adminKey, "legacy")
		defer closeAdmin()
		_, err := admin.Exec("DELETE FROM _migrations")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			var jobs batchv1.JobList
			g.Expect(K8sClient.List(Ctx, &jobs, client.InNamespace(ns.Name))).To(Succeed())
			g.Expect(jobs.Items).To(BeEmpty())
		}, Timeout, Interval).Should(Succeed())
		Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
		before = ms.DeepCopy()
		ms.Status.Operation = nil
		ms.Status.LastSuccessfulOperation = nil
		ms.Status.ActiveJob = "expired-legacy-job"
		Expect(K8sClient.Status().Patch(Ctx, ms, client.MergeFrom(before))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(K8sClient.Get(Ctx, key, ms)).To(Succeed())
			g.Expect(ms.Status.Operation).NotTo(BeNil())
			g.Expect(ms.Status.Operation.VerificationOnly).To(BeTrue())
			g.Expect(ms.Status.Operation.Phase).To(Equal("Unknown"))
		}, Timeout, Interval).Should(Succeed())
		var count int
		Expect(admin.QueryRow("SELECT count(*) FROM _migrations").Scan(&count)).To(Succeed())
		Expect(count).To(Equal(0))
	})

})
