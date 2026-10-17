//go:build integration

package controller_test

import (
	"crypto/sha256"
	"fmt"
	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	api "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"time"
)

var _ = Describe("RedisCredentialReconciler effective policy", func() {
	It("repairs wildcard-writer tampering and does not trust replayed policy records for session revocation", func() {
		ns, db, dbKey, adminKey := NewRedisDatabase("redis-owner-tamper")
		DeferCleanup(func() { _ = K8sClient.Delete(Ctx, ns) })
		WaitForRedisDatabase(dbKey)
		credential := &api.RedisCredential{ObjectMeta: metav1.ObjectMeta{Namespace: ns.Name, Name: "wildcard", Labels: map[string]string{"db-operator.benjamin-wright.github.com/operator-instance": "test"}}, Spec: api.RedisCredentialSpec{DatabaseRef: db.Name, Username: "wildcard", SecretName: "wildcard-secret", KeyPatterns: []string{"*"}, Commands: []string{"get", "set", "hset", "hgetall", "del", "ping", "client|id"}}}
		Expect(K8sClient.Create(Ctx, credential)).To(Succeed())
		key := types.NamespacedName{Namespace: ns.Name, Name: credential.Name}
		wait := func() {
			Eventually(func(g Gomega) {
				g.Expect(K8sClient.Get(Ctx, key, credential)).To(Succeed())
				g.Expect(credential.Status.Phase).To(Equal(api.RedisCredentialPhaseReady))
				condition := apimeta.FindStatusCondition(credential.Status.Conditions, "PolicyEnforced")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(condition.ObservedGeneration).To(Equal(credential.Generation))
			}, Timeout, Interval).Should(Succeed())
		}
		wait()
		original := credential.Spec.DeepCopy()
		originalCondition := *apimeta.FindStatusCondition(credential.Status.Conditions, "PolicyEnforced")
		user, closeUser := ConnectToRedisDatabase(dbKey, types.NamespacedName{Namespace: ns.Name, Name: credential.Spec.SecretName})
		defer closeUser()
		admin, closeAdmin := ConnectToRedisDatabase(dbKey, adminKey)
		defer closeAdmin()
		ownerKey := fmt.Sprintf("__dbop_control:credential:%x", sha256.Sum256([]byte(credential.Spec.Username)))
		originalRecord, err := user.HGetAll(Ctx, ownerKey).Result()
		Expect(err).NotTo(HaveOccurred())
		Expect(originalRecord["signature"]).NotTo(BeEmpty())
		Expect(user.Set(Ctx, "safe/retained", "value", 0).Err()).To(Succeed())
		Expect(user.HSet(Ctx, ownerKey, "uid", "forged-owner", "generation", "999999", "policy", "forged", "signature", "forged").Err()).To(Succeed())
		credential.Spec.KeyPatterns = []string{"safe/*"}
		credential.Spec.Commands = []string{"get", "ping", "client|id"}
		Expect(K8sClient.Update(Ctx, credential)).To(Succeed())
		wait()
		Expect(user.Set(Ctx, "safe/retained", "forbidden", 0).Err()).To(HaveOccurred())
		Expect(user.Get(Ctx, "safe/retained").Result()).To(Equal("value"))
		repaired, err := admin.HGetAll(Ctx, ownerKey).Result()
		Expect(err).NotTo(HaveOccurred())
		Expect(repaired["uid"]).To(Equal(string(credential.UID)))
		Expect(repaired["signature"]).NotTo(Equal("forged"))

		held := user.Conn()
		defer held.Close()
		id, err := held.ClientID(Ctx).Result()
		Expect(err).NotTo(HaveOccurred())
		Expect(admin.HSet(Ctx, ownerKey, originalRecord).Err()).To(Succeed())
		apimeta.SetStatusCondition(&credential.Status.Conditions, originalCondition)
		Expect(K8sClient.Status().Update(Ctx, credential)).To(Succeed())
		credential.Spec = *original
		Expect(K8sClient.Update(Ctx, credential)).To(Succeed())
		wait()
		clients, err := admin.ClientList(Ctx).Result()
		Expect(err).NotTo(HaveOccurred())
		Expect(clients).NotTo(ContainSubstring("id=" + fmt.Sprint(id) + " "))

		Expect(user.Del(Ctx, ownerKey).Err()).To(Succeed())
		var secret corev1.Secret
		Expect(K8sClient.Get(Ctx, types.NamespacedName{Namespace: ns.Name, Name: credential.Spec.SecretName}, &secret)).To(Succeed())
		secret.Annotations = map[string]string{"test-reconcile": "missing-owner-record"}
		Expect(K8sClient.Update(Ctx, &secret)).To(Succeed())
		Eventually(func() string { return admin.HGet(Ctx, ownerKey, "signature").Val() }, Timeout, Interval).ShouldNot(BeEmpty())
		Expect(K8sClient.Delete(Ctx, credential)).To(Succeed())
		Eventually(func() bool { return apierrors.IsNotFound(K8sClient.Get(Ctx, key, &api.RedisCredential{})) }, time.Minute, Interval).Should(BeTrue())
	})

	It("revokes old passwords, key scopes, writes and connected sessions while retaining values", func() {
		ns, db, dbKey, adminKey := NewRedisDatabase("redis-policy")
		DeferCleanup(func() { _ = K8sClient.Delete(Ctx, ns) })
		WaitForRedisDatabase(dbKey)
		credential := &api.RedisCredential{ObjectMeta: metav1.ObjectMeta{Namespace: ns.Name, Name: "policy", Labels: map[string]string{"db-operator.benjamin-wright.github.com/operator-instance": "test"}}, Spec: api.RedisCredentialSpec{DatabaseRef: db.Name, Username: "policy", SecretName: "policy-secret", KeyPatterns: []string{"app/*", "other/*"}, Commands: []string{"get", "set", "ping", "client|id"}}}
		Expect(K8sClient.Create(Ctx, credential)).To(Succeed())
		key := types.NamespacedName{Namespace: ns.Name, Name: credential.Name}
		wait := func() {
			Eventually(func(g Gomega) {
				g.Expect(K8sClient.Get(Ctx, key, credential)).To(Succeed())
				g.Expect(credential.Status.Phase).To(Equal(api.RedisCredentialPhaseReady))
				c := apimeta.FindStatusCondition(credential.Status.Conditions, "PolicyEnforced")
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.ObservedGeneration).To(Equal(credential.Generation))
				g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
			}, Timeout, Interval).Should(Succeed())
		}
		wait()
		user, closeUser := ConnectToRedisDatabase(dbKey, types.NamespacedName{Namespace: ns.Name, Name: credential.Spec.SecretName})
		defer closeUser()
		held := user.Conn()
		defer held.Close()
		Expect(held.Set(Ctx, "app/key", "retained", 0).Err()).To(Succeed())
		Expect(held.Set(Ctx, "other/key", "hidden", 0).Err()).To(Succeed())
		id, err := held.ClientID(Ctx).Result()
		Expect(err).NotTo(HaveOccurred())
		credential.Spec.KeyPatterns = []string{"app/*"}
		credential.Spec.Commands = []string{"get", "ping"}
		Expect(K8sClient.Update(Ctx, credential)).To(Succeed())
		wait()
		admin, closeAdmin := ConnectToRedisDatabase(dbKey, adminKey)
		defer closeAdmin()
		clients, err := admin.ClientList(Ctx).Result()
		Expect(err).NotTo(HaveOccurred())
		Expect(clients).NotTo(ContainSubstring("id=" + fmt.Sprint(id) + " "))
		Expect(user.Get(Ctx, "app/key").Result()).To(Equal("retained"))
		Expect(user.Get(Ctx, "other/key").Err()).To(HaveOccurred())
		Expect(user.Set(Ctx, "app/key", "forbidden", 0).Err()).To(HaveOccurred())
		var secret corev1.Secret
		Expect(K8sClient.Get(Ctx, types.NamespacedName{Namespace: ns.Name, Name: credential.Spec.SecretName}, &secret)).To(Succeed())
		secret.Data["REDIS_PASSWORD"] = []byte("replacement-password")
		Expect(K8sClient.Update(Ctx, &secret)).To(Succeed())
		Eventually(func() error { return user.Ping(Ctx).Err() }, Timeout, Interval).Should(HaveOccurred())
		Expect(K8sClient.Delete(Ctx, credential)).To(Succeed())
		Eventually(func() bool { return apierrors.IsNotFound(K8sClient.Get(Ctx, key, &api.RedisCredential{})) }, time.Minute, Interval).Should(BeTrue())
		Expect(admin.Get(Ctx, "app/key").Result()).To(Equal("retained"))
	})
})
