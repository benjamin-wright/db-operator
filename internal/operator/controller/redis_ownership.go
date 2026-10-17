package controller

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	api "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	redis "github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func redisPolicyDigest(credential *api.RedisCredential, password string, secret *corev1.Secret) string {
	data, _ := json.Marshal([]any{credential.UID, credential.Generation, credential.Spec, password, secret.UID, secret.ResourceVersion})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// Revocation precedes conditional enablement. A writer invalidating WATCH may
// delay reconciliation, but cannot keep the old user's privileges or sessions.
func revokeRedisCredential(ctx context.Context, tx *redis.Tx, username string) error {
	if err := tx.Do(ctx, "ACL", "SETUSER", username, "reset", "off").Err(); err != nil {
		return err
	}
	return tx.Do(ctx, "CLIENT", "KILL", "USER", username).Err()
}

func redisOwnerSignature(adminPass, username string, state map[string]string) string {
	data, _ := json.Marshal([]string{"redis-credential-owner-v1", username, state["uid"], state["generation"], state["policy"]})
	mac := hmac.New(sha256.New, []byte(adminPass))
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func readRedisOwner(ctx context.Context, tx *redis.Tx, key, username, adminPass string) (map[string]string, bool, error) {
	fields := []string{"uid", "generation", "policy", "signature"}
	result, err := tx.Eval(ctx, `
local empty = {'', '', '', ''}
if redis.call('TYPE', KEYS[1]).ok ~= 'hash' then return empty end
for _, field in ipairs(ARGV) do
  if redis.call('HSTRLEN', KEYS[1], field) > 1024 then return empty end
end
return redis.call('HMGET', KEYS[1], unpack(ARGV))
`, []string{key}, "uid", "generation", "policy", "signature").Result()
	if err != nil {
		return nil, false, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != len(fields) {
		return nil, false, fmt.Errorf("invalid Redis ownership response")
	}
	state := make(map[string]string, len(fields))
	for i, field := range fields {
		if value, ok := values[i].(string); ok {
			state[field] = value
		}
	}
	want, _ := hex.DecodeString(redisOwnerSignature(adminPass, username, state))
	got, err := hex.DecodeString(state["signature"])
	authenticated := err == nil && state["uid"] != "" && hmac.Equal(got, want)
	return state, authenticated, nil
}

func validateRedisOwner(ctx context.Context, tx *redis.Tx, credential *api.RedisCredential, state map[string]string, authenticated, secretProof bool) error {
	if authenticated && state["uid"] == string(credential.UID) {
		generation, err := strconv.ParseInt(state["generation"], 10, 64)
		if err != nil || generation > credential.Generation {
			return fmt.Errorf("Redis credential generation is stale")
		}
		return nil
	}
	// Keyspace writers can erase, corrupt or replay records. The live unique CR
	// and its exact owned Secret provide independent proof for safe repair.
	if secretProof {
		return nil
	}
	if authenticated {
		return fmt.Errorf("Redis credential belongs to another resource UID")
	}
	existing, err := tx.Do(ctx, "ACL", "GETUSER", credential.Spec.Username).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("existing Redis credential has no verified owner")
	}
	return nil
}

func redisCredentialFence(ctx context.Context, reader client.Reader, credential *api.RedisCredential, secretKey client.ObjectKey, secret *corev1.Secret, secretFound, deleting bool) func() error {
	return func() error {
		if reader == nil {
			return fmt.Errorf("uncached credential reader is required")
		}
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var credentials api.RedisCredentialList
		if err := reader.List(checkCtx, &credentials, client.InNamespace(credential.Namespace)); err != nil {
			return err
		}
		for _, other := range credentials.Items {
			if other.UID != credential.UID && other.Spec.DatabaseRef == credential.Spec.DatabaseRef && other.Spec.Username == credential.Spec.Username {
				return fmt.Errorf("Redis credential role has a conflicting owner")
			}
		}
		return credentialFence(checkCtx, reader, credential, secretKey, secret, secretFound, deleting)()
	}
}
