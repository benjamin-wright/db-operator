//go:build integration

package controller_test

import (
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("RedisCredentialReconciler", func() {

	// ── Full lifecycle: create → ready → delete ──────────────────────────────
	Context("full credential lifecycle", Ordered, func() {
		var (
			ns               *corev1.Namespace
			rdb              *v1alpha1.RedisDatabase
			rcred            *v1alpha1.RedisCredential
			dbLookup         types.NamespacedName
			adminSecretKey   types.NamespacedName
			credLookup       types.NamespacedName
			credSecretLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns, rdb, dbLookup, adminSecretKey = NewRedisDatabase("rcred-lifecycle-db")
			WaitForRedisDatabase(dbLookup)

			rcred = &v1alpha1.RedisCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rcred-lifecycle",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.RedisCredentialSpec{
					DatabaseRef:   rdb.Name,
					Username:      "appuser",
					SecretName:    "rcred-lifecycle-secret",
					KeyPatterns:   []string{"cache:*"},
					ACLCategories: []v1alpha1.RedisACLCategory{v1alpha1.RedisACLCategoryRead, v1alpha1.RedisACLCategoryWrite},
				},
			}
			Expect(K8sClient.Create(Ctx, rcred)).To(Succeed())

			credLookup = types.NamespacedName{Name: rcred.Name, Namespace: ns.Name}
			credSecretLookup = types.NamespacedName{Name: rcred.Spec.SecretName, Namespace: ns.Name}
			_ = adminSecretKey
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should transition RedisCredential to Ready", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.RedisCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.RedisCredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})

		It("should populate RedisCredentialStatus.SecretName", func() {
			var fetched v1alpha1.RedisCredential
			Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
			Expect(fetched.Status.SecretName).To(Equal(rcred.Spec.SecretName))
		})

		It("should create the credential Secret with expected keys", func() {
			var secret corev1.Secret
			Expect(K8sClient.Get(Ctx, credSecretLookup, &secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey("REDIS_USERNAME"))
			Expect(secret.Data).To(HaveKey("REDIS_PASSWORD"))
			Expect(secret.Data).To(HaveKey("REDIS_HOST"))
			Expect(secret.Data).To(HaveKey("REDIS_PORT"))
			Expect(string(secret.Data["REDIS_USERNAME"])).To(Equal("appuser"))
			Expect(string(secret.Data["REDIS_PASSWORD"])).To(HaveLen(24))
			Expect(string(secret.Data["REDIS_PORT"])).To(Equal("6379"))
		})

		It("should set a controller owner reference on the credential Secret", func() {
			var secret corev1.Secret
			Expect(K8sClient.Get(Ctx, credSecretLookup, &secret)).To(Succeed())
			Expect(secret.OwnerReferences).To(HaveLen(1))
			Expect(secret.OwnerReferences[0].Name).To(Equal(rcred.Name))
			Expect(*secret.OwnerReferences[0].Controller).To(BeTrue())
		})

		It("should add the finalizer to the RedisCredential", func() {
			var fetched v1alpha1.RedisCredential
			Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
			Expect(fetched.Finalizers).To(ContainElement("games-hub.io/redis-credential"))
		})

		It("should allow reads and writes for keys matching the pattern but deny them for non-matching keys", func() {
			redisCli, close := ConnectToRedisDatabase(dbLookup, credSecretLookup)
			defer close()

			// Matching pattern — both write and read should succeed.
			Expect(redisCli.Set(Ctx, "cache:foo", "bar", 0).Err()).To(Succeed())
			val, err := redisCli.Get(Ctx, "cache:foo").Result()
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal("bar"))

			// Non-matching pattern — write should be denied.
			Expect(redisCli.Set(Ctx, "other:foo", "bar", 0).Err()).To(MatchError(ContainSubstring("NOPERM")))

			// Non-matching pattern — read should be denied.
			Expect(redisCli.Get(Ctx, "other:foo").Err()).To(MatchError(ContainSubstring("NOPERM")))
		})
	})

	// ── Dependency-wait behaviour ────────────────────────────────────────────
	Context("when the target database is not yet Ready", Ordered, func() {
		var (
			ns         *corev1.Namespace
			rcred      *v1alpha1.RedisCredential
			credLookup types.NamespacedName
			dbLookup   types.NamespacedName
		)

		BeforeAll(func() {
			ns, _, dbLookup, _ = NewRedisDatabase("rcred-wait-db")

			rcred = &v1alpha1.RedisCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rcred-wait",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.RedisCredentialSpec{
					DatabaseRef:   "rcred-wait-db",
					Username:      "waituser",
					SecretName:    "rcred-wait-secret",
					ACLCategories: []v1alpha1.RedisACLCategory{v1alpha1.RedisACLCategoryAll},
				},
			}
			Expect(K8sClient.Create(Ctx, rcred)).To(Succeed())
			credLookup = types.NamespacedName{Name: rcred.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should remain Pending while the database is not Ready", func() {
			Eventually(func(g Gomega) {
				var fetched v1alpha1.RedisCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.RedisCredentialPhasePending))
			}, Timeout, Interval).Should(Succeed())
		})

		It("should transition to Ready once the database becomes Ready", func() {
			WaitForRedisDatabase(dbLookup)

			Eventually(func(g Gomega) {
				var fetched v1alpha1.RedisCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.RedisCredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())
		})
	})

	// ── Deletion / cleanup ───────────────────────────────────────────────────
	Context("when a RedisCredential is deleted", Ordered, func() {
		var (
			ns                *corev1.Namespace
			rdb               *v1alpha1.RedisDatabase
			rcred             *v1alpha1.RedisCredential
			dbLookup          types.NamespacedName
			adminSecretLookup types.NamespacedName
			credLookup        types.NamespacedName
			credSecretLookup  types.NamespacedName
		)

		BeforeAll(func() {
			ns, rdb, dbLookup, adminSecretLookup = NewRedisDatabase("rcred-delete-db")
			WaitForRedisDatabase(dbLookup)

			rcred = &v1alpha1.RedisCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rcred-delete",
					Namespace: ns.Name,
					Labels: map[string]string{
						"db-operator.benjamin-wright.github.com/operator-instance": "test",
					},
				},
				Spec: v1alpha1.RedisCredentialSpec{
					DatabaseRef:   rdb.Name,
					Username:      "deleteuser",
					SecretName:    "rcred-delete-secret",
					ACLCategories: []v1alpha1.RedisACLCategory{v1alpha1.RedisACLCategoryRead},
				},
			}
			Expect(K8sClient.Create(Ctx, rcred)).To(Succeed())
			credLookup = types.NamespacedName{Name: rcred.Name, Namespace: ns.Name}
			credSecretLookup = types.NamespacedName{Name: rcred.Spec.SecretName, Namespace: ns.Name}

			Eventually(func(g Gomega) {
				var fetched v1alpha1.RedisCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.RedisCredentialPhaseReady))
			}, Timeout, Interval).Should(Succeed())

			Expect(K8sClient.Delete(Ctx, rcred)).To(Succeed())
			Eventually(func(g Gomega) {
				var fetched v1alpha1.RedisCredential
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

		It("should drop the Redis ACL user", func() {
			redisCli, close := ConnectToRedisDatabase(dbLookup, adminSecretLookup)
			defer close()

			_, err := redisCli.Do(Ctx, "ACL", "GETUSER", "deleteuser").Result()
			Expect(err).To(Equal(goredis.Nil), "Redis ACL user 'deleteuser' should have been removed")
		})

		It("should leave no orphaned credential Secrets", func() {
			labels := client.MatchingLabels{
				"app.kubernetes.io/managed-by": "db-operator",
				"app.kubernetes.io/instance":   rcred.Name,
			}

			var secretList corev1.SecretList
			Expect(K8sClient.List(Ctx, &secretList, client.InNamespace(ns.Name), labels)).To(Succeed())
			Expect(secretList.Items).To(BeEmpty(), fmt.Sprintf("orphaned Secrets: %v", secretList.Items))
		})
	})

	// ── Instance label filtering ─────────────────────────────────────────────
	Context("when a RedisCredential has no operator-instance label", Ordered, func() {
		var (
			ns         *corev1.Namespace
			rcred      *v1alpha1.RedisCredential
			credLookup types.NamespacedName
		)

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-rcred-nolabel-",
				},
			}
			Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

			rcred = &v1alpha1.RedisCredential{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-label-rcred",
					Namespace: ns.Name,
					// Deliberately omit the db-operator.benjamin-wright.github.com/operator-instance label.
				},
				Spec: v1alpha1.RedisCredentialSpec{
					DatabaseRef:   "nonexistent-db",
					Username:      "nolabeluser",
					SecretName:    "no-label-rcred-secret",
					ACLCategories: []v1alpha1.RedisACLCategory{v1alpha1.RedisACLCategoryRead},
				},
			}
			Expect(K8sClient.Create(Ctx, rcred)).To(Succeed())
			credLookup = types.NamespacedName{Name: rcred.Name, Namespace: ns.Name}
		})

		AfterAll(func() {
			_ = K8sClient.Delete(Ctx, rcred)
			_ = K8sClient.Delete(Ctx, ns)
		})

		It("should never set a status phase on the CR", func() {
			Consistently(func(g Gomega) {
				var fetched v1alpha1.RedisCredential
				g.Expect(K8sClient.Get(Ctx, credLookup, &fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(BeEmpty())
			}, 10*time.Second, Interval).Should(Succeed())
		})

		It("should not create the credential Secret", func() {
			var secretList corev1.SecretList
			Expect(K8sClient.List(Ctx, &secretList, client.InNamespace(ns.Name))).To(Succeed())
			for _, s := range secretList.Items {
				Expect(s.Name).NotTo(Equal("no-label-rcred-secret"), "credential Secret should not exist for unlabelled CR")
			}
		})
	})
})
