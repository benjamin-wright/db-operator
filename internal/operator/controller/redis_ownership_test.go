package controller

import (
	"context"
	"testing"

	api "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRedisOwnerSignatureBindsEveryAuthorityField(t *testing.T) {
	state := map[string]string{"uid": "owner", "generation": "3", "policy": "digest"}
	signature := redisOwnerSignature("admin-secret", "user", state)
	for _, field := range []string{"uid", "generation", "policy"} {
		changed := map[string]string{"uid": "owner", "generation": "3", "policy": "digest"}
		changed[field] += "changed"
		if redisOwnerSignature("admin-secret", "user", changed) == signature {
			t.Fatalf("signature did not bind %s", field)
		}
	}
	if redisOwnerSignature("admin-secret", "other-user", state) == signature || redisOwnerSignature("other-secret", "user", state) == signature {
		t.Fatal("signature must bind username and admin secret")
	}
}

func TestRedisCredentialFenceRejectsDuplicateOwnerWithoutBackendState(t *testing.T) {
	credential := &api.RedisCredential{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "owner", UID: "owner-uid", Generation: 1}, Spec: api.RedisCredentialSpec{DatabaseRef: "db", Username: "user", SecretName: "secret"}}
	other := credential.DeepCopy()
	other.Name, other.UID = "other", "other-uid"
	reader, _ := protocolClient(t, credential, other)
	fence := redisCredentialFence(context.Background(), reader, credential, client.ObjectKey{Namespace: "apps", Name: "secret"}, &corev1.Secret{}, false, false)
	if err := fence(); err == nil {
		t.Fatal("another live credential must prevent claiming a role after its Redis record disappears")
	}
}

func TestRedisPolicyDigestBindsSecretVersionAndGeneration(t *testing.T) {
	credential := &api.RedisCredential{ObjectMeta: metav1.ObjectMeta{UID: "owner", Generation: 1}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{UID: "secret", ResourceVersion: "1"}}
	original := redisPolicyDigest(credential, "password-a", secret)
	secret.ResourceVersion = "3"
	if redisPolicyDigest(credential, "password-a", secret) == original {
		t.Fatal("password A-to-B-to-A must still require session revocation")
	}
	secret.ResourceVersion = "1"
	credential.Generation = 3
	if redisPolicyDigest(credential, "password-a", secret) == original {
		t.Fatal("policy A-to-B-to-A must still require session revocation")
	}
}
