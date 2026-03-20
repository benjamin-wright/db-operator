package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// postgresDatabaseClient encapsulates all cluster interactions for the
// PostgresDatabase reconciler, providing a focused API that absorbs common
// error-handling patterns (e.g. not-found on get/delete).
type postgresDatabaseClient struct {
	inner client.Client
}

// get fetches obj from the cluster. Returns false with a nil error when the
// object does not exist. Returns true and populates obj when it does.
func (c *postgresDatabaseClient) get(ctx context.Context, key client.ObjectKey, obj client.Object) (bool, error) {
	if err := c.inner.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *postgresDatabaseClient) create(ctx context.Context, obj client.Object) error {
	return c.inner.Create(ctx, obj)
}

func (c *postgresDatabaseClient) update(ctx context.Context, obj client.Object) error {
	return c.inner.Update(ctx, obj)
}

// delete removes obj from the cluster. A not-found error is treated as success
// since the desired state (object absent) is already achieved.
func (c *postgresDatabaseClient) delete(ctx context.Context, obj client.Object) error {
	if err := c.inner.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *postgresDatabaseClient) updateStatus(ctx context.Context, obj client.Object) error {
	return c.inner.Status().Update(ctx, obj)
}

// isConflict reports whether err is a Kubernetes API conflict error.
func isConflict(err error) bool { return apierrors.IsConflict(err) }

// isForbidden reports whether err is a Kubernetes API forbidden error.
func isForbidden(err error) bool { return apierrors.IsForbidden(err) }

// isNotFound reports whether err is a Kubernetes API not-found error.
func isNotFound(err error) bool { return apierrors.IsNotFound(err) }

// pvcName returns the deterministic PVC name for the given StatefulSet ordinal.
func pvcName(stsName string, ordinal int) string {
	return fmt.Sprintf("data-%s-%d", stsName, ordinal)
}
