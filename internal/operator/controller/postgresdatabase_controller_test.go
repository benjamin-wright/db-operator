//go:build integration

package controller_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
)

// newTestResources creates a unique namespace and a PostgresDatabase CR inside
// it, returning lookup keys for both the CR and its admin Secret. It does NOT
// create the CR — callers do that so they can control the exact moment of
// creation relative to their assertions.
func newTestResources(name string) (ns *corev1.Namespace, pgdb *v1alpha1.PostgresDatabase, lookup, secretLookup types.NamespacedName) {
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-pgdb-",
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
			DatabaseName:    "mydb",
			PostgresVersion: "16",
			StorageSize:     resource.MustParse("256Mi"),
		},
	}
	lookup = types.NamespacedName{Name: pgdb.Name, Namespace: ns.Name}
	secretLookup = types.NamespacedName{Name: pgdb.Name + "-admin", Namespace: ns.Name}
	return
}

var _ = Describe("PostgresDatabaseReconciler", func() {

	// ── Phase lifecycle ──────────────────────────────────────────────────────
	// One DB instance covers the full Pending → Ready transition.
	Context("phase lifecycle", Ordered, func() {
		var (
			ns     *corev1.Namespace
			pgdb   *v1alpha1.PostgresDatabase
			lookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, lookup, _ = newTestResources("test-db")
			Expect(k8sClient.Create(ctx, pgdb)).To(Succeed())
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		It("should initially set status phase to Pending before the StatefulSet is ready", func() {
			// The reconciler should report Pending immediately after creating the
			// StatefulSet, since the pod won't be ready yet.
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresDatabase
				g.Expect(k8sClient.Get(ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.DatabasePhasePending))
			}, timeout, interval).Should(Succeed())
		})

		It("should transition to Ready when the StatefulSet has ready replicas", func() {
			// On a real cluster the StatefulSet controller will schedule the pod
			// and it will become ready once Postgres starts.
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresDatabase
				g.Expect(k8sClient.Get(ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.DatabasePhaseReady))
			}, timeout, interval).Should(Succeed())
		})
	})

	// ── Steady-state resource properties ────────────────────────────────────
	// One DB instance, waited to Ready, shared across all property assertions.
	Context("when a PostgresDatabase is created and becomes ready", Ordered, func() {
		var (
			ns           *corev1.Namespace
			pgdb         *v1alpha1.PostgresDatabase
			lookup       types.NamespacedName
			secretLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, lookup, secretLookup = newTestResources("test-db")
			Expect(k8sClient.Create(ctx, pgdb)).To(Succeed())

			// Wait until all owned resources exist and the DB is ready before
			// running any of the property assertions below.
			Eventually(func(g Gomega) {
				var sts appsv1.StatefulSet
				g.Expect(k8sClient.Get(ctx, lookup, &sts)).To(Succeed())
				var svc corev1.Service
				g.Expect(k8sClient.Get(ctx, lookup, &svc)).To(Succeed())
				var secret corev1.Secret
				g.Expect(k8sClient.Get(ctx, secretLookup, &secret)).To(Succeed())
				var fetched v1alpha1.PostgresDatabase
				g.Expect(k8sClient.Get(ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.DatabasePhaseReady))
			}, timeout, interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		It("should create a headless Service on port 5432", func() {
			var svc corev1.Service
			Expect(k8sClient.Get(ctx, lookup, &svc)).To(Succeed())
			Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(5432)))
		})

		It("should create a StatefulSet with the right image, replicas, and PVC", func() {
			var sts appsv1.StatefulSet
			Expect(k8sClient.Get(ctx, lookup, &sts)).To(Succeed())
			Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("postgres:16"))
			Expect(sts.Spec.VolumeClaimTemplates).To(HaveLen(1))
			Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
		})

		It("should set owner references on the StatefulSet and Service", func() {
			var svc corev1.Service
			Expect(k8sClient.Get(ctx, lookup, &svc)).To(Succeed())
			Expect(svc.OwnerReferences).To(HaveLen(1))
			Expect(svc.OwnerReferences[0].Name).To(Equal(pgdb.Name))

			var sts appsv1.StatefulSet
			Expect(k8sClient.Get(ctx, lookup, &sts)).To(Succeed())
			Expect(sts.OwnerReferences).To(HaveLen(1))
			Expect(sts.OwnerReferences[0].Name).To(Equal(pgdb.Name))
		})

		It("should add the finalizer to the CR", func() {
			var fetched v1alpha1.PostgresDatabase
			Expect(k8sClient.Get(ctx, lookup, &fetched)).To(Succeed())
			Expect(fetched.Finalizers).To(ContainElement("games-hub.io/postgres-database"))
		})

		It("should set the correct environment variables on the Postgres container", func() {
			var sts appsv1.StatefulSet
			Expect(k8sClient.Get(ctx, lookup, &sts)).To(Succeed())

			container := sts.Spec.Template.Spec.Containers[0]

			// POSTGRES_DB and POSTGRES_USER should be plain values.
			envMap := make(map[string]string)
			for _, e := range container.Env {
				if e.Value != "" {
					envMap[e.Name] = e.Value
				}
			}
			Expect(envMap["POSTGRES_DB"]).To(Equal("mydb"))
			Expect(envMap["POSTGRES_USER"]).To(Equal("postgres"))

			// POSTGRES_PASSWORD must be sourced from the admin Secret via secretKeyRef.
			var passwordEnv *corev1.EnvVar
			for i := range container.Env {
				if container.Env[i].Name == "POSTGRES_PASSWORD" {
					passwordEnv = &container.Env[i]
					break
				}
			}
			Expect(passwordEnv).NotTo(BeNil())
			Expect(passwordEnv.Value).To(BeEmpty(), "POSTGRES_PASSWORD must not have a literal value")
			Expect(passwordEnv.ValueFrom).NotTo(BeNil())
			Expect(passwordEnv.ValueFrom.SecretKeyRef).NotTo(BeNil())
			Expect(passwordEnv.ValueFrom.SecretKeyRef.Name).To(Equal(pgdb.Name + "-admin"))
			Expect(passwordEnv.ValueFrom.SecretKeyRef.Key).To(Equal("password"))
		})

		It("should create an admin Secret with username and password keys", func() {
			var secret corev1.Secret
			Expect(k8sClient.Get(ctx, secretLookup, &secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey("username"))
			Expect(secret.Data).To(HaveKey("password"))
			Expect(string(secret.Data["username"])).To(Equal("postgres"))
			Expect(string(secret.Data["password"])).To(HaveLen(24))
		})

		It("should set a controller owner reference on the admin Secret", func() {
			var secret corev1.Secret
			Expect(k8sClient.Get(ctx, secretLookup, &secret)).To(Succeed())
			Expect(secret.OwnerReferences).To(HaveLen(1))
			Expect(secret.OwnerReferences[0].Name).To(Equal(pgdb.Name))
			Expect(*secret.OwnerReferences[0].Controller).To(BeTrue())
		})

		It("should populate PostgresDatabaseStatus.SecretName", func() {
			var fetched v1alpha1.PostgresDatabase
			Expect(k8sClient.Get(ctx, lookup, &fetched)).To(Succeed())
			Expect(fetched.Status.SecretName).To(Equal(pgdb.Name + "-admin"))
		})
	})

	// ── Instance label filtering ─────────────────────────────────────────────
	// Verify that a CR without the operator-instance label is never reconciled.
	Context("when a PostgresDatabase has no operator-instance label", Ordered, func() {
		var (
			ns     *corev1.Namespace
			pgdb   *v1alpha1.PostgresDatabase
			lookup types.NamespacedName
		)

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-pgdb-nolabel-",
				},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())

			pgdb = &v1alpha1.PostgresDatabase{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-label-db",
					Namespace: ns.Name,
					// Deliberately omit the games-hub.io/operator-instance label.
				},
				Spec: v1alpha1.PostgresDatabaseSpec{
					DatabaseName:    "mydb",
					PostgresVersion: "16",
					StorageSize:     resource.MustParse("256Mi"),
				},
			}
			Expect(k8sClient.Create(ctx, pgdb)).To(Succeed())
			lookup = types.NamespacedName{Name: pgdb.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, pgdb)
			_ = k8sClient.Delete(ctx, ns)
		})

		It("should never set a status phase on the CR", func() {
			// The operator's cache excludes CRs without the instance label so the
			// reconciler is never called. The status phase must remain empty.
			Consistently(func(g Gomega) {
				var fetched v1alpha1.PostgresDatabase
				g.Expect(k8sClient.Get(ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(BeEmpty())
			}, 10*time.Second, interval).Should(Succeed())
		})

		It("should not create any owned sub-resources", func() {
			var stsList appsv1.StatefulSetList
			Expect(k8sClient.List(ctx, &stsList, client.InNamespace(ns.Name))).To(Succeed())
			Expect(stsList.Items).To(BeEmpty(), "expected no StatefulSets for unlabelled CR")

			var svcList corev1.ServiceList
			Expect(k8sClient.List(ctx, &svcList, client.InNamespace(ns.Name))).To(Succeed())
			Expect(svcList.Items).To(BeEmpty(), "expected no Services for unlabelled CR")
		})
	})

	// ── Deletion / cleanup ───────────────────────────────────────────────────
	// One DB instance: created, waited to ready, then deleted. All cleanup
	// assertions run against the same post-deletion state.
	Context("when a PostgresDatabase is deleted", Ordered, func() {
		var (
			ns           *corev1.Namespace
			pgdb         *v1alpha1.PostgresDatabase
			lookup       types.NamespacedName
			secretLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, pgdb, lookup, secretLookup = newTestResources("test-db")
			Expect(k8sClient.Create(ctx, pgdb)).To(Succeed())

			// Wait for all owned resources to exist.
			Eventually(func(g Gomega) {
				var sts appsv1.StatefulSet
				g.Expect(k8sClient.Get(ctx, lookup, &sts)).To(Succeed())
				var svc corev1.Service
				g.Expect(k8sClient.Get(ctx, lookup, &svc)).To(Succeed())
				var secret corev1.Secret
				g.Expect(k8sClient.Get(ctx, secretLookup, &secret)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			// Delete the CR and wait for it to be fully removed (finalizer handled).
			Expect(k8sClient.Delete(ctx, pgdb)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.PostgresDatabase
				err := k8sClient.Get(ctx, lookup, &fetched)
				g.Expect(err).To(HaveOccurred())
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			}, timeout, interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		It("should cascade-delete the StatefulSet", func() {
			var sts appsv1.StatefulSet
			err := k8sClient.Get(ctx, lookup, &sts)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should cascade-delete the Service", func() {
			var svc corev1.Service
			err := k8sClient.Get(ctx, lookup, &svc)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should cascade-delete the admin Secret", func() {
			var secret corev1.Secret
			err := k8sClient.Get(ctx, secretLookup, &secret)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should leave no orphaned resources with the operator-managed label", func() {
			labels := client.MatchingLabels{
				"app.kubernetes.io/managed-by": "db-operator",
				"app.kubernetes.io/instance":   pgdb.Name,
			}

			var stsList appsv1.StatefulSetList
			Expect(k8sClient.List(ctx, &stsList, client.InNamespace(ns.Name), labels)).To(Succeed())
			Expect(stsList.Items).To(BeEmpty(), fmt.Sprintf("orphaned StatefulSets: %v", stsList.Items))

			var svcList corev1.ServiceList
			Expect(k8sClient.List(ctx, &svcList, client.InNamespace(ns.Name), labels)).To(Succeed())
			Expect(svcList.Items).To(BeEmpty(), fmt.Sprintf("orphaned Services: %v", svcList.Items))

			var secretList corev1.SecretList
			Expect(k8sClient.List(ctx, &secretList, client.InNamespace(ns.Name), labels)).To(Succeed())
			Expect(secretList.Items).To(BeEmpty(), fmt.Sprintf("orphaned Secrets: %v", secretList.Items))
		})
	})
})
