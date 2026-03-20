package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// natsClusterClient encapsulates all cluster interactions for the NatsClusterReconciler.
type natsClusterClient struct {
	inner client.Client
}

func (c *natsClusterClient) get(ctx context.Context, key client.ObjectKey, obj client.Object) (bool, error) {
	if err := c.inner.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *natsClusterClient) create(ctx context.Context, obj client.Object) error {
	return c.inner.Create(ctx, obj)
}

func (c *natsClusterClient) update(ctx context.Context, obj client.Object) error {
	return c.inner.Update(ctx, obj)
}

// delete removes obj from the cluster. A not-found error is treated as success.
func (c *natsClusterClient) delete(ctx context.Context, obj client.Object) error {
	if err := c.inner.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *natsClusterClient) updateStatus(ctx context.Context, obj client.Object) error {
	return c.inner.Status().Update(ctx, obj)
}

func (c *natsClusterClient) list(ctx context.Context, obj client.ObjectList, opts ...client.ListOption) error {
	return c.inner.List(ctx, obj, opts...)
}
