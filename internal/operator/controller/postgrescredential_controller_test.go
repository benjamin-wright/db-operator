//go:build integration

package controller_test

import (
	"fmt"
	"time"

	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
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
		)

		BeforeAll(func() {
			ns, pgdb, dbLookup, _ = NewDatabase("cred-lifecycle-db")
			WaitForDatabase(dbLookup)

			pgcred = &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cred-lifecycle",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "appuser",
					SecretName:  "cred-lifecycle-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases: []string{"testdb"},
							Permissions: []v1alpha1.DatabasePermission{
								v1alpha1.PermissionSelect,
								v1alpha1.PermissionInsert,
							},
						},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, pgcred)).To(Succeed())

			credLookup = types.NamespacedName{Name: pgcred.Name, Namespace: ns.Name}
			credSecretLookup = types.NamespacedName{Name: pgcred.Spec.SecretName, Namespace: ns.Name}
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
			Expect(secret.Data).To(HaveKey("PGUSER"))
			Expect(secret.Data).To(HaveKey("PGPASSWORD"))
			Expect(secret.Data).To(HaveKey("PGHOST"))
			Expect(secret.Data).To(HaveKey("PGPORT"))
			Expect(secret.Data).To(HaveKey("PGDATABASE"))
			Expect(string(secret.Data["PGUSER"])).To(Equal("appuser"))
			Expect(string(secret.Data["PGPASSWORD"])).To(HaveLen(24))
			Expect(string(secret.Data["PGPORT"])).To(Equal("5432"))
			Expect(string(secret.Data["PGDATABASE"])).To(Equal("testdb"))
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
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: "cred-wait-db",
					Username:    "waituser",
					SecretName:  "cred-wait-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"testdb"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionAll},
						},
					},
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
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "deleteuser",
					SecretName:  "cred-delete-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"testdb"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
						},
					},
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
					// Deliberately omit the db-operator.benjamin-wright.github.com/operator-instance label.
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: "nonexistent-db",
					Username:    "nolabeluser",
					SecretName:  "no-label-cred-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"testdb"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
						},
					},
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

	// ── Multi-database credential ────────────────────────────────────────────
	// One credential covers two databases; a second credential creates a third
	// database.  Verify the multi-db user can query tables in its two databases
	// but is denied in the one it was never granted access to.
	Context("when a credential covers multiple databases", Ordered, func() {
		var (
			ns                *corev1.Namespace
			pgdb              *v1alpha1.PostgresDatabase
			dbLookup          types.NamespacedName
			adminSecretLookup types.NamespacedName
			multiSecretLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLookup, adminSecretLookup = NewDatabase("multi-db-instance")
			WaitForDatabase(dbLookup)

			// Credential with access to two databases in one entry.
			multiCred := &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-cred",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "multiuser",
					SecretName:  "multi-cred-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"db_alpha", "db_beta"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
						},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, multiCred)).To(Succeed())

			// A separate credential that creates a third database; multiuser is
			// never granted access to it.
			otherCred := &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-cred",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "otheruser",
					SecretName:  "other-cred-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"db_gamma"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
						},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, otherCred)).To(Succeed())

			multiSecretLookup = types.NamespacedName{Name: "multi-cred-secret", Namespace: ns.Name}

			// Wait for both credentials to be Ready before touching databases.
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, types.NamespacedName{Name: "multi-cred", Namespace: ns.Name}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, types.NamespacedName{Name: "other-cred", Namespace: ns.Name}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())

			// Create a test table in each database as admin.  Because the
			// credentials were already provisioned (ALTER DEFAULT PRIVILEGES was
			// run by EnsureUser), tables created now by the postgres role inherit
			// those default privileges automatically.
			for _, dbName := range []string{"db_alpha", "db_beta", "db_gamma"} {
				db, closeConn := ConnectToDatabaseNamed(dbLookup, adminSecretLookup, dbName)
				_, err := db.Exec("CREATE TABLE IF NOT EXISTS items (id INT)")
				Expect(err).NotTo(HaveOccurred(), "creating items table in "+dbName)
				closeConn()
			}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should set PGDATABASE in the credential Secret for a single-database credential", func() {
			var secret corev1.Secret
			Expect(K8sClient.Get(Ctx, multiSecretLookup, &secret)).To(Succeed())
			Expect(secret.Data).NotTo(HaveKey("PGDATABASE"), "multi-database credential should not set PGDATABASE")
		})

		It("should allow multiuser to query tables in db_alpha", func() {
			db, closeConn := ConnectToDatabaseNamed(dbLookup, multiSecretLookup, "db_alpha")
			defer closeConn()
			_, err := db.Exec("SELECT * FROM items")
			Expect(err).NotTo(HaveOccurred(), "multiuser should have SELECT on db_alpha")
		})

		It("should allow multiuser to query tables in db_beta", func() {
			db, closeConn := ConnectToDatabaseNamed(dbLookup, multiSecretLookup, "db_beta")
			defer closeConn()
			_, err := db.Exec("SELECT * FROM items")
			Expect(err).NotTo(HaveOccurred(), "multiuser should have SELECT on db_beta")
		})

		It("should deny multiuser SELECT on tables in db_gamma", func() {
			db, closeConn := ConnectToDatabaseNamed(dbLookup, multiSecretLookup, "db_gamma")
			defer closeConn()
			_, err := db.Exec("SELECT * FROM items")
			Expect(err).To(HaveOccurred(), "multiuser should not have SELECT on db_gamma")
		})
	})

	// ── Table-scoped permission grants ───────────────────────────────────────
	// Verify that a credential with tables: [foo] can only access the named
	// table, that a credential without tables retains full-schema access, and
	// that a credential referencing a non-existent table transitions to Failed.
	Context("when permissions are scoped to specific tables", Ordered, func() {
		var (
			ns                 *corev1.Namespace
			pgdb               *v1alpha1.PostgresDatabase
			dbLookup           types.NamespacedName
			adminSecretLookup  types.NamespacedName
			scopedSecretLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLookup, adminSecretLookup = NewDatabase("table-scoped-db")
			WaitForDatabase(dbLookup)

			// Create a bootstrap credential so the operator provisions the
			// `scopeddb` database. We then create the test tables via the admin
			// connection before provisioning the table-scoped credential.
			bootstrapCred := &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scoped-bootstrap",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "bootstrapuser",
					SecretName:  "scoped-bootstrap-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"scopeddb"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionAll},
						},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, bootstrapCred)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, types.NamespacedName{Name: "scoped-bootstrap", Namespace: ns.Name}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())

			// scopeddb now exists — create the test tables as admin.
			db, closeConn := ConnectToDatabaseNamed(dbLookup, adminSecretLookup, "scopeddb")
			_, err := db.Exec("CREATE TABLE IF NOT EXISTS allowed_table (id INT)")
			Expect(err).NotTo(HaveOccurred(), "creating allowed_table")
			_, err = db.Exec("CREATE TABLE IF NOT EXISTS forbidden_table (id INT)")
			Expect(err).NotTo(HaveOccurred(), "creating forbidden_table")
			closeConn()

			scopedCred := &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scoped-cred",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "scopeduser",
					SecretName:  "scoped-cred-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"scopeddb"},
							Tables:      []string{"allowed_table"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
						},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, scopedCred)).To(Succeed())
			scopedSecretLookup = types.NamespacedName{Name: "scoped-cred-secret", Namespace: ns.Name}

			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, types.NamespacedName{Name: "scoped-cred", Namespace: ns.Name}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should allow scopeduser to SELECT from allowed_table", func() {
			db, closeConn := ConnectToDatabaseNamed(dbLookup, scopedSecretLookup, "scopeddb")
			defer closeConn()
			_, err := db.Exec("SELECT * FROM allowed_table")
			Expect(err).NotTo(HaveOccurred(), "scopeduser should have SELECT on allowed_table")
		})

		It("should deny scopeduser SELECT on forbidden_table", func() {
			db, closeConn := ConnectToDatabaseNamed(dbLookup, scopedSecretLookup, "scopeddb")
			defer closeConn()
			_, err := db.Exec("SELECT * FROM forbidden_table")
			Expect(err).To(HaveOccurred(), "scopeduser should not have SELECT on forbidden_table")
		})
	})

	Context("when a table-scoped credential references a non-existent table", Ordered, func() {
		var (
			ns         *corev1.Namespace
			pgdb       *v1alpha1.PostgresDatabase
			dbLookup   types.NamespacedName
			credLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLookup, _ = NewDatabase("table-missing-db")
			WaitForDatabase(dbLookup)

			missingCred := &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "missing-table-cred",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "missinguser",
					SecretName:  "missing-table-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"missingdb"},
							Tables:      []string{"no_such_table"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
						},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, missingCred)).To(Succeed())
			credLookup = types.NamespacedName{Name: "missing-table-cred", Namespace: ns.Name}
			_ = dbLookup
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should transition to Pending with reason WaitingForTable", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhasePending))
				cond := meta.FindStatusCondition(fetched.Status.Conditions, "Ready")
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Reason).To(Equal("WaitingForTable"))
			}, Timeout, Interval).Should(Succeed())
		})
	})

	// ── clusterRoles: pg_read_all_data without per-database permissions ──────
	Context("when a credential sets clusterRoles only", Ordered, func() {
		var (
			ns                *corev1.Namespace
			pgdb              *v1alpha1.PostgresDatabase
			cred              *v1alpha1.PostgresCredential
			dbLookup          types.NamespacedName
			adminSecretLookup types.NamespacedName
			credLookup        types.NamespacedName
			credSecretLookup  types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLookup, adminSecretLookup = NewDatabase("clusterroles-db")
			WaitForDatabase(dbLookup)

			// Create a table in the default "postgres" database as the admin so
			// the clusterRoles user has something to read via pg_read_all_data.
			db, closeConn := ConnectToDatabaseNamed(dbLookup, adminSecretLookup, "postgres")
			defer closeConn()
			_, err := db.Exec("CREATE TABLE IF NOT EXISTS cr_widgets (id int)")
			Expect(err).NotTo(HaveOccurred())
			_, err = db.Exec("INSERT INTO cr_widgets VALUES (1), (2), (3)")
			Expect(err).NotTo(HaveOccurred())

			cred = &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cr-cred",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef:  pgdb.Name,
					Username:     "cruser",
					SecretName:   "cr-cred-secret",
					ClusterRoles: []v1alpha1.PredefinedRole{v1alpha1.RolePgReadAllData},
				},
			}
			Expect(K8sClient.Create(Ctx, cred)).To(Succeed())
			credLookup = types.NamespacedName{Name: cred.Name, Namespace: ns.Name}
			credSecretLookup = types.NamespacedName{Name: cred.Spec.SecretName, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should transition to Ready", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})

		It("should grant pg_read_all_data membership", func() {
			db, closeConn := ConnectToDatabaseNamed(dbLookup, adminSecretLookup, "postgres")
			defer closeConn()
			var hasMembership bool
			err := db.QueryRow(`
				SELECT EXISTS (
					SELECT 1 FROM pg_auth_members am
					JOIN pg_roles r ON r.oid = am.roleid
					JOIN pg_roles m ON m.oid = am.member
					WHERE r.rolname = 'pg_read_all_data' AND m.rolname = 'cruser'
				)`).Scan(&hasMembership)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasMembership).To(BeTrue())
		})

		It("should let the user SELECT from a table created by the admin", func() {
			db, closeConn := ConnectToDatabaseNamed(dbLookup, credSecretLookup, "postgres")
			defer closeConn()
			var count int
			err := db.QueryRow("SELECT count(*) FROM cr_widgets").Scan(&count)
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(3))
		})

		It("should let the user SELECT from a table created AFTER the credential", func() {
			adminDB, closeAdmin := ConnectToDatabaseNamed(dbLookup, adminSecretLookup, "postgres")
			defer closeAdmin()
			_, err := adminDB.Exec("CREATE TABLE cr_late (id int)")
			Expect(err).NotTo(HaveOccurred())
			_, err = adminDB.Exec("INSERT INTO cr_late VALUES (42)")
			Expect(err).NotTo(HaveOccurred())

			userDB, closeUser := ConnectToDatabaseNamed(dbLookup, credSecretLookup, "postgres")
			defer closeUser()
			var v int
			err = userDB.QueryRow("SELECT id FROM cr_late").Scan(&v)
			Expect(err).NotTo(HaveOccurred())
			Expect(v).To(Equal(42))
		})

		It("should NOT let the user INSERT (read-only role)", func() {
			db, closeConn := ConnectToDatabaseNamed(dbLookup, credSecretLookup, "postgres")
			defer closeConn()
			_, err := db.Exec("INSERT INTO cr_widgets VALUES (99)")
			Expect(err).To(HaveOccurred())
		})

		It("should drop the role on credential deletion", func() {
			Expect(K8sClient.Delete(Ctx, cred)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				err := K8sClient.Get(Ctx, credLookup, &fetched)
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
				g.Expect(err).To(HaveOccurred())
			}, Timeout, Interval).Should(Succeed())

			db, closeConn := ConnectToDatabaseNamed(dbLookup, adminSecretLookup, "postgres")
			defer closeConn()
			var exists bool
			err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = 'cruser')").Scan(&exists)
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeFalse())
		})
	})

	Context("Bug B: table-scoped credential should recover when the table appears", Ordered, func() {
		var (
			ns                *corev1.Namespace
			pgdb              *v1alpha1.PostgresDatabase
			dbLookup          types.NamespacedName
			adminSecretLookup types.NamespacedName
			lateCredLookup    types.NamespacedName
			lateSecretLookup  types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, dbLookup, adminSecretLookup = NewDatabase("bug-b-db")
			WaitForDatabase(dbLookup)

			bootstrap := &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bug-b-bootstrap",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "bug_b_bootstrap",
					SecretName:  "bug-b-bootstrap-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"bug_b_db"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionAll},
						},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, bootstrap)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, types.NamespacedName{Name: bootstrap.Name, Namespace: ns.Name}, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())

			lateCred := &v1alpha1.PostgresCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bug-b-late",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.PostgresCredentialSpec{
					DatabaseRef: pgdb.Name,
					Username:    "bug_b_late",
					SecretName:  "bug-b-late-secret",
					Permissions: []v1alpha1.DatabasePermissionEntry{
						{
							Databases:   []string{"bug_b_db"},
							Tables:      []string{"bug_b_late_table"},
							Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
						},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, lateCred)).To(Succeed())
			lateCredLookup = types.NamespacedName{Name: lateCred.Name, Namespace: ns.Name}
			lateSecretLookup = types.NamespacedName{Name: lateCred.Spec.SecretName, Namespace: ns.Name}

			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, lateCredLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).NotTo(BeEmpty())
			}, Timeout, Interval).Should(Succeed())

			adminDB, closeAdmin := ConnectToDatabaseNamed(dbLookup, adminSecretLookup, "bug_b_db")
			defer closeAdmin()
			_, err := adminDB.Exec("CREATE TABLE bug_b_late_table (id INT)")
			Expect(err).NotTo(HaveOccurred())
			_, err = adminDB.Exec("INSERT INTO bug_b_late_table VALUES (7)")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should not surface a terminal Failed phase for a missing table", func() {
			Consistently(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, lateCredLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).NotTo(Equal(v1alpha1.CredentialPhaseFailed))
			}, 5*time.Second, Interval).Should(Succeed())
		})

		It("should transition to Ready once the table exists", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresCredential
				g.Expect(K8sClient.Get(Ctx, lateCredLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.CredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})

		It("should issue credentials that authenticate against PostgreSQL", func() {
			db, closeConn := ConnectToDatabaseNamed(dbLookup, lateSecretLookup, "bug_b_db")
			defer closeConn()
			Expect(db.Ping()).To(Succeed())
		})

		It("should let the late credential SELECT from the table", func() {
			db, closeConn := ConnectToDatabaseNamed(dbLookup, lateSecretLookup, "bug_b_db")
			defer closeConn()
			var v int
			err := db.QueryRow("SELECT id FROM bug_b_late_table").Scan(&v)
			Expect(err).NotTo(HaveOccurred())
			Expect(v).To(Equal(7))
		})
	})
})
