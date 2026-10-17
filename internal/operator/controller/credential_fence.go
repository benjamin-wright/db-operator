package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Check again inside the backend mutation lock/transaction. A cached reconcile
// must not restore a deleted role, superseded generation, or rotated password.
func credentialFence(ctx context.Context, reader client.Reader, expected client.Object, secretKey client.ObjectKey, expectedSecret *corev1.Secret, secretFound, deleting bool) func() error {
	return func() error {
		if reader == nil {
			return fmt.Errorf("uncached credential reader is required")
		}
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		current := expected.DeepCopyObject().(client.Object)
		if err := reader.Get(checkCtx, client.ObjectKeyFromObject(expected), current); err != nil {
			return err
		}
		if current.GetUID() != expected.GetUID() || current.GetGeneration() != expected.GetGeneration() || current.GetDeletionTimestamp().IsZero() == deleting {
			return fmt.Errorf("credential owner or desired generation changed")
		}
		var secret corev1.Secret
		err := reader.Get(checkCtx, secretKey, &secret)
		if !secretFound {
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("credential Secret changed before policy mutation")
		}
		if err != nil {
			return err
		}
		if secret.UID != expectedSecret.UID || secret.ResourceVersion != expectedSecret.ResourceVersion {
			return fmt.Errorf("credential Secret changed before policy mutation")
		}
		return nil
	}
}
