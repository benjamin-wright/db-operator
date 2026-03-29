//go:build integration

package controller_test

import (
	"time"

	nats "github.com/nats-io/nats.go"

	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("NatsAccountReconciler", func() {

	// ── Pending while cluster is absent ─────────────────────────────────────
	Context("when the referenced NatsCluster does not exist", Ordered, func() {
		var (
			ns     *corev1.Namespace
			acct   *v1alpha1.NatsAccount
			lookup types.NamespacedName
		)

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "test-natsacct-nocluster-"},
			}
			Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

			acct = &v1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "acct-pending",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.NatsAccountSpec{
					ClusterRef: "nonexistent-cluster",
				},
			}
			Expect(K8sClient.Create(Ctx, acct)).To(Succeed())
			lookup = types.NamespacedName{Name: acct.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should set status phase to Pending", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.NatsAccount
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsAccountPhasePending))
			}, Timeout, Interval).Should(Succeed())
		})
	})

	// ── Full lifecycle ───────────────────────────────────────────────────────
	Context("when a NatsAccount is created with a ready cluster", Ordered, func() {
		var (
			ns            *corev1.Namespace
			nats          *v1alpha1.NatsCluster
			acct          *v1alpha1.NatsAccount
			clusterLookup types.NamespacedName
			acctLookup    types.NamespacedName
			secretLookup  types.NamespacedName
		)

		BeforeAll(func() {
			ns, nats, clusterLookup = NewNatsCluster("acct-lifecycle-cluster")
			WaitForNatsCluster(clusterLookup)

			acct = &v1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "acct-lifecycle",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.NatsAccountSpec{
					ClusterRef: nats.Name,
					Users: []v1alpha1.NatsUser{
						{Username: "alice", SecretName: "acct-lifecycle-alice"},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, acct)).To(Succeed())
			acctLookup = types.NamespacedName{Name: acct.Name, Namespace: ns.Name}
			secretLookup = types.NamespacedName{Name: "acct-lifecycle-alice", Namespace: ns.Name}

			Eventually(func(g Gomega) {
				var fetched v1alpha1.NatsAccount
				g.Expect(K8sClient.Get(Ctx, acctLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsAccountPhaseReady))
				var secret corev1.Secret
				g.Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed())
			}, Timeout, Interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should set status phase to Ready", func() {
			var fetched v1alpha1.NatsAccount
			Expect(K8sClient.Get(Ctx, acctLookup, &fetched)).To(Succeed())
			Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsAccountPhaseReady))
		})

		It("should create a user credential Secret with all expected keys", func() {
			var secret corev1.Secret
			Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey("NATS_USERNAME"))
			Expect(secret.Data).To(HaveKey("NATS_PASSWORD"))
			Expect(secret.Data).To(HaveKey("NATS_ACCOUNT"))
			Expect(secret.Data).To(HaveKey("NATS_HOST"))
			Expect(secret.Data).To(HaveKey("NATS_PORT"))
		})

		It("should write correct values into the user credential Secret", func() {
			var secret corev1.Secret
			Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed())
			Expect(string(secret.Data["NATS_USERNAME"])).To(Equal("alice"))
			Expect(string(secret.Data["NATS_PASSWORD"])).To(HaveLen(24))
			Expect(string(secret.Data["NATS_ACCOUNT"])).To(Equal(acct.Name))
			Expect(string(secret.Data["NATS_HOST"])).To(ContainSubstring(nats.Name))
			Expect(string(secret.Data["NATS_PORT"])).To(Equal("4222"))
		})

		It("should set a controller owner reference on the user credential Secret", func() {
			var secret corev1.Secret
			Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed())
			Expect(secret.OwnerReferences).To(HaveLen(1))
			Expect(secret.OwnerReferences[0].Name).To(Equal(acct.Name))
			Expect(*secret.OwnerReferences[0].Controller).To(BeTrue())
		})
	})

	// ── Multiple users ───────────────────────────────────────────────────────
	Context("when a NatsAccount has multiple users", Ordered, func() {
		var (
			ns            *corev1.Namespace
			nats          *v1alpha1.NatsCluster
			clusterLookup types.NamespacedName
			acctLookup    types.NamespacedName
		)

		BeforeAll(func() {
			ns, nats, clusterLookup = NewNatsCluster("multi-user-cluster")
			WaitForNatsCluster(clusterLookup)

			acct := &v1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-user-acct",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.NatsAccountSpec{
					ClusterRef: nats.Name,
					Users: []v1alpha1.NatsUser{
						{Username: "alice", SecretName: "multi-user-alice"},
						{Username: "bob", SecretName: "multi-user-bob"},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, acct)).To(Succeed())
			acctLookup = types.NamespacedName{Name: acct.Name, Namespace: ns.Name}

			Eventually(func(g Gomega) {
				var fetched v1alpha1.NatsAccount
				g.Expect(K8sClient.Get(Ctx, acctLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsAccountPhaseReady))
				var aliceSecret corev1.Secret
				g.Expect(K8sClient.Get(Ctx, types.NamespacedName{Name: "multi-user-alice", Namespace: ns.Name}, &aliceSecret)).To(Succeed())
				var bobSecret corev1.Secret
				g.Expect(K8sClient.Get(Ctx, types.NamespacedName{Name: "multi-user-bob", Namespace: ns.Name}, &bobSecret)).To(Succeed())
			}, Timeout, Interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should create a credential Secret for each user", func() {
			var aliceSecret corev1.Secret
			Expect(K8sClient.Get(Ctx, types.NamespacedName{Name: "multi-user-alice", Namespace: ns.Name}, &aliceSecret)).To(Succeed())
			Expect(string(aliceSecret.Data["NATS_USERNAME"])).To(Equal("alice"))

			var bobSecret corev1.Secret
			Expect(K8sClient.Get(Ctx, types.NamespacedName{Name: "multi-user-bob", Namespace: ns.Name}, &bobSecret)).To(Succeed())
			Expect(string(bobSecret.Data["NATS_USERNAME"])).To(Equal("bob"))
		})
	})

	// ── Instance label filtering ─────────────────────────────────────────────
	Context("when a NatsAccount has no operator-instance label", Ordered, func() {
		var (
			ns     *corev1.Namespace
			acct   *v1alpha1.NatsAccount
			lookup types.NamespacedName
		)

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "test-natsacct-nolabel-"},
			}
			Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

			acct = &v1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-label-acct",
					Namespace: ns.Name,
					// Deliberately omit the db-operator.benjamin-wright.github.com/operator-instance label.
				},
				Spec: v1alpha1.NatsAccountSpec{
					ClusterRef: "some-cluster",
					Users: []v1alpha1.NatsUser{
						{Username: "alice", SecretName: "no-label-alice"},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, acct)).To(Succeed())
			lookup = types.NamespacedName{Name: acct.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, acct)
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should never set a status phase on the CR", func() {
			Consistently(func(g Gomega) {
				var fetched v1alpha1.NatsAccount
				g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(BeEmpty())
			}, 10*time.Second, Interval).Should(Succeed())
		})

		It("should not create any credential Secrets", func() {
			var secretList corev1.SecretList
			Expect(K8sClient.List(Ctx, &secretList, client.InNamespace(ns.Name))).To(Succeed())
			Expect(secretList.Items).To(BeEmpty(), "expected no Secrets for unlabelled CR")
		})
	})

	// ── Deletion / secret cleanup ────────────────────────────────────────────
	Context("when a NatsAccount is deleted", Ordered, func() {
		var (
			ns            *corev1.Namespace
			nats          *v1alpha1.NatsCluster
			acct          *v1alpha1.NatsAccount
			clusterLookup types.NamespacedName
			acctLookup    types.NamespacedName
			secretLookup  types.NamespacedName
		)

		BeforeAll(func() {
			ns, nats, clusterLookup = NewNatsCluster("acct-delete-cluster")
			WaitForNatsCluster(clusterLookup)

			acct = &v1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "acct-delete",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.NatsAccountSpec{
					ClusterRef: nats.Name,
					Users: []v1alpha1.NatsUser{
						{Username: "alice", SecretName: "acct-delete-alice"},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, acct)).To(Succeed())
			acctLookup = types.NamespacedName{Name: acct.Name, Namespace: ns.Name}
			secretLookup = types.NamespacedName{Name: "acct-delete-alice", Namespace: ns.Name}

			Eventually(func(g Gomega) {
				var secret corev1.Secret
				g.Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed())
			}, Timeout, Interval).Should(Succeed())

			Expect(K8sClient.Delete(Ctx, acct)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.NatsAccount
				err := K8sClient.Get(Ctx, acctLookup, &fetched)
				g.Expect(err).To(HaveOccurred())
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			}, Timeout, Interval).Should(Succeed())
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should cascade-delete the user credential Secret", func() {
			Eventually(func(g Gomega) {
				var secret corev1.Secret
				err := K8sClient.Get(Ctx, secretLookup, &secret)
				g.Expect(err).To(HaveOccurred())
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			}, Timeout, Interval).Should(Succeed())
		})
	})

	// ── Connectivity ─────────────────────────────────────────────────────────
	Context("when a user connects with the generated credentials", Ordered, func() {
		var (
			ns            *corev1.Namespace
			natsCluster   *v1alpha1.NatsCluster
			clusterLookup types.NamespacedName
			secretLookup  types.NamespacedName
		)

		BeforeAll(func() {
			ns, natsCluster, clusterLookup = NewNatsCluster("conn-cluster")

			acct := &v1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "conn-acct",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.NatsAccountSpec{
					ClusterRef: natsCluster.Name,
					Users: []v1alpha1.NatsUser{
						{Username: "alice", SecretName: "conn-alice"},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, acct)).To(Succeed())
			acctLookup := types.NamespacedName{Name: acct.Name, Namespace: ns.Name}
			secretLookup = types.NamespacedName{Name: "conn-alice", Namespace: ns.Name}

			// Wait for the account to be Ready (secret provisioned), then wait for
			// the cluster's rolling restart (triggered by the config update) to
			// complete before attempting any connections.
			Eventually(func(g Gomega) {
				var fetched v1alpha1.NatsAccount
				g.Expect(K8sClient.Get(Ctx, acctLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsAccountPhaseReady))
			}, Timeout, Interval).Should(Succeed())
			WaitForNatsCluster(clusterLookup)
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should authenticate successfully and round-trip a message on any subject", func() {
			nc, close := ConnectToNats(clusterLookup, secretLookup)
			defer close()

			received := make(chan []byte, 1)
			_, err := nc.Subscribe("test.subject", func(msg *nats.Msg) {
				received <- msg.Data
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(nc.Flush()).To(Succeed())

			Expect(nc.Publish("test.subject", []byte("hello nats"))).To(Succeed())

			Eventually(received, 5*time.Second, Interval).Should(Receive(Equal([]byte("hello nats"))))
		})
	})

	// ── Publish subject permissions ───────────────────────────────────────────
	Context("when a user has publish subject permissions", Ordered, func() {
		var (
			ns            *corev1.Namespace
			natsCluster   *v1alpha1.NatsCluster
			clusterLookup types.NamespacedName
			secretLookup  types.NamespacedName
		)

		BeforeAll(func() {
			ns, natsCluster, clusterLookup = NewNatsCluster("perm-cluster")

			acct := &v1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "perm-acct",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.NatsAccountSpec{
					ClusterRef: natsCluster.Name,
					Users: []v1alpha1.NatsUser{
						{
							Username:   "alice",
							SecretName: "perm-alice",
							Permissions: &v1alpha1.NatsUserPermissions{
								Publish: &v1alpha1.NatsSubjectPermission{
									Allow: []string{"events.*"},
								},
							},
						},
					},
				},
			}
			Expect(K8sClient.Create(Ctx, acct)).To(Succeed())
			acctLookup := types.NamespacedName{Name: acct.Name, Namespace: ns.Name}
			secretLookup = types.NamespacedName{Name: "perm-alice", Namespace: ns.Name}

			Eventually(func(g Gomega) {
				var fetched v1alpha1.NatsAccount
				g.Expect(K8sClient.Get(Ctx, acctLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsAccountPhaseReady))
			}, Timeout, Interval).Should(Succeed())
			WaitForNatsCluster(clusterLookup)
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should deliver messages published to an allowed subject", func() {
			nc, close := ConnectToNats(clusterLookup, secretLookup)
			defer close()

			received := make(chan []byte, 1)
			_, err := nc.Subscribe("events.test", func(msg *nats.Msg) {
				received <- msg.Data
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(nc.Flush()).To(Succeed())

			Expect(nc.Publish("events.test", []byte("allowed"))).To(Succeed())

			Eventually(received, 5*time.Second, Interval).Should(Receive(Equal([]byte("allowed"))))
		})

		It("should receive a permission violation when publishing to a subject outside the allow list", func() {
			permErr := make(chan error, 1)
			nc, close := ConnectToNats(clusterLookup, secretLookup,
				nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
					select {
					case permErr <- err:
					default:
					}
				}),
			)
			defer close()

			Expect(nc.Publish("other.subject", []byte("denied"))).To(Succeed())
			Expect(nc.Flush()).To(Succeed())

			Eventually(permErr, 5*time.Second, Interval).Should(Receive(MatchError(ContainSubstring("Permissions Violation"))))
		})
	})
})
