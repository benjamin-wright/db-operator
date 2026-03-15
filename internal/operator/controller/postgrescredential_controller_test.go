//go:build integration

package controller_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
)

// newTestDatabaseForCred creates a namespace, a PostgresDatabase CR, and waits
// for it to reach Ready. Returns the namespace, database, and admin secret lookup key.
func newTestDatabaseForCred(name string) (ns *corev1.Namespace, pgdb *v1alpha1.PostgresDatabase, dbLookup, adminSecretLookup types.NamespacedName) {
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-pgcred-",
		},
	}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())

	pgdb = &v1alpha1.PostgresDatabase{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns.Name,
			Labels: map[string]string{
				"games-hub.io/operator-instance": "test",
			},
		},
		Spec: v1alpha1.PostgresDatabaseSpec{
			DatabaseName:    "testdb",
			PostgresVersion: "16",
			StorageSize:     resource.MustParse("256Mi"),
		},
	}
	Expect(k8sClient.Create(ctx, pgdb)).To(Succeed())
	dbLookup = types.NamespacedName{Name: pgdb.Name, Namespace: ns.Name}
	adminSecretLookup = types.NamespacedName{Name: pgdb.Name + "-admin", Namespace: ns.Name}
	return
}

// waitForDatabaseReady polls until the PostgresDatabase reaches Ready phase.
func waitForDatabaseReady(lookup types.NamespacedName) {
	Eventually(func(g Gomega) {
		var fetched v1alpha1.PostgresDatabase
		g.Expect(k8sClient.Get(ctx, lookup, &fetched)).To(Succeed())
		g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.DatabasePhaseReady))
	}, timeout, interval).Should(Succeed())
}

var _ = Describe("PostgresCredentialReconciler", func() {

	// ── Full lifecycle: create → ready → delete ─────────────────────────────
	Context("full credential lifecycle", Ordered, func() {
		var (
			ns               *corev1.Namespace
			pgdb             *v1alpha1.PostgresDatabase
			pgcred           *v1alpha1.PostgresCredential
			dbLookup         types.NamespacedName
			credLookup       types.NamespacedName
			credSecretLookup types.NamespacedName
			adminSecretKey   types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLookup, adminSecretKey = newTestDatabaseForCred("cred-lifecycle-db")
			waitForDatabaseReady(dbLookup)

			pgcred = &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cred-lifecycle",
					Namespace: ns.Name,
					Labels: map[string]string{
						"games-hub.io/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "appuser",
					SecretName:  "cred-lifecycle-secret",
					Permissions: []v1alpha1.DatabasePermission{
						v1alpha1.PermissionSelect,
						v1alpha1.PermissionInsert,
					},
				},
			}
			Expect(k8sClient.Create(ctx, pgcred)).To(Succeed())

			credLookup = types.NamespacedName{Name: pgcred.Name, Namespace: ns.Name}
			credSecretLookup = types.NamespacedName{Name: pgcred.Spec.SecretName, Namespace: ns.Name}
			_ = adminSecretKey
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		It("should transition PostgresCredential to Ready", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(k8sClient.Get(ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, timeout, interval).Should(Succeed())
		})

		It("should populate PostgresCredentialStatus.SecretName", func() {
			var fetched v1alpha1.PostgresCredential
			Expect(k8sClient.Get(ctx, credLookup, &fetched)).To(Succeed())
			Expect(fetched.Status.SecretName).To(Equal(pgcred.Spec.SecretName))
		})

		It("should create the credential Secret with expected keys", func() {
			var secret corev1.Secret
			Expect(k8sClient.Get(ctx, credSecretLookup, &secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey("username"))
			Expect(secret.Data).To(HaveKey("password"))
			Expect(secret.Data).To(HaveKey("host"))
			Expect(secret.Data).To(HaveKey("port"))
			Expect(secret.Data).To(HaveKey("database"))
			Expect(string(secret.Data["username"])).To(Equal("appuser"))
			Expect(string(secret.Data["password"])).To(HaveLen(24))
			Expect(string(secret.Data["port"])).To(Equal("5432"))
			Expect(string(secret.Data["database"])).To(Equal("testdb"))
		})

		It("should set a controller owner reference on the credential Secret", func() {
			var secret corev1.Secret
			Expect(k8sClient.Get(ctx, credSecretLookup, &secret)).To(Succeed())
			Expect(secret.OwnerReferences).To(HaveLen(1))
			Expect(secret.OwnerReferences[0].Name).To(Equal(pgcred.Name))
			Expect(*secret.OwnerReferences[0].Controller).To(BeTrue())
		})

		It("should add the finalizer to the PostgresCredential", func() {
			var fetched v1alpha1.PostgresCredential
			Expect(k8sClient.Get(ctx, credLookup, &fetched)).To(Succeed())
			Expect(fetched.Finalizers).To(ContainElement("games-hub.io/postgres-credential"))
		})

		It("should have created a Postgres user that can authenticate", func() {
			// Read the password from the credential Secret.
			var secret corev1.Secret
			Expect(k8sClient.Get(ctx, credSecretLookup, &secret)).To(Succeed())
			password := string(secret.Data["password"])

			// Use pod exec to run psql inside the Postgres pod with a
			// connection URI that embeds the password, verifying the user
			// can actually authenticate against the database.
			podName := fmt.Sprintf("%s-0", pgdb.Name)
			connStr := fmt.Sprintf("postgresql://appuser:%s@localhost:5432/testdb?sslmode=disable", password)
			stdout, stderr, err := podExec(ns.Name, podName, []string{"psql", connStr, "-c", "SELECT 1"})
			Expect(err).NotTo(HaveOccurred(), "psql connection failed: stdout=%s stderr=%s", stdout, stderr)
			Expect(stdout).To(ContainSubstring("1"))
		})
	})

	// ── Dependency-wait behaviour ────────────────────────────────────────────
	Context("when the target database is not yet Ready", Ordered, func() {
		var (
			ns         *corev1.Namespace
			pgcred     *v1alpha1.PostgresCredential
			credLookup types.NamespacedName
			dbLookup   types.NamespacedName
		)

		BeforeAll(func() {
			// Create the namespace and database, but DON'T wait for it to be Ready.
			ns, _, dbLookup, _ = newTestDatabaseForCred("cred-wait-db")

			pgcred = &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cred-wait",
					Namespace: ns.Name,
					Labels: map[string]string{
						"games-hub.io/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: "cred-wait-db",
					Username:    "waituser",
					SecretName:  "cred-wait-secret",
					Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionAll},
				},
			}
			Expect(k8sClient.Create(ctx, pgcred)).To(Succeed())
			credLookup = types.NamespacedName{Name: pgcred.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		It("should remain Pending while the database is not Ready", func() {
			// Give the reconciler enough time to have processed the CR at least once.
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(k8sClient.Get(ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhasePending))
			}, timeout, interval).Should(Succeed())
		})

		It("should transition to Ready once the database becomes Ready", func() {
			// Now wait for the database to become Ready.
			waitForDatabaseReady(dbLookup)

			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(k8sClient.Get(ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, timeout, interval).Should(Succeed())
		})
	})

	// ── Deletion / cleanup ───────────────────────────────────────────────────
	Context("when a PostgresCredential is deleted", Ordered, func() {
		var (
			ns               *corev1.Namespace
			pgdb             *v1alpha1.PostgresDatabase
			pgcred           *v1alpha1.PostgresCredential
			dbLookup         types.NamespacedName
			credLookup       types.NamespacedName
			credSecretLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLookup, _ = newTestDatabaseForCred("cred-delete-db")
			waitForDatabaseReady(dbLookup)

			pgcred = &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cred-delete",
					Namespace: ns.Name,
					Labels: map[string]string{
						"games-hub.io/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "deleteuser",
					SecretName:  "cred-delete-secret",
					Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
				},
			}
			Expect(k8sClient.Create(ctx, pgcred)).To(Succeed())
			credLookup = types.NamespacedName{Name: pgcred.Name, Namespace: ns.Name}
			credSecretLookup = types.NamespacedName{Name: pgcred.Spec.SecretName, Namespace: ns.Name}

			// Wait for the credential to be Ready (Secret and user exist).
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(k8sClient.Get(ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, timeout, interval).Should(Succeed())

			// Delete the credential and wait for it to be gone.
			Expect(k8sClient.Delete(ctx, pgcred)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				err := k8sClient.Get(ctx, credLookup, &fetched)
				g.Expect(err).To(HaveOccurred())
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			}, timeout, interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		It("should delete the credential Secret", func() {
			var secret corev1.Secret
			err := k8sClient.Get(ctx, credSecretLookup, &secret)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should drop the Postgres user", func() {
			// Verify via pod exec + psql that the role no longer exists.
			podName := fmt.Sprintf("%s-0", pgdb.Name)
			stdout, stderr, err := podExec(ns.Name, podName, []string{
				"psql", "-U", "postgres", "-d", "testdb",
				"-tAc", "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='deleteuser')",
			})
			Expect(err).NotTo(HaveOccurred(), "psql query failed: stdout=%s stderr=%s", stdout, stderr)
			Expect(stdout).To(ContainSubstring("f"), "Postgres role 'deleteuser' should have been dropped")
		})

		It("should leave no orphaned credential Secrets", func() {
			labels := client.MatchingLabels{
				"app.kubernetes.io/managed-by": "db-operator",
				"app.kubernetes.io/instance":   pgcred.Name,
			}

			var secretList corev1.SecretList
			Expect(k8sClient.List(ctx, &secretList, client.InNamespace(ns.Name), labels)).To(Succeed())
			Expect(secretList.Items).To(BeEmpty(), fmt.Sprintf("orphaned Secrets: %v", secretList.Items))
		})
	})

	// ── Instance label filtering ─────────────────────────────────────────────
	Context("when a PostgresCredential has no operator-instance label", Ordered, func() {
		var (
			ns         *corev1.Namespace
			pgcred     *v1alpha1.PostgresCredential
			credLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-pgcred-nolabel-",
				},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())

			pgcred = &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-label-cred",
					Namespace: ns.Name,
					// Deliberately omit the games-hub.io/operator-instance label.
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: "nonexistent-db",
					Username:    "nolabeluser",
					SecretName:  "no-label-cred-secret",
					Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
				},
			}
			Expect(k8sClient.Create(ctx, pgcred)).To(Succeed())
			credLookup = types.NamespacedName{Name: pgcred.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, pgcred)
			_ = k8sClient.Delete(ctx, ns)
		})

		It("should never set a status phase on the CR", func() {
			Consistently(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(k8sClient.Get(ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(BeEmpty())
			}, 10*time.Second, interval).Should(Succeed())
		})

		It("should not create the credential Secret", func() {
			var secretList corev1.SecretList
			Expect(k8sClient.List(ctx, &secretList, client.InNamespace(ns.Name))).To(Succeed())
			for _, s := range secretList.Items {
				Expect(s.Name).NotTo(Equal("no-label-cred-secret"), "credential Secret should not exist for unlabelled CR")
			}
		})
	})
})
