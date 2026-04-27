package pgwatcher

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

const (
	mcpManagedByLabel = "app.kubernetes.io/managed-by"
	mcpManagedByValue = "db-mcp"
	mcpUsername       = "db-mcp-readonly"
	requeueDelay      = 5 * time.Second
)

// Reconciler watches PostgresDatabase and PostgresCredential resources, manages
// a read-only MCP credential per database instance, and keeps the Index up to date.
type Reconciler struct {
	client client.Client
	index  *Index
}

// NewReconciler creates a Reconciler that writes discovery results into index.
// Call SetupWithManager to register it with a controller-runtime manager.
func NewReconciler(index *Index) *Reconciler {
	return &Reconciler{index: index}
}

// Reconcile is called whenever a PostgresDatabase changes, or whenever a
// PostgresCredential that references one changes.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	key := ClusterKey{Namespace: req.Namespace, Name: req.Name}

	var pgdb v1alpha1.PostgresDatabase
	if err := r.client.Get(ctx, req.NamespacedName, &pgdb); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("PostgresDatabase not found; removing from index")
			r.index.Delete(key)
			return ctrl.Result{}, r.deleteMCPCredential(ctx, req.Namespace, req.Name)
		}
		return ctrl.Result{}, fmt.Errorf("fetching PostgresDatabase: %w", err)
	}

	// List all credentials in this namespace to find those targeting this database.
	var allCreds v1alpha1.PostgresCredentialList
	if err := r.client.List(ctx, &allCreds, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing PostgresCredentials: %w", err)
	}

	userDatabases := userDatabaseUnion(allCreds.Items, req.Name)

	if len(userDatabases) == 0 {
		// No user credentials exist yet; do not create a read-only credential.
		logger.V(1).Info("no user credentials for database; skipping MCP credential")
		r.index.Delete(key)
		return ctrl.Result{}, r.deleteMCPCredential(ctx, req.Namespace, req.Name)
	}

	result, err := r.reconcileMCPCredential(ctx, req.Namespace, req.Name, userDatabases)
	if err != nil || result.Requeue || result.RequeueAfter > 0 {
		return result, err
	}

	// Credential exists; look up the Secret to populate the index.
	mcpCredName := mcpCredentialName(req.Name)
	var existingCred v1alpha1.PostgresCredential
	if err := r.client.Get(ctx, types.NamespacedName{Name: mcpCredName, Namespace: req.Namespace}, &existingCred); err != nil {
		return ctrl.Result{}, fmt.Errorf("re-fetching MCP credential: %w", err)
	}

	if existingCred.Status.Phase != v1alpha1.CredentialPhaseReady || existingCred.Status.SecretName == "" {
		logger.V(1).Info("waiting for MCP credential to become ready", "credential", mcpCredName)
		r.index.Delete(key)
		return ctrl.Result{RequeueAfter: requeueDelay}, nil
	}

	var secret corev1.Secret
	secretKey := types.NamespacedName{Name: existingCred.Status.SecretName, Namespace: req.Namespace}
	if err := r.client.Get(ctx, secretKey, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			r.index.Delete(key)
			return ctrl.Result{RequeueAfter: requeueDelay}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching credential Secret: %w", err)
	}

	r.index.Set(key, ClusterInfo{
		Namespace: req.Namespace,
		Name:      req.Name,
		Host:      string(secret.Data["PGHOST"]),
		Port:      string(secret.Data["PGPORT"]),
		User:      string(secret.Data["PGUSER"]),
		Password:  string(secret.Data["PGPASSWORD"]),
		Databases: userDatabases,
		Ready:     true,
	})

	return ctrl.Result{}, nil
}

// reconcileMCPCredential creates or updates the MCP-managed PostgresCredential for
// the given database, covering all databases in userDatabases with SELECT access.
// Returns a non-zero Result when the caller should not proceed to Secret lookup yet.
func (r *Reconciler) reconcileMCPCredential(ctx context.Context, namespace, dbName string, userDatabases []string) (ctrl.Result, error) {
	mcpCredName := mcpCredentialName(dbName)
	desired := &v1alpha1.PostgresCredential{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mcpCredName,
			Namespace: namespace,
			Labels: map[string]string{
				mcpManagedByLabel: mcpManagedByValue,
			},
		},
		Spec: v1alpha1.PostgresCredentialSpec{
			DatabaseRef: dbName,
			Username:    mcpUsername,
			SecretName:  mcpCredName,
			Permissions: []v1alpha1.DatabasePermissionEntry{
				{
					Databases:   userDatabases,
					Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect},
				},
			},
		},
	}

	var existing v1alpha1.PostgresCredential
	err := r.client.Get(ctx, types.NamespacedName{Name: mcpCredName, Namespace: namespace}, &existing)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("fetching MCP credential: %w", err)
		}
		if createErr := r.client.Create(ctx, desired); createErr != nil {
			return ctrl.Result{}, fmt.Errorf("creating MCP credential: %w", createErr)
		}
		return ctrl.Result{RequeueAfter: requeueDelay}, nil
	}

	if !databaseSetsEqual(existing.Spec.Permissions, desired.Spec.Permissions) {
		existing.Spec.Permissions = desired.Spec.Permissions
		if updateErr := r.client.Update(ctx, &existing); updateErr != nil {
			if apierrors.IsConflict(updateErr) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("updating MCP credential: %w", updateErr)
		}
	}

	return ctrl.Result{}, nil
}

// deleteMCPCredential removes the MCP-managed PostgresCredential for the given
// database if it exists. A not-found error is treated as success.
func (r *Reconciler) deleteMCPCredential(ctx context.Context, namespace, dbName string) error {
	cred := &v1alpha1.PostgresCredential{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mcpCredentialName(dbName),
			Namespace: namespace,
		},
	}
	if err := r.client.Delete(ctx, cred); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting MCP credential: %w", err)
	}
	return nil
}

// SetupWithManager registers the Reconciler with the provided manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = mgr.GetClient()

	mapCredToDatabase := func(_ context.Context, obj client.Object) []reconcile.Request {
		cred, ok := obj.(*v1alpha1.PostgresCredential)
		if !ok || cred.Spec.DatabaseRef == "" {
			return nil
		}
		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{
				Namespace: cred.Namespace,
				Name:      cred.Spec.DatabaseRef,
			},
		}}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PostgresDatabase{}).
		Watches(&v1alpha1.PostgresCredential{}, handler.EnqueueRequestsFromMapFunc(mapCredToDatabase)).
		Complete(r)
}

// mcpCredentialName returns the deterministic name for the MCP-managed
// PostgresCredential for a given PostgresDatabase name.
func mcpCredentialName(dbName string) string {
	return "db-mcp-" + dbName
}

// userDatabaseUnion computes the sorted union of database names from all
// non-MCP PostgresCredential resources targeting dbName.
func userDatabaseUnion(creds []v1alpha1.PostgresCredential, dbName string) []string {
	seen := make(map[string]bool)
	for _, cred := range creds {
		if cred.Labels[mcpManagedByLabel] == mcpManagedByValue {
			continue
		}
		if cred.Spec.DatabaseRef != dbName {
			continue
		}
		for _, entry := range cred.Spec.Permissions {
			for _, db := range entry.Databases {
				seen[db] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for db := range seen {
		result = append(result, db)
	}
	sort.Strings(result)
	return result
}

// databaseSetsEqual returns true if the two permission slices collectively
// reference the same set of databases (order-independent).
func databaseSetsEqual(a, b []v1alpha1.DatabasePermissionEntry) bool {
	return slices.Equal(permissionDatabases(a), permissionDatabases(b))
}

// permissionDatabases extracts and sorts all database names from a permissions slice.
func permissionDatabases(entries []v1alpha1.DatabasePermissionEntry) []string {
	var dbs []string
	for _, e := range entries {
		dbs = append(dbs, e.Databases...)
	}
	sort.Strings(dbs)
	return dbs
}
