//go:build integration

package controller_test

import (
	"fmt"
	"time"

	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
			ns, pgdb, dbLookup, adminSecretKey = NewDatabase("cred-lifecycle-db")
			WaitForDatabase(dbLookup)

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
			Expect(K8sClient.Create(Ctx, pgcred)).To(Succeed())

			credLookup = types.NamespacedName{Name: pgcred.Name, Namespace: ns.Name}
			credSecretLookup = types.NamespacedName{Name: pgcred.Spec.SecretName, Namespace: ns.Name}
			_ = adminSecretKey
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should transition PostgresCredential to Ready", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})

		It("should populate PostgresCredentialStatus.SecretName", func() {
			var fetched v1alpha1.PostgresCredential
			Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
			Expect(fetched.Status.SecretName).To(Equal(pgcred.Spec.SecretName))
		})

		It("should create the credential Secret with expected keys", func() {
			var secret corev1.Secret
			Expect(K8sClient.Get(Ctx, credSecretLookup, &secret)).To(Succeed())
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
			Expect(K8sClient.Get(Ctx, credSecretLookup, &secret)).To(Succeed())
			Expect(secret.OwnerReferences).To(HaveLen(1))
			Expect(secret.OwnerReferences[0].Name).To(Equal(pgcred.Name))
			Expect(*secret.OwnerReferences[0].Controller).To(BeTrue())
		})

		It("should add the finalizer to the PostgresCredential", func() {
			var fetched v1alpha1.PostgresCredential
			Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
			Expect(fetched.Finalizers).To(ContainElement("games-hub.io/postgres-credential"))
		})

		It("should have created a Postgres user that can authenticate", func() {
			db, close := ConnectToDatabase(dbLookup, credSecretLookup)
			defer close()
			Expect(db.Ping()).To(Succeed(), "pinging database with created credentials should succeed")
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
			ns, _, dbLookup, _ = NewDatabase("cred-wait-db")

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
			Expect(K8sClient.Create(Ctx, pgcred)).To(Succeed())
			credLookup = types.NamespacedName{Name: pgcred.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should remain Pending while the database is not Ready", func() {
			// Give the reconciler enough time to have processed the CR at least once.
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhasePending))
			}, Timeout, Interval).Should(Succeed())
		})

		It("should transition to Ready once the database becomes Ready", func() {
			// Now wait for the database to become Ready.
			WaitForDatabase(dbLookup)

			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})
	})

	// ── Deletion / cleanup ───────────────────────────────────────────────────
	Context("when a PostgresCredential is deleted", Ordered, func() {
		var (
			ns                *corev1.Namespace
			pgdb              *v1alpha1.PostgresDatabase
			pgcred            *v1alpha1.PostgresCredential
			dbLookup          types.NamespacedName
			adminSecretLookup types.NamespacedName
			credLookup        types.NamespacedName
			credSecretLookup  types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLookup, adminSecretLookup = NewDatabase("cred-delete-db")
			WaitForDatabase(dbLookup)

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
			Expect(K8sClient.Create(Ctx, pgcred)).To(Succeed())
			credLookup = types.NamespacedName{Name: pgcred.Name, Namespace: ns.Name}
			credSecretLookup = types.NamespacedName{Name: pgcred.Spec.SecretName, Namespace: ns.Name}

			// Wait for the credential to be Ready (Secret and user exist).
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())

			// Delete the credential and wait for it to be gone.
			Expect(K8sClient.Delete(Ctx, pgcred)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				err := K8sClient.Get(Ctx, credLookup, &fetched)
				g.Expect(err).To(HaveOccurred())
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			}, Timeout, Interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should delete the credential Secret", func() {
			var secret corev1.Secret
			err := K8sClient.Get(Ctx, credSecretLookup, &secret)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should drop the Postgres user", func() {
			db, close := ConnectToDatabase(dbLookup, adminSecretLookup)
			defer close()

			var exists bool
			err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = 'deleteuser')`).Scan(&exists)
			Expect(err).To(Succeed(), "querying for existence of Postgres role should not error")
			Expect(exists).To(BeFalse(), "Postgres role 'deleteuser' should have been dropped")
		})

		It("should leave no orphaned credential Secrets", func() {
			labels := client.MatchingLabels{
				"app.kubernetes.io/managed-by": "db-operator",
				"app.kubernetes.io/instance":   pgcred.Name,
			}

			var secretList corev1.SecretList
			Expect(K8sClient.List(Ctx, &secretList, client.InNamespace(ns.Name), labels)).To(Succeed())
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
			Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

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
			Expect(K8sClient.Create(Ctx, pgcred)).To(Succeed())
			credLookup = types.NamespacedName{Name: pgcred.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, pgcred)
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should never set a status phase on the CR", func() {
			Consistently(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(BeEmpty())
			}, 10*time.Second, Interval).Should(Succeed())
		})

		It("should not create the credential Secret", func() {
			var secretList corev1.SecretList
			Expect(K8sClient.List(Ctx, &secretList, client.InNamespace(ns.Name))).To(Succeed())
			for _, s := range secretList.Items {
				Expect(s.Name).NotTo(Equal("no-label-cred-secret"), "credential Secret should not exist for unlabelled CR")
			}
		})
	})
})
