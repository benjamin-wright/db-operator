package controller

import (
	"context"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

// postgresMigrationSetClient encapsulates all Kubernetes API and OCI registry
// interactions for the PostgresMigrationSetReconciler.
type postgresMigrationSetClient struct {
	inner  client.Client
	scheme *runtime.Scheme
}

func (c *postgresMigrationSetClient) get(ctx context.Context, key client.ObjectKey, obj client.Object) (bool, error) {
	if err := c.inner.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *postgresMigrationSetClient) updateStatus(ctx context.Context, obj client.Object) error {
	return c.inner.Status().Update(ctx, obj)
}

func (c *postgresMigrationSetClient) list(ctx context.Context, obj client.ObjectList, opts ...client.ListOption) error {
	return c.inner.List(ctx, obj, opts...)
}

// createOwned sets a controller owner reference on obj then creates it.
func (c *postgresMigrationSetClient) createOwned(ctx context.Context, owner, obj client.Object) error {
	_ = controllerutil.SetControllerReference(owner, obj, c.scheme)
	return c.inner.Create(ctx, obj)
}

// delete removes obj from the cluster. A not-found error is treated as success.
func (c *postgresMigrationSetClient) delete(ctx context.Context, obj client.Object) error {
	if err := c.inner.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// listOwnedJobs returns all batch/v1 Jobs in the same namespace labelled as
// owned by pgms (via migrationSetLabel).
func (c *postgresMigrationSetClient) listOwnedJobs(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet) (batchv1.JobList, error) {
	var list batchv1.JobList
	err := c.inner.List(ctx, &list,
		client.InNamespace(pgms.Namespace),
		client.MatchingLabels{migrationSetLabel: pgms.Name},
	)
	return list, err
}

// listPodsForJob returns all Pods associated with the named Job (using the
// standard batch.kubernetes.io/job-name label set by Kubernetes).
func (c *postgresMigrationSetClient) listPodsForJob(ctx context.Context, jobName, namespace string) (corev1.PodList, error) {
	var list corev1.PodList
	err := c.inner.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{"batch.kubernetes.io/job-name": jobName},
	)
	return list, err
}

// resolveArtifact resolves the OCI reference ref to a digest-pinned reference
// of the form `registry/repository@sha256:…`. If ref already contains a
// digest (i.e. the reference segment begins with "sha256:"), it is returned
// unchanged.
func (c *postgresMigrationSetClient) resolveArtifact(ctx context.Context, ref string) (string, error) {
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return "", fmt.Errorf("parsing artifact reference %q: %w", ref, err)
	}

	if isRegistryLoopback(repo.Reference.Registry) {
		repo.PlainHTTP = true
	}

	credStore, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err == nil {
		repo.Client = &auth.Client{
			Client:     retry.DefaultClient,
			Cache:      auth.NewCache(),
			Credential: credentials.Credential(credStore),
		}
	}

	tag := repo.Reference.Reference
	if tag == "" {
		return "", fmt.Errorf("artifact reference %q is missing a tag or digest", ref)
	}
	if strings.HasPrefix(tag, "sha256:") {
		return ref, nil
	}

	desc, err := repo.Resolve(ctx, tag)
	if err != nil {
		return "", fmt.Errorf("resolving artifact %q: %w", ref, err)
	}

	return repo.Reference.Registry + "/" + repo.Reference.Repository + "@" + desc.Digest.String(), nil
}

// isRegistryLoopback reports whether the registry host is a loopback address.
func isRegistryLoopback(host string) bool {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
