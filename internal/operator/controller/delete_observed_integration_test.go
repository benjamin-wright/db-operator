//go:build integration

package controller

import (
	"context"
	"time"

	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Observed-object cleanup", func() {
	for _, kind := range []string{"postgres-secret", "redis-secret", "migration-job"} {
		for _, change := range []string{"replacement", "update"} {
			It("preserves "+kind+" after concurrent "+change, func() {
				ctx, cancel := context.WithTimeout(Ctx, 30*time.Second)
				defer cancel()
				namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "cleanup-race-"}}
				Expect(K8sClient.Create(ctx, namespace)).To(Succeed())
				DeferCleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
					defer stop()
					Expect(client.IgnoreNotFound(K8sClient.Delete(cleanup, namespace))).To(Succeed())
				})
				newObject := func() client.Object {
					metadata := metav1.ObjectMeta{Name: kind + "-" + change, Namespace: namespace.Name}
					if kind != "migration-job" {
						return &corev1.Secret{ObjectMeta: metadata, Data: map[string][]byte{"password": []byte("opaque")}}
					}
					suspended := true
					return &batchv1.Job{
						ObjectMeta: metadata,
						Spec: batchv1.JobSpec{Suspend: &suspended, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers:    []corev1.Container{{Name: "unused", Image: "busybox:1.37"}},
						}}},
					}
				}
				remove := (&postgresCredentialClient{inner: K8sClient}).delete
				if kind == "redis-secret" {
					remove = (&redisCredentialClient{inner: K8sClient}).delete
				} else if kind == "migration-job" {
					remove = (&postgresMigrationSetClient{inner: K8sClient}).delete
				}
				observed := newObject()
				Expect(K8sClient.Create(ctx, observed)).To(Succeed())
				key := client.ObjectKeyFromObject(observed)
				current := observed.DeepCopyObject().(client.Object)
				if change == "replacement" {
					Expect(K8sClient.Delete(ctx, current)).To(Succeed())
					Eventually(func() bool {
						return apierrors.IsNotFound(K8sClient.Get(ctx, key, current))
					}, 10*time.Second, 20*time.Millisecond).Should(BeTrue())
					current = newObject()
					Expect(K8sClient.Create(ctx, current)).To(Succeed())
					Expect(current.GetUID()).NotTo(Equal(observed.GetUID()))
				} else {
					Eventually(func() error {
						if err := K8sClient.Get(ctx, key, current); err != nil {
							return err
						}
						current.SetLabels(map[string]string{"rotation": "new"})
						return K8sClient.Update(ctx, current)
					}, 10*time.Second, 20*time.Millisecond).Should(Succeed())
				}
				Expect(current.GetResourceVersion()).NotTo(Equal(observed.GetResourceVersion()))
				Expect(apierrors.IsConflict(remove(ctx, observed))).To(BeTrue(), "stale cleanup must not delete the current object")
				retained := current.DeepCopyObject().(client.Object)
				Expect(K8sClient.Get(ctx, key, retained)).To(Succeed())
				Expect(retained.GetUID()).To(Equal(current.GetUID()))
				Expect(remove(ctx, newObject())).To(HaveOccurred(), "a name-only object is not deletion authority")
				Eventually(func() error {
					if err := K8sClient.Get(ctx, key, retained); err != nil {
						return client.IgnoreNotFound(err)
					}
					return remove(ctx, retained)
				}, 10*time.Second, 20*time.Millisecond).Should(Succeed())
				Eventually(func() bool {
					return apierrors.IsNotFound(K8sClient.Get(ctx, key, newObject()))
				}, 10*time.Second, 20*time.Millisecond).Should(BeTrue())
				Expect(remove(ctx, retained)).To(Succeed(), "already absent cleanup remains idempotent")
			})
		}
	}
})
