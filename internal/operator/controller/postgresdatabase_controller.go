package controller

import (
	"context"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

const (
	// finalizerName is the finalizer added to PostgresDatabase resources to ensure
	// owned StatefulSet and Service are cleaned up before deletion completes.
	databaseFinalizerName = "games-hub.io/postgres-database"

	// postgresPort is the default port used by PostgreSQL.
	postgresPort = 5432
)

// errStatefulSetBeingRecreated is returned by reconcileStatefulSet when the
// StatefulSet has been orphan-deleted to apply a VolumeClaimTemplate change
// and has not yet been fully removed. The main reconciler treats this as a
// transient Pending condition rather than a failure.
var errStatefulSetBeingRecreated = errors.New("StatefulSet is being recreated for VolumeClaimTemplate update")

// PostgresDatabaseReconciler reconciles a PostgresDatabase object.
// It orchestrates when resources are created, updated, or deleted, delegating
// resource construction to the builder and cluster interactions to the client.
type PostgresDatabaseReconciler struct {
	InstanceName string
	client       postgresDatabaseClient
	builder      postgresDatabaseBuilder
}

// +kubebuilder:rbac:groups=games-hub.io,resources=postgresdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=games-hub.io,resources=postgresdatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=games-hub.io,resources=postgresdatabases/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles create/update/delete events for PostgresDatabase resources.
func (r *PostgresDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the PostgresDatabase instance.
	var pgdb v1alpha1.PostgresDatabase
	found, err := r.client.get(ctx, req.NamespacedName, &pgdb)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching PostgresDatabase: %w", err)
	}
	if !found {
		logger.Info("PostgresDatabase resource not found; ignoring")
		return ctrl.Result{}, nil
	}

	// Handle deletion via finalizer.
	if !pgdb.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &pgdb)
	}

	// Ensure the finalizer is present.
	if !controllerutil.ContainsFinalizer(&pgdb, databaseFinalizerName) {
		controllerutil.AddFinalizer(&pgdb, databaseFinalizerName)
		if err := r.client.update(ctx, &pgdb); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
	}

	// Run sub-reconcilers, collecting the desired status in memory.
	// On the first failure, set the Failed phase and skip subsequent reconcilers.
	var result ctrl.Result
	var reconcileErr error
	if err := r.reconcileAdminSecret(ctx, &pgdb); err != nil {
		reconcileErr = err
		result = r.setPhase(&pgdb, v1alpha1.DatabasePhaseFailed,
			"AdminSecretReconcileFailed", err.Error())
	} else if err := r.reconcileService(ctx, &pgdb); err != nil {
		reconcileErr = err
		result = r.setPhase(&pgdb, v1alpha1.DatabasePhaseFailed,
			"ServiceReconcileFailed", err.Error())
	} else {
		sts, err := r.reconcileStatefulSet(ctx, &pgdb)
		if err != nil {
			if errors.Is(err, errStatefulSetBeingRecreated) {
				result = r.setPhase(&pgdb, v1alpha1.DatabasePhasePending,
					"StatefulSetBeingRecreated", "StatefulSet is being recreated to apply volume claim template changes")
			} else {
				reconcileErr = err
				result = r.setPhase(&pgdb, v1alpha1.DatabasePhaseFailed,
					"StatefulSetReconcileFailed", err.Error())
			}
		} else {
			result = r.updatePhaseFromStatefulSet(&pgdb, sts)
		}
	}

	// Conflict means the cached object is stale; requeue without logging an error
	// and let the informer provide the latest version.
	// Forbidden typically means the namespace is terminating; stop without marking Failed.
	if isConflict(reconcileErr) {
		return ctrl.Result{Requeue: true}, nil
	}
	if isForbidden(reconcileErr) {
		logger.V(1).Info("reconcile blocked by Forbidden error; namespace may be terminating", "error", reconcileErr)
		return ctrl.Result{}, nil
	}

	// Persist all accumulated status mutations in a single write.
	if err := r.client.updateStatus(ctx, &pgdb); err != nil {
		if isConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return result, reconcileErr
}

// reconcileDelete handles deletion of owned resources and removes the finalizer.
func (r *PostgresDatabaseReconciler) reconcileDelete(ctx context.Context, pgdb *v1alpha1.PostgresDatabase) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(pgdb, databaseFinalizerName) {
		return ctrl.Result{}, nil
	}

	logger.Info("running finalizer cleanup")

	// Delete the StatefulSet if it exists.
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      statefulSetName(pgdb),
			Namespace: pgdb.Namespace,
		},
	}
	if err := r.client.delete(ctx, sts); err != nil {
		return ctrl.Result{}, fmt.Errorf("deleting StatefulSet: %w", err)
	}

	// Delete the headless Service if it exists.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(pgdb),
			Namespace: pgdb.Namespace,
		},
	}
	if err := r.client.delete(ctx, svc); err != nil {
		return ctrl.Result{}, fmt.Errorf("deleting Service: %w", err)
	}

	// Delete the admin credentials Secret if it exists.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      adminSecretName(pgdb),
			Namespace: pgdb.Namespace,
		},
	}
	if err := r.client.delete(ctx, secret); err != nil {
		return ctrl.Result{}, fmt.Errorf("deleting admin Secret: %w", err)
	}

	// Remove finalizer so the CR can be garbage-collected.
	controllerutil.RemoveFinalizer(pgdb, databaseFinalizerName)
	if err := r.client.update(ctx, pgdb); err != nil {
		if isConflict(err) || isNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	logger.Info("finalizer cleanup complete")
	return ctrl.Result{}, nil
}

// reconcileAdminSecret ensures the admin credentials Secret exists for the
// PostgresDatabase instance. On first reconcile it generates a random password;
// on subsequent reconciles it verifies the Secret still exists (recreating if
// missing) but does NOT rotate the password when the Secret is present.
func (r *PostgresDatabaseReconciler) reconcileAdminSecret(ctx context.Context, pgdb *v1alpha1.PostgresDatabase) error {
	name := adminSecretName(pgdb)

	var existing corev1.Secret
	found, err := r.client.get(ctx, client.ObjectKey{Namespace: pgdb.Namespace, Name: name}, &existing)
	if err != nil {
		return fmt.Errorf("fetching admin Secret: %w", err)
	}
	if found {
		pgdb.Status.SecretName = name
		return nil
	}

	// Secret not found — build and create one with a freshly generated password.
	secret, err := r.builder.desiredAdminSecret(pgdb)
	if err != nil {
		return fmt.Errorf("building admin Secret: %w", err)
	}
	if err := r.client.create(ctx, secret); err != nil {
		return fmt.Errorf("creating admin Secret: %w", err)
	}

	pgdb.Status.SecretName = name
	return nil
}

// reconcileService ensures the headless Service exists and is up-to-date.
func (r *PostgresDatabaseReconciler) reconcileService(ctx context.Context, pgdb *v1alpha1.PostgresDatabase) error {
	desired := r.builder.desiredService(pgdb)

	var existing corev1.Service
	found, err := r.client.get(ctx, client.ObjectKeyFromObject(desired), &existing)
	if err != nil {
		return fmt.Errorf("fetching Service: %w", err)
	}
	if !found {
		if err := r.client.create(ctx, desired); err != nil {
			return fmt.Errorf("creating Service: %w", err)
		}
		return nil
	}

	// Update if spec has drifted.
	if !equality.Semantic.DeepEqual(existing.Spec.Ports, desired.Spec.Ports) ||
		!equality.Semantic.DeepEqual(existing.Spec.Selector, desired.Spec.Selector) {
		existing.Spec.Ports = desired.Spec.Ports
		existing.Spec.Selector = desired.Spec.Selector
		if err := r.client.update(ctx, &existing); err != nil {
			return fmt.Errorf("updating Service: %w", err)
		}
	}

	return nil
}

// reconcileStatefulSet ensures the StatefulSet exists and is up-to-date.
// It returns the StatefulSet as returned by the API server (from create or
// update) so callers can inspect the latest state without a redundant cache read.
//
// Because StatefulSet.spec.volumeClaimTemplates is immutable, a storage size
// change requires deleting the StatefulSet and its PVC, then recreating both.
// WARNING: this destroys all data in the database. During the deletion window
// the method returns errStatefulSetBeingRecreated so the caller sets
// phase=Pending rather than phase=Failed.
func (r *PostgresDatabaseReconciler) reconcileStatefulSet(ctx context.Context, pgdb *v1alpha1.PostgresDatabase) (*appsv1.StatefulSet, error) {
	logger := log.FromContext(ctx)
	desired := r.builder.desiredStatefulSet(pgdb)

	var existing appsv1.StatefulSet
	found, err := r.client.get(ctx, client.ObjectKeyFromObject(desired), &existing)
	if err != nil {
		return nil, fmt.Errorf("fetching StatefulSet: %w", err)
	}
	if !found {
		if err := r.client.create(ctx, desired); err != nil {
			return nil, fmt.Errorf("creating StatefulSet: %w", err)
		}
		return desired, nil
	}

	// If the StatefulSet is already being deleted, wait for it to disappear
	// before recreating.
	if !existing.DeletionTimestamp.IsZero() {
		return nil, errStatefulSetBeingRecreated
	}

	// volumeClaimTemplates is immutable. When storage has changed, delete both
	// the StatefulSet and its PVC — this destroys all data — then requeue so
	// the next reconcile recreates everything fresh with the new storage size.
	if volumeClaimStorageChanged(&existing, desired) {
		currentSize := existing.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
		desiredSize := desired.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
		logger.V(1).Info("WARNING: storageSize change requires destroying and recreating the database; all data will be lost",
			"name", pgdb.Name, "namespace", pgdb.Namespace,
			"currentSize", currentSize.String(),
			"desiredSize", desiredSize.String())

		name := pvcName(pgdb.Name, 0)
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: pgdb.Namespace},
		}
		if err := r.client.delete(ctx, pvc); err != nil {
			return nil, fmt.Errorf("deleting PVC %s for storage resize: %w", name, err)
		}

		if err := r.client.delete(ctx, &existing); err != nil {
			return nil, fmt.Errorf("deleting StatefulSet for storage resize: %w", err)
		}
		return nil, errStatefulSetBeingRecreated
	}

	// Update mutable fields only if the spec template has drifted.
	if !equality.Semantic.DeepEqual(existing.Spec.Template, desired.Spec.Template) {
		existing.Spec.Template = desired.Spec.Template
		if err := r.client.update(ctx, &existing); err != nil {
			return nil, fmt.Errorf("updating StatefulSet: %w", err)
		}
	}

	return &existing, nil
}

// volumeClaimStorageChanged returns true when the storage request in the first
// VolumeClaimTemplate of the existing StatefulSet differs from the desired one.
func volumeClaimStorageChanged(existing, desired *appsv1.StatefulSet) bool {
	if len(existing.Spec.VolumeClaimTemplates) == 0 || len(desired.Spec.VolumeClaimTemplates) == 0 {
		return false
	}
	existingStorage := existing.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
	desiredStorage := desired.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
	return existingStorage.Cmp(desiredStorage) != 0
}

// updatePhaseFromStatefulSet checks the StatefulSet readiness and sets the
// PostgresDatabase phase accordingly in memory. The caller passes the StatefulSet
// returned by the most recent API server write so no redundant cache read is needed.
func (r *PostgresDatabaseReconciler) updatePhaseFromStatefulSet(pgdb *v1alpha1.PostgresDatabase, sts *appsv1.StatefulSet) ctrl.Result {
	if sts.Status.ReadyReplicas >= 1 && sts.Status.ReadyReplicas == *sts.Spec.Replicas {
		return r.setPhase(pgdb, v1alpha1.DatabasePhaseReady,
			"StatefulSetReady", "StatefulSet has all replicas ready")
	}

	return r.setPhase(pgdb, v1alpha1.DatabasePhasePending,
		"StatefulSetNotReady", "waiting for StatefulSet replicas to become ready")
}

// setPhase mutates the PostgresDatabase status phase and condition in memory.
// The caller is responsible for persisting the status via a single
// r.Status().Update() call. A requeue result is returned when the phase is Pending.
func (r *PostgresDatabaseReconciler) setPhase(
	pgdb *v1alpha1.PostgresDatabase,
	phase v1alpha1.DatabasePhase,
	reason, message string,
) ctrl.Result {
	pgdb.Status.Phase = phase

	conditionStatus := metav1.ConditionFalse
	if phase == v1alpha1.DatabasePhaseReady {
		conditionStatus = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&pgdb.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: pgdb.Generation,
	})

	if phase == v1alpha1.DatabasePhasePending {
		return ctrl.Result{RequeueAfter: 5_000_000_000} // 5 seconds
	}

	return ctrl.Result{}
}

// SetupWithManager registers the PostgresDatabaseReconciler with the controller manager.
func (r *PostgresDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = postgresDatabaseClient{inner: mgr.GetClient()}
	r.builder = postgresDatabaseBuilder{instanceName: r.InstanceName, scheme: mgr.GetScheme()}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PostgresDatabase{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
