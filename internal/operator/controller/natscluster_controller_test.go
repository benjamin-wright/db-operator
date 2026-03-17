//go:build integration

package controller_test

import (
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

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
)

// newTestNatsClusterResources creates a unique namespace and a NatsCluster CR inside it.
// It does NOT create the CR — callers do that so they can control the exact moment of
// creation relative to their assertions.
func newTestNatsClusterResources(name, version string) (ns *corev1.Namespace, nats *v1alpha1.NatsCluster, lookup, cfgLookup types.NamespacedName) {
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-nats-",
		},
	}
	Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

	nats = &v1alpha1.NatsCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns.Name,
			Labels: map[string]string{
				"games-hub.io/operator-instance": "test",
			},
		},
		Spec: v1alpha1.NatsClusterSpec{
			NatsVersion: version,
		},
	}
	lookup = types.NamespacedName{Name: nats.Name, Namespace: ns.Name}
	cfgLookup = types.NamespacedName{Name: nats.Name + "-config", Namespace: ns.Name}
	return
}

var _ = Describe("NatsClusterReconciler", func() {

	// ── Phase lifecycle ──────────────────────────────────────────────────────
	Context("phase lifecycle", Ordered, func() {
		var (
			ns     *corev1.Namespace
			nats   *v1alpha1.NatsCluster
			lookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, nats, lookup, _ = newTestNatsClusterResources("test-nats", "2.10")
			Expect(K8sClient.Create(Ctx, nats)).To(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should initially set status phase to Pending before the Deployment is ready", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.NatsCluster
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsClusterPhasePending))
			}, Timeout, Interval).Should(Succeed())
		})

		It("should transition to Ready when the Deployment has ready replicas", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.NatsCluster
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsClusterPhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})
	})

	// ── Steady-state resource properties ────────────────────────────────────
	Context("when a NatsCluster is created and becomes Ready", Ordered, func() {
		var (
			ns        *corev1.Namespace
			nats      *v1alpha1.NatsCluster
			lookup    types.NamespacedName
			cfgLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, nats, lookup, cfgLookup = newTestNatsClusterResources("test-nats", "2.10")
			Expect(K8sClient.Create(Ctx, nats)).To(Succeed())

			// Wait until all owned resources exist and the instance is ready.
			Eventually(func(g Gomega) {
				var deploy appsv1.Deployment
				g.Expect(K8sClient.Get(Ctx, lookup, &deploy)).To(Succeed())
				var svc corev1.Service
				g.Expect(K8sClient.Get(Ctx, lookup, &svc)).To(Succeed())
				var cm corev1.ConfigMap
				g.Expect(K8sClient.Get(Ctx, cfgLookup, &cm)).To(Succeed())
				var fetched v1alpha1.NatsCluster
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsClusterPhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should create a Deployment using the nats:2.10 image with one replica", func() {
			var deploy appsv1.Deployment
			Expect(K8sClient.Get(Ctx, lookup, &deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal("nats:2.10"))
			Expect(*deploy.Spec.Replicas).To(Equal(int32(1)))
		})

		It("should pass --config pointing at the mounted nats.conf as container args", func() {
			var deploy appsv1.Deployment
			Expect(K8sClient.Get(Ctx, lookup, &deploy)).To(Succeed())
			args := deploy.Spec.Template.Spec.Containers[0].Args
			Expect(args).To(ContainElements("--config", "/etc/nats/nats.conf"))
		})

		It("should mount the config ConfigMap into the container at /etc/nats", func() {
			var deploy appsv1.Deployment
			Expect(K8sClient.Get(Ctx, lookup, &deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers[0].VolumeMounts).To(ContainElement(
				HaveField("MountPath", "/etc/nats"),
			))
		})

		It("should expose the client port 4222 on the container", func() {
			var deploy appsv1.Deployment
			Expect(K8sClient.Get(Ctx, lookup, &deploy)).To(Succeed())
			ports := deploy.Spec.Template.Spec.Containers[0].Ports
			Expect(ports).To(ContainElement(HaveField("ContainerPort", int32(4222))))
		})

		It("should create a Service with client port 4222", func() {
			var svc corev1.Service
			Expect(K8sClient.Get(Ctx, lookup, &svc)).To(Succeed())
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(4222)))
		})

		It("should create a ConfigMap containing a nats.conf key", func() {
			var cm corev1.ConfigMap
			Expect(K8sClient.Get(Ctx, cfgLookup, &cm)).To(Succeed())
			Expect(cm.Data).To(HaveKey("nats.conf"))
		})

		It("should add the finalizer to the CR", func() {
			var fetched v1alpha1.NatsCluster
			Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
			Expect(fetched.Finalizers).To(ContainElement("games-hub.io/nats-cluster"))
		})

		It("should set controller owner references on the Deployment, Service, and ConfigMap", func() {
			var deploy appsv1.Deployment
			Expect(K8sClient.Get(Ctx, lookup, &deploy)).To(Succeed())
			Expect(deploy.OwnerReferences).To(HaveLen(1))
			Expect(deploy.OwnerReferences[0].Name).To(Equal(nats.Name))

			var svc corev1.Service
			Expect(K8sClient.Get(Ctx, lookup, &svc)).To(Succeed())
			Expect(svc.OwnerReferences).To(HaveLen(1))
			Expect(svc.OwnerReferences[0].Name).To(Equal(nats.Name))

			var cm corev1.ConfigMap
			Expect(K8sClient.Get(Ctx, cfgLookup, &cm)).To(Succeed())
			Expect(cm.OwnerReferences).To(HaveLen(1))
			Expect(cm.OwnerReferences[0].Name).To(Equal(nats.Name))
		})
	})

	// ── JetStream ────────────────────────────────────────────────────────────
	Context("when JetStream is enabled", Ordered, func() {
		var (
			ns        *corev1.Namespace
			nats      *v1alpha1.NatsCluster
			lookup    types.NamespacedName
			pvcLookup types.NamespacedName
			cfgLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "test-nats-js-"},
			}
			Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

			nats = &v1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nats-js",
					Namespace: ns.Name,
					Labels: map[string]string{
						"games-hub.io/operator-instance": "test",
					},
				},
				Spec: v1alpha1.NatsClusterSpec{
					NatsVersion: "2.10",
					JetStream: &v1alpha1.NatsJetStreamConfig{
						StorageSize: resource.MustParse("512Mi"),
					},
				},
			}
			Expect(K8sClient.Create(Ctx, nats)).To(Succeed())
			lookup = types.NamespacedName{Name: nats.Name, Namespace: ns.Name}
			pvcLookup = types.NamespacedName{Name: nats.Name + "-jetstream", Namespace: ns.Name}
			cfgLookup = types.NamespacedName{Name: nats.Name + "-config", Namespace: ns.Name}

			Eventually(func(g Gomega) {
				var pvc corev1.PersistentVolumeClaim
				g.Expect(K8sClient.Get(Ctx, pvcLookup, &pvc)).To(Succeed())
				var fetched v1alpha1.NatsCluster
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsClusterPhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should create a PVC with the requested storage size", func() {
			var pvc corev1.PersistentVolumeClaim
			Expect(K8sClient.Get(Ctx, pvcLookup, &pvc)).To(Succeed())
			storage := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
			Expect(storage.Cmp(resource.MustParse("512Mi"))).To(Equal(0))
		})

		It("should mount the JetStream PVC into the container at /data", func() {
			var deploy appsv1.Deployment
			Expect(K8sClient.Get(Ctx, lookup, &deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers[0].VolumeMounts).To(ContainElement(
				HaveField("MountPath", "/data"),
			))
		})

		It("should include a jetstream block in the nats.conf ConfigMap", func() {
			var cm corev1.ConfigMap
			Expect(K8sClient.Get(Ctx, cfgLookup, &cm)).To(Succeed())
			Expect(cm.Data["nats.conf"]).To(ContainSubstring("jetstream"))
		})
	})

	// ── Instance label filtering ─────────────────────────────────────────────
	Context("when a NatsCluster has no operator-instance label", Ordered, func() {
		var (
			ns     *corev1.Namespace
			nats   *v1alpha1.NatsCluster
			lookup types.NamespacedName
		)

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "test-nats-nolabel-"},
			}
			Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

			nats = &v1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-label-nats",
					Namespace: ns.Name,
					// Deliberately omit the games-hub.io/operator-instance label.
				},
				Spec: v1alpha1.NatsClusterSpec{
					NatsVersion: "2.10",
				},
			}
			Expect(K8sClient.Create(Ctx, nats)).To(Succeed())
			lookup = types.NamespacedName{Name: nats.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, nats)
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should never set a status phase on the CR", func() {
			Consistently(func(g Gomega) {
				var fetched v1alpha1.NatsCluster
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(BeEmpty())
			}, 10*time.Second, Interval).Should(Succeed())
		})

		It("should not create any owned sub-resources", func() {
			var deployList appsv1.DeploymentList
			Expect(K8sClient.List(Ctx, &deployList, client.InNamespace(ns.Name))).To(Succeed())
			Expect(deployList.Items).To(BeEmpty(), "expected no Deployments for unlabelled CR")

			var svcList corev1.ServiceList
			Expect(K8sClient.List(Ctx, &svcList, client.InNamespace(ns.Name))).To(Succeed())
			Expect(svcList.Items).To(BeEmpty(), "expected no Services for unlabelled CR")
		})
	})

	// ── Deletion / cleanup ───────────────────────────────────────────────────
	Context("when a NatsCluster is deleted", Ordered, func() {
		var (
			ns        *corev1.Namespace
			nats      *v1alpha1.NatsCluster
			lookup    types.NamespacedName
			cfgLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, nats, lookup, cfgLookup = newTestNatsClusterResources("test-nats", "2.10")
			Expect(K8sClient.Create(Ctx, nats)).To(Succeed())

			// Wait for all owned resources to exist.
			Eventually(func(g Gomega) {
				var deploy appsv1.Deployment
				g.Expect(K8sClient.Get(Ctx, lookup, &deploy)).To(Succeed())
				var svc corev1.Service
				g.Expect(K8sClient.Get(Ctx, lookup, &svc)).To(Succeed())
				var cm corev1.ConfigMap
				g.Expect(K8sClient.Get(Ctx, cfgLookup, &cm)).To(Succeed())
			}, Timeout, Interval).Should(Succeed())

			// Delete the CR and wait for it to be fully removed (finalizer handled).
			Expect(K8sClient.Delete(Ctx, nats)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.NatsCluster
				err := K8sClient.Get(Ctx, lookup, &fetched)
				g.Expect(err).To(HaveOccurred())
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			}, Timeout, Interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should delete the Deployment", func() {
			var deploy appsv1.Deployment
			err := K8sClient.Get(Ctx, lookup, &deploy)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should delete the Service", func() {
			var svc corev1.Service
			err := K8sClient.Get(Ctx, lookup, &svc)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should delete the ConfigMap", func() {
			var cm corev1.ConfigMap
			err := K8sClient.Get(Ctx, cfgLookup, &cm)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})
	})
})
