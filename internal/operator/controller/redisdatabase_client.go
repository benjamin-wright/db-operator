package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// redisDatabaseClient encapsulates all cluster interactions for the
// RedisDatabase reconciler, providing a focused API that absorbs common
// error-handling patterns (e.g. not-found on get/delete).
type redisDatabaseClient struct {
	inner client.Client
}

// get fetches obj from the cluster. Returns false with a nil error when the
// object does not exist. Returns true and populates obj when it does.
func (c *redisDatabaseClient) get(ctx context.Context, key client.ObjectKey, obj client.Object) (bool, error) {
	if err := c.inner.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *redisDatabaseClient) create(ctx context.Context, obj client.Object) error {
	return c.inner.Create(ctx, obj)
}

func (c *redisDatabaseClient) update(ctx context.Context, obj client.Object) error {
	return c.inner.Update(ctx, obj)
}

// delete removes obj from the cluster. A not-found error is treated as success
// since the desired state (object absent) is already achieved.
func (c *redisDatabaseClient) delete(ctx context.Context, obj client.Object) error {
	if err := c.inner.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *redisDatabaseClient) updateStatus(ctx context.Context, obj client.Object) error {
	return c.inner.Status().Update(ctx, obj)
}
