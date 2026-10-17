package controller

import (
	"context"
	"testing"

	api "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCredentialFenceRejectsStaleWork(t *testing.T) {
	for _, kind := range []string{"postgres", "redis"} {
		for _, change := range []string{"deleted", "replaced", "generation", "deleting", "rotated_secret", "replaced_secret"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				ctx := context.Background()
				metadata := metav1.ObjectMeta{Name: "credential", Namespace: "apps", UID: "original", Generation: 1}
				var expected client.Object = &api.PostgresCredential{ObjectMeta: metadata}
				if kind == "redis" {
					expected = &api.RedisCredential{ObjectMeta: metadata}
				}
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: "apps", UID: "secret-original"}, Data: map[string][]byte{"password": []byte("old")}}
				apiClient, _ := protocolClient(t, expected, secret)
				if err := apiClient.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
					t.Fatal(err)
				}
				fence := credentialFence(ctx, apiClient, expected.DeepCopyObject().(client.Object), client.ObjectKeyFromObject(secret), secret.DeepCopy(), true, false)
				if err := fence(); err != nil {
					t.Fatalf("current work rejected: %v", err)
				}
				current := expected.DeepCopyObject().(client.Object)
				if err := apiClient.Get(ctx, client.ObjectKeyFromObject(current), current); err != nil {
					t.Fatal(err)
				}
				switch change {
				case "deleted", "replaced":
					if err := apiClient.Delete(ctx, current); err != nil {
						t.Fatal(err)
					}
					if change == "replaced" {
						current.SetUID("replacement")
						current.SetResourceVersion("")
						if err := apiClient.Create(ctx, current); err != nil {
							t.Fatal(err)
						}
					}
				case "generation":
					current.SetGeneration(2)
					if err := apiClient.Update(ctx, current); err != nil {
						t.Fatal(err)
					}
				case "deleting":
					current.SetFinalizers([]string{"test.example/protect"})
					if err := apiClient.Update(ctx, current); err != nil {
						t.Fatal(err)
					}
					if err := apiClient.Delete(ctx, current); err != nil {
						t.Fatal(err)
					}
				case "rotated_secret":
					secret.Data["password"] = []byte("new")
					if err := apiClient.Update(ctx, secret); err != nil {
						t.Fatal(err)
					}
				case "replaced_secret":
					if err := apiClient.Delete(ctx, secret); err != nil {
						t.Fatal(err)
					}
					secret.UID = "secret-replacement"
					secret.ResourceVersion = ""
					if err := apiClient.Create(ctx, secret); err != nil {
						t.Fatal(err)
					}
				}
				if err := fence(); err == nil {
					t.Fatal("stale work must be rejected before a backend mutation")
				}
			})
		}
	}
}
