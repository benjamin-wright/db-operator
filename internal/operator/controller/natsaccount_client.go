package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// natsAccountClient encapsulates all cluster interactions for the NatsAccountReconciler.
type natsAccountClient struct {
	inner client.Client
}

func (c *natsAccountClient) get(ctx context.Context, key client.ObjectKey, obj client.Object) (bool, error) {
	if err := c.inner.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *natsAccountClient) create(ctx context.Context, obj client.Object) error {
	return c.inner.Create(ctx, obj)
}

func (c *natsAccountClient) update(ctx context.Context, obj client.Object) error {
	return c.inner.Update(ctx, obj)
}

func (c *natsAccountClient) updateStatus(ctx context.Context, obj client.Object) error {
	return c.inner.Status().Update(ctx, obj)
}
