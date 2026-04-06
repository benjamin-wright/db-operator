package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	if err := c.inner.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
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
	EnsureACLUser(ctx context.Context, host, adminPass, username, password string, keyPatterns []string, aclCategories []v1alpha1.RedisACLCategory, commands []string) error
	DropACLUser(ctx context.Context, host, adminPass, username string) error
}

// redisManager is the production implementation of RedisManager.
type redisManager struct{}

// EnsureACLUser connects to Redis and creates (or updates) an ACL user with the
// given password, key patterns, ACL categories, and individual commands.
func (r redisManager) EnsureACLUser(ctx context.Context, host, adminPass, username, password string, keyPatterns []string, aclCategories []v1alpha1.RedisACLCategory, commands []string) error {
	rdb := openRedis(host, adminPass)
	defer rdb.Close()

	args := []interface{}{"ACL", "SETUSER", username, "on", ">" + password, "resetkeys", "nocommands"}

	for _, p := range keyPatterns {
		args = append(args, "~"+p)
	}
	for _, cat := range aclCategories {
		args = append(args, "+@"+string(cat))
	}
	for _, cmd := range commands {
		args = append(args, "+"+cmd)
	}

	if err := rdb.Do(ctx, args...).Err(); err != nil {
		return fmt.Errorf("creating Redis ACL user %q: %w", username, err)
	}

	return nil
}

// DropACLUser connects to Redis and removes the ACL user.
func (r redisManager) DropACLUser(ctx context.Context, host, adminPass, username string) error {
	rdb := openRedis(host, adminPass)
	defer rdb.Close()

	if err := rdb.Do(ctx, "ACL", "DELUSER", username).Err(); err != nil {
		return fmt.Errorf("dropping Redis ACL user %q: %w", username, err)
	}

	return nil
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
