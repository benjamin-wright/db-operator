package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	goredis "github.com/redis/go-redis/v9"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

// redisCredentialClient encapsulates all Kubernetes API interactions for the
// RedisCredentialReconciler. The scheme is required to set owner references on
// created objects.
type redisCredentialClient struct {
	inner  client.Client
	scheme *runtime.Scheme
}

func (c *redisCredentialClient) get(ctx context.Context, key client.ObjectKey, obj client.Object) (bool, error) {
	if err := c.inner.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// createOwned sets a controller owner reference on obj then creates it in the cluster.
func (c *redisCredentialClient) createOwned(ctx context.Context, owner, obj client.Object) error {
	_ = controllerutil.SetControllerReference(owner, obj, c.scheme)
	return c.inner.Create(ctx, obj)
}

func (c *redisCredentialClient) update(ctx context.Context, obj client.Object) error {
	return c.inner.Update(ctx, obj)
}

// delete removes obj from the cluster. A not-found error is treated as success.
func (c *redisCredentialClient) delete(ctx context.Context, obj client.Object) error {
	return deleteObserved(ctx, c.inner, obj)
}

func (c *redisCredentialClient) updateStatus(ctx context.Context, obj client.Object) error {
	return c.inner.Status().Update(ctx, obj)
}

// ────────────────────────────────────────────────────────────────────────────
// RedisManager — external Redis dependency interface
// ────────────────────────────────────────────────────────────────────────────

// RedisManager abstracts direct Redis interactions so the reconciler can be
// tested without a live Redis instance.
type RedisManager interface {
	EnsureCredential(ctx context.Context, host, adminPass string, credential *v1alpha1.RedisCredential, password, policy string, legacy bool, validate func() error) error
	DropCredential(ctx context.Context, host, adminPass string, credential *v1alpha1.RedisCredential, legacy bool, validate func() error) error
}

type redisManager struct{}

func redisOwnerKey(username string) string {
	sum := sha256.Sum256([]byte(username))
	return fmt.Sprintf("__dbop_control:credential:%x", sum)
}

// A watched ownership record and MULTI/EXEC bind policy replacement to one UID
// and monotonic desired generation. The reset removes old passwords, selectors,
// command categories and channel patterns as well as key patterns.
func (r redisManager) EnsureCredential(ctx context.Context, host, adminPass string, credential *v1alpha1.RedisCredential, password, policy string, legacy bool, validate func() error) error {
	if credential.Spec.Username == "default" || credential.UID == "" {
		return fmt.Errorf("reserved or unowned Redis credential")
	}
	previous := meta.FindStatusCondition(credential.Status.Conditions, "PolicyEnforced")
	changed := previous == nil || previous.Status != metav1.ConditionTrue || previous.Message != policy
	args := []any{"ACL", "SETUSER", credential.Spec.Username, "reset", "on", ">" + password}
	for _, pattern := range credential.Spec.KeyPatterns {
		if strings.HasPrefix(pattern, "__dbop_control") {
			return fmt.Errorf("reserved Redis key prefix")
		}
		args = append(args, "~"+pattern)
	}
	for _, category := range credential.Spec.ACLCategories {
		args = append(args, "+@"+string(category))
	}
	for _, command := range credential.Spec.Commands {
		args = append(args, "+"+command)
	}
	rdb := openRedis(host, adminPass)
	defer rdb.Close()
	key := redisOwnerKey(credential.Spec.Username)
	return rdb.Watch(ctx, func(tx *goredis.Tx) error {
		state, authenticated, err := readRedisOwner(ctx, tx, key, credential.Spec.Username, adminPass)
		if err != nil {
			return err
		}
		if validate == nil {
			return fmt.Errorf("credential mutation fence is required")
		}
		if err := validate(); err != nil {
			return err
		}
		if err := validateRedisOwner(ctx, tx, credential, state, authenticated, legacy); err != nil {
			return err
		}
		if changed {
			if err := revokeRedisCredential(ctx, tx, credential.Spec.Username); err != nil {
				return err
			}
		}
		state = map[string]string{"uid": string(credential.UID), "generation": fmt.Sprint(credential.Generation), "policy": policy}
		state["signature"] = redisOwnerSignature(adminPass, credential.Spec.Username, state)
		_, err = tx.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
			pipe.Do(ctx, args...)
			pipe.Del(ctx, key)
			pipe.HSet(ctx, key, state)
			return nil
		})
		return err
	}, key)
}

func (r redisManager) DropCredential(ctx context.Context, host, adminPass string, credential *v1alpha1.RedisCredential, legacy bool, validate func() error) error {
	if credential.Spec.Username == "default" || credential.UID == "" {
		return fmt.Errorf("reserved or unowned Redis credential")
	}
	rdb := openRedis(host, adminPass)
	defer rdb.Close()
	key := redisOwnerKey(credential.Spec.Username)
	return rdb.Watch(ctx, func(tx *goredis.Tx) error {
		state, authenticated, err := readRedisOwner(ctx, tx, key, credential.Spec.Username, adminPass)
		if err != nil {
			return err
		}
		if validate == nil {
			return fmt.Errorf("credential mutation fence is required")
		}
		if err := validate(); err != nil {
			return err
		}
		if err := validateRedisOwner(ctx, tx, credential, state, authenticated, legacy); err != nil {
			return err
		}
		if err := revokeRedisCredential(ctx, tx, credential.Spec.Username); err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
			pipe.Do(ctx, "ACL", "DELUSER", credential.Spec.Username)
			pipe.Del(ctx, key)
			return nil
		})
		return err
	}, key)
}

// openRedis opens a Redis client authenticated as the default admin user.
func openRedis(host, adminPass string) *goredis.Client {
	return goredis.NewClient(&goredis.Options{
		Addr:         fmt.Sprintf("%s:%d", host, redisPort),
		Username:     "default",
		Password:     adminPass,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	})
}
