package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func deleteObserved(ctx context.Context, api client.Client, object client.Object) error {
	uid, version := object.GetUID(), object.GetResourceVersion()
	if uid == "" || version == "" {
		return fmt.Errorf("deletion requires observed UID and resource version")
	}
	err := api.Delete(ctx, object, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &version}})
	return client.IgnoreNotFound(err)
}
