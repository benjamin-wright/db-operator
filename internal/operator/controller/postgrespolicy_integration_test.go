//go:build integration

package controller_test

import (
	"database/sql"
	"time"

	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("PostgresCredentialReconciler effective policy", func() {
	It("revokes narrowed tables, writes, defaults, sequences and retained sessions before reporting readiness", func() {
		ns, pgdb, lookup, adminLookup := NewDatabase("effective-policy")
		DeferCleanup(func() { _ = K8sClient.Delete(Ctx, ns) })
		WaitForDatabase(lookup)
		maintenance, closeMaintenance := ConnectToDatabase(lookup, adminLookup)
		defer closeMaintenance()
		_, err := maintenance.Exec("CREATE DATABASE policydb")
		Expect(err).NotTo(HaveOccurred())
		admin, closeAdmin := ConnectToDatabaseNamed(lookup, adminLookup, "policydb")
		defer closeAdmin()
		_, err = admin.Exec("CREATE TABLE visible (id SERIAL PRIMARY KEY, value text); CREATE TABLE hidden (id SERIAL PRIMARY KEY, value text); CREATE SCHEMA public_work; GRANT ALL ON SCHEMA public_work TO PUBLIC; CREATE TABLE column_only (secret text); GRANT SELECT (secret) ON column_only TO PUBLIC; GRANT ALL ON hidden TO PUBLIC; GRANT ALL ON SEQUENCE visible_id_seq TO PUBLIC; ALTER DEFAULT PRIVILEGES GRANT ALL ON TABLES TO PUBLIC; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO PUBLIC; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO PUBLIC; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON FUNCTIONS TO PUBLIC; CREATE FUNCTION hidden_count() RETURNS bigint LANGUAGE sql SECURITY DEFINER AS 'SELECT count(*) FROM hidden'")
		Expect(err).NotTo(HaveOccurred())
		cred := &v1alpha1.PostgresCredential{ObjectMeta: metav1.ObjectMeta{Name: "policy-user", Namespace: ns.Name, Labels: map[string]string{"db-operator.benjamin-wright.github.com/operator-instance": "test"}}, Spec: v1alpha1.PostgresCredentialSpec{DatabaseRef: pgdb.Name, Username: "policy_user", SecretName: "policy-user-secret", Permissions: []v1alpha1.DatabasePermissionEntry{{Databases: []string{"policydb"}, Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionAll}}}}}
		Expect(K8sClient.Create(Ctx, cred)).To(Succeed())
		key := types.NamespacedName{Namespace: ns.Name, Name: cred.Name}
		waitPolicy := func() {
			Eventually(func(g Gomega) {
				g.Expect(K8sClient.Get(Ctx, key, cred)).To(Succeed())
				g.Expect(cred.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
				condition := meta.FindStatusCondition(cred.Status.Conditions, "PolicyEnforced")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(condition.ObservedGeneration).To(Equal(cred.Generation))
			}, Timeout, Interval).Should(Succeed())
		}
		waitPolicy()
		user, closeUser := ConnectToDatabaseNamed(lookup, types.NamespacedName{Namespace: ns.Name, Name: cred.Spec.SecretName}, "policydb")
		defer closeUser()
		held, err := user.Conn(Ctx)
		Expect(err).NotTo(HaveOccurred())
		defer held.Close()
		var pid int
		Expect(held.QueryRowContext(Ctx, "SELECT pg_backend_pid()").Scan(&pid)).To(Succeed())
		_, err = held.ExecContext(Ctx, "INSERT INTO visible(value) VALUES ('before')")
		Expect(err).NotTo(HaveOccurred())
		cred.Spec.Permissions = []v1alpha1.DatabasePermissionEntry{{Databases: []string{"policydb"}, Tables: []string{"visible"}, Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect}}}
		Expect(K8sClient.Update(Ctx, cred)).To(Succeed())
		waitPolicy()
		Expect(held.PingContext(Ctx)).NotTo(Succeed())
		assertAllowed := func(query string, allowed bool) {
			result, err := user.Exec(query)
			_ = result
			if allowed {
				Expect(err).NotTo(HaveOccurred(), query)
			} else {
				Expect(err).To(HaveOccurred(), query)
			}
		}
		assertAllowed("SELECT * FROM visible", true)
		assertAllowed("SELECT * FROM hidden", false)
		assertAllowed("SELECT secret FROM column_only", false)
		assertAllowed("CREATE TABLE public_work.unauthorized (value text)", false)
		assertAllowed("SELECT hidden_count()", false)
		assertAllowed("INSERT INTO visible(value) VALUES ('after')", false)
		assertAllowed("SELECT nextval('visible_id_seq')", false)
		assertAllowed("SELECT setval('visible_id_seq', 100)", false)
		_, err = admin.Exec("CREATE TABLE future (id SERIAL PRIMARY KEY); CREATE FUNCTION future_count() RETURNS bigint LANGUAGE sql SECURITY DEFINER AS 'SELECT count(*) FROM hidden'")
		Expect(err).NotTo(HaveOccurred())
		assertAllowed("SELECT * FROM future", false)
		assertAllowed("SELECT future_count()", false)
		assertAllowed("SELECT nextval('future_id_seq')", false)

		// Replayed status must not make A-to-B-to-A skip revoking B's grants.
		narrowCondition := *meta.FindStatusCondition(cred.Status.Conditions, "PolicyEnforced")
		cred.Spec.Permissions = []v1alpha1.DatabasePermissionEntry{{Databases: []string{"policydb"}, Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionAll}}}
		Expect(K8sClient.Update(Ctx, cred)).To(Succeed())
		waitPolicy()
		// Earlier policy changes deliberately killed connections in the first
		// pool. Start a fresh pool before retaining a live broad-policy session.
		broadUser, closeBroadUser := ConnectToDatabaseNamed(lookup, types.NamespacedName{Namespace: ns.Name, Name: cred.Spec.SecretName}, "policydb")
		defer closeBroadUser()
		broadSession, err := broadUser.Conn(Ctx)
		Expect(err).NotTo(HaveOccurred())
		defer broadSession.Close()
		_, err = broadSession.ExecContext(Ctx, "UPDATE visible SET value = value")
		Expect(err).NotTo(HaveOccurred())
		meta.SetStatusCondition(&cred.Status.Conditions, narrowCondition)
		Expect(K8sClient.Status().Update(Ctx, cred)).To(Succeed())
		cred.Spec.Permissions = []v1alpha1.DatabasePermissionEntry{{Databases: []string{"policydb"}, Tables: []string{"visible"}, Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect}}}
		Expect(K8sClient.Update(Ctx, cred)).To(Succeed())
		waitPolicy()
		Expect(broadSession.PingContext(Ctx)).NotTo(Succeed())
		assertAllowed("UPDATE visible SET value = value", false)
		assertAllowed("SELECT * FROM hidden", false)
		var count int
		Expect(admin.QueryRow("SELECT count(*) FROM visible").Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))

		deletionSession, err := user.Conn(Ctx)
		Expect(err).NotTo(HaveOccurred())
		defer deletionSession.Close()
		Expect(K8sClient.Delete(Ctx, cred)).To(Succeed())
		Eventually(func() bool { return apierrors.IsNotFound(K8sClient.Get(Ctx, key, &v1alpha1.PostgresCredential{})) }, time.Minute, Interval).Should(BeTrue())
		Expect(deletionSession.PingContext(Ctx)).NotTo(Succeed())
		var role sql.NullString
		Expect(admin.QueryRow("SELECT (SELECT rolname FROM pg_roles WHERE rolname = 'policy_user')").Scan(&role)).To(Succeed())
		Expect(role.Valid).To(BeFalse())
		Expect(admin.QueryRow("SELECT count(*) FROM visible").Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
	})
})
