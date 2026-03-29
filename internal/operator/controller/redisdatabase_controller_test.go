//go:build integration

package controller_test

import (
	"fmt"
	"time"

	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

// newTestRedisResources creates a unique namespace and a RedisDatabase CR inside
// it, returning lookup keys for both the CR and its admin Secret. It does NOT
// create the CR — callers do that so they can control the exact moment of
// creation relative to their assertions.
func newTestRedisResources(name string) (ns *corev1.Namespace, rdb *v1alpha1.RedisDatabase, lookup, secretLookup types.NamespacedName) {
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-rdb-",
		},
	}
	Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

	rdb = &v1alpha1.RedisDatabase{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns.Name,
			Labels: map[string]string{
				"db-operator.benjamin-wright.github.com/operator-instance": "test",
			},
		},
		Spec: v1alpha1.RedisDatabaseSpec{
			StorageSize: resource.MustParse("256Mi"),
		},
	}
	lookup = types.NamespacedName{Name: rdb.Name, Namespace: ns.Name}
	secretLookup = types.NamespacedName{Name: rdb.Name + "-admin", Namespace: ns.Name}
	return
}

var _ = Describe("RedisDatabaseReconciler", func() {

	// ── Phase lifecycle ──────────────────────────────────────────────────────
	Context("phase lifecycle", Ordered, func() {
		var (
			ns     *corev1.Namespace
			rdb    *v1alpha1.RedisDatabase
			lookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, rdb, lookup, _ = newTestRedisResources("test-rdb")
			Expect(K8sClient.Create(Ctx, rdb)).To(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should initially set status phase to Pending before the StatefulSet is ready", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.RedisDatabase
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.RedisDatabasePhasePending))
			}, Timeout, Interval).Should(Succeed())
		})

		It("should transition to Ready when the StatefulSet has ready replicas", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.RedisDatabase
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.RedisDatabasePhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})
	})

	// ── Steady-state resource properties ────────────────────────────────────
	Context("when a RedisDatabase is created and becomes ready", Ordered, func() {
		var (
			ns           *corev1.Namespace
			rdb          *v1alpha1.RedisDatabase
			lookup       types.NamespacedName
			secretLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, rdb, lookup, secretLookup = newTestRedisResources("test-rdb")
			Expect(K8sClient.Create(Ctx, rdb)).To(Succeed())

			// Wait until all owned resources exist and the instance is ready.
			Eventually(func(g Gomega) {
				var sts appsv1.StatefulSet
				g.Expect(K8sClient.Get(Ctx, lookup, &sts)).To(Succeed())
				var svc corev1.Service
				g.Expect(K8sClient.Get(Ctx, lookup, &svc)).To(Succeed())
				var secret corev1.Secret
				g.Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed())
				var fetched v1alpha1.RedisDatabase
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.RedisDatabasePhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should create a headless Service on port 6379", func() {
			var svc corev1.Service
			Expect(K8sClient.Get(Ctx, lookup, &svc)).To(Succeed())
			Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(6379)))
		})

		It("should create a StatefulSet with the redis:8 image, one replica, and a PVC", func() {
			var sts appsv1.StatefulSet
			Expect(K8sClient.Get(Ctx, lookup, &sts)).To(Succeed())
			Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("redis:8"))
			Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
			Expect(sts.Spec.VolumeClaimTemplates).To(HaveLen(1))
		})

		It("should pass --requirepass via the container command", func() {
			var sts appsv1.StatefulSet
			Expect(K8sClient.Get(Ctx, lookup, &sts)).To(Succeed())
			container := sts.Spec.Template.Spec.Containers[0]
			Expect(container.Command).To(Equal([]string{"redis-server", "--requirepass", "$(REDIS_PASSWORD)"}))
		})

		It("should source REDIS_PASSWORD from the admin Secret", func() {
			var sts appsv1.StatefulSet
			Expect(K8sClient.Get(Ctx, lookup, &sts)).To(Succeed())
			container := sts.Spec.Template.Spec.Containers[0]

			var passwordEnv *corev1.EnvVar
			for i := range container.Env {
				if container.Env[i].Name == "REDIS_PASSWORD" {
					passwordEnv = &container.Env[i]
					break
				}
			}
			Expect(passwordEnv).NotTo(BeNil())
			Expect(passwordEnv.Value).To(BeEmpty(), "REDIS_PASSWORD must not have a literal value")
			Expect(passwordEnv.ValueFrom).NotTo(BeNil())
			Expect(passwordEnv.ValueFrom.SecretKeyRef).NotTo(BeNil())
			Expect(passwordEnv.ValueFrom.SecretKeyRef.Name).To(Equal(rdb.Name + "-admin"))
			Expect(passwordEnv.ValueFrom.SecretKeyRef.Key).To(Equal("REDIS_PASSWORD"))
		})

		It("should set owner references on the StatefulSet and Service", func() {
			var svc corev1.Service
			Expect(K8sClient.Get(Ctx, lookup, &svc)).To(Succeed())
			Expect(svc.OwnerReferences).To(HaveLen(1))
			Expect(svc.OwnerReferences[0].Name).To(Equal(rdb.Name))

			var sts appsv1.StatefulSet
			Expect(K8sClient.Get(Ctx, lookup, &sts)).To(Succeed())
			Expect(sts.OwnerReferences).To(HaveLen(1))
			Expect(sts.OwnerReferences[0].Name).To(Equal(rdb.Name))
		})

		It("should add the finalizer to the CR", func() {
			var fetched v1alpha1.RedisDatabase
			Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
			Expect(fetched.Finalizers).To(ContainElement("games-hub.io/redis-database"))
		})

		It("should create an admin Secret with username and password keys", func() {
			var secret corev1.Secret
			Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey("REDIS_USERNAME"))
			Expect(secret.Data).To(HaveKey("REDIS_PASSWORD"))
			Expect(string(secret.Data["REDIS_USERNAME"])).To(Equal("default"))
			Expect(string(secret.Data["REDIS_PASSWORD"])).To(HaveLen(24))
		})

		It("should set a controller owner reference on the admin Secret", func() {
			var secret corev1.Secret
			Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed())
			Expect(secret.OwnerReferences).To(HaveLen(1))
			Expect(secret.OwnerReferences[0].Name).To(Equal(rdb.Name))
			Expect(*secret.OwnerReferences[0].Controller).To(BeTrue())
		})

		It("should populate RedisDatabaseStatus.SecretName", func() {
			var fetched v1alpha1.RedisDatabase
			Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
			Expect(fetched.Status.SecretName).To(Equal(rdb.Name + "-admin"))
		})
	})

	// ── Storage resize ───────────────────────────────────────────────────────
	// Verify that increasing StorageSize destroys and recreates the database
	// with the new storage size, and returns to Ready.
	Context("when the StorageSize of a ready RedisDatabase is increased", Ordered, func() {
		var (
			ns     *corev1.Namespace
			rdb    *v1alpha1.RedisDatabase
			lookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, rdb, lookup, _ = newTestRedisResources("test-rdb")
			Expect(K8sClient.Create(Ctx, rdb)).To(Succeed())

			// Wait for the database to become Ready before attempting a resize.
			Eventually(func(g Gomega) {
				var fetched v1alpha1.RedisDatabase
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.RedisDatabasePhaseReady))
			}, Timeout, Interval).Should(Succeed())

			// Increase the storage size.
			var latest v1alpha1.RedisDatabase
			Expect(K8sClient.Get(Ctx, lookup, &latest)).To(Succeed())
			latest.Spec.StorageSize = resource.MustParse("512Mi")
			Expect(K8sClient.Update(Ctx, &latest)).To(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should update the StatefulSet VolumeClaimTemplate to the new size", func() {
			Eventually(func(g Gomega) {
				var sts appsv1.StatefulSet
				g.Expect(K8sClient.Get(Ctx, lookup, &sts)).To(Succeed())
				g.Expect(sts.Spec.VolumeClaimTemplates).To(HaveLen(1))
				vcStorage := sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
				g.Expect(vcStorage.Cmp(resource.MustParse("512Mi"))).To(Equal(0))
			}, Timeout, Interval).Should(Succeed())
		})

		It("should return to Ready phase after the resize", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.RedisDatabase
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.RedisDatabasePhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})
	})

	// ── Instance label filtering ─────────────────────────────────────────────
	Context("when a RedisDatabase has no operator-instance label", Ordered, func() {
		var (
			ns     *corev1.Namespace
			rdb    *v1alpha1.RedisDatabase
			lookup types.NamespacedName
		)

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-rdb-nolabel-",
				},
			}
			Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

			rdb = &v1alpha1.RedisDatabase{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-label-rdb",
					Namespace: ns.Name,
					// Deliberately omit the db-operator.benjamin-wright.github.com/operator-instance label.
				},
				Spec: v1alpha1.RedisDatabaseSpec{
					StorageSize: resource.MustParse("256Mi"),
				},
			}
			Expect(K8sClient.Create(Ctx, rdb)).To(Succeed())
			lookup = types.NamespacedName{Name: rdb.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, rdb)
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should never set a status phase on the CR", func() {
			Consistently(func(g Gomega) {
				var fetched v1alpha1.RedisDatabase
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(BeEmpty())
			}, 10*time.Second, Interval).Should(Succeed())
		})

		It("should not create any owned sub-resources", func() {
			var stsList appsv1.StatefulSetList
			Expect(K8sClient.List(Ctx, &stsList, client.InNamespace(ns.Name))).To(Succeed())
			Expect(stsList.Items).To(BeEmpty(), "expected no StatefulSets for unlabelled CR")

			var svcList corev1.ServiceList
			Expect(K8sClient.List(Ctx, &svcList, client.InNamespace(ns.Name))).To(Succeed())
			Expect(svcList.Items).To(BeEmpty(), "expected no Services for unlabelled CR")
		})
	})

	// ── Deletion / cleanup ───────────────────────────────────────────────────
	Context("when a RedisDatabase is deleted", Ordered, func() {
		var (
			ns           *corev1.Namespace
			rdb          *v1alpha1.RedisDatabase
			lookup       types.NamespacedName
			secretLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, rdb, lookup, secretLookup = newTestRedisResources("test-rdb")
			Expect(K8sClient.Create(Ctx, rdb)).To(Succeed())

			// Wait for all owned resources to exist.
			Eventually(func(g Gomega) {
				var sts appsv1.StatefulSet
				g.Expect(K8sClient.Get(Ctx, lookup, &sts)).To(Succeed())
				var svc corev1.Service
				g.Expect(K8sClient.Get(Ctx, lookup, &svc)).To(Succeed())
				var secret corev1.Secret
				g.Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed())
			}, Timeout, Interval).Should(Succeed())

			// Delete the CR and wait for it to be fully removed (finalizer handled).
			Expect(K8sClient.Delete(Ctx, rdb)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.RedisDatabase
				err := K8sClient.Get(Ctx, lookup, &fetched)
				g.Expect(err).To(HaveOccurred())
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			}, Timeout, Interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should cascade-delete the StatefulSet", func() {
			var sts appsv1.StatefulSet
			err := K8sClient.Get(Ctx, lookup, &sts)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should cascade-delete the Service", func() {
			var svc corev1.Service
			err := K8sClient.Get(Ctx, lookup, &svc)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should cascade-delete the admin Secret", func() {
			var secret corev1.Secret
			err := K8sClient.Get(Ctx, secretLookup, &secret)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should leave no orphaned resources with the operator-managed label", func() {
			labels := client.MatchingLabels{
				"app.kubernetes.io/managed-by": "db-operator",
				"app.kubernetes.io/instance":   rdb.Name,
			}

			var stsList appsv1.StatefulSetList
			Expect(K8sClient.List(Ctx, &stsList, client.InNamespace(ns.Name), labels)).To(Succeed())
			Expect(stsList.Items).To(BeEmpty(), fmt.Sprintf("orphaned StatefulSets: %v", stsList.Items))

			var svcList corev1.ServiceList
			Expect(K8sClient.List(Ctx, &svcList, client.InNamespace(ns.Name), labels)).To(Succeed())
			Expect(svcList.Items).To(BeEmpty(), fmt.Sprintf("orphaned Services: %v", svcList.Items))

			var secretList corev1.SecretList
			Expect(K8sClient.List(Ctx, &secretList, client.InNamespace(ns.Name), labels)).To(Succeed())
			Expect(secretList.Items).To(BeEmpty(), fmt.Sprintf("orphaned Secrets: %v", secretList.Items))
		})
	})
})
