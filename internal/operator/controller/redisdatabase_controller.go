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

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
)

const (
	// redisDatabaseFinalizerName is the finalizer added to RedisDatabase resources to ensure
	// owned StatefulSet and Service are cleaned up before deletion completes.
	redisDatabaseFinalizerName = "games-hub.io/redis-database"

	// redisPort is the default port used by Redis.
	redisPort = 6379

	// redisImage is the hardcoded Redis 8 image.
	redisImage = "redis:8"
)

// errRedisStatefulSetBeingRecreated is returned by reconcileRedisStatefulSet when
// the StatefulSet has been deleted to apply a VolumeClaimTemplate change and has
// not yet been fully removed. The main reconciler treats this as a transient
// Pending condition rather than a failure.
var errRedisStatefulSetBeingRecreated = errors.New("Redis StatefulSet is being recreated for storage resize")

// RedisDatabaseReconciler reconciles a RedisDatabase object.
// It creates and owns a StatefulSet and headless Service that back the Redis instance.
type RedisDatabaseReconciler struct {
	InstanceName string
	client       redisDatabaseClient
	builder      redisDatabaseBuilder
}

// +kubebuilder:rbac:groups=games-hub.io,resources=redisdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=games-hub.io,resources=redisdatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=games-hub.io,resources=redisdatabases/finalizers,verbs=update

// Reconcile handles create/update/delete events for RedisDatabase resources.
func (r *RedisDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var rdb v1alpha1.RedisDatabase
	found, err := r.client.get(ctx, req.NamespacedName, &rdb)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching RedisDatabase: %w", err)
	}
	if !found {
		logger.Info("RedisDatabase resource not found; ignoring")
		return ctrl.Result{}, nil
	}

	if !rdb.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &rdb)
	}

	if !controllerutil.ContainsFinalizer(&rdb, redisDatabaseFinalizerName) {
		controllerutil.AddFinalizer(&rdb, redisDatabaseFinalizerName)
		if err := r.client.update(ctx, &rdb); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
	}

	var result ctrl.Result
	var reconcileErr error
	if err := r.reconcileRedisAdminSecret(ctx, &rdb); err != nil {
		reconcileErr = err
		result = r.setRedisPhase(&rdb, v1alpha1.RedisDatabasePhaseFailed,
			"AdminSecretReconcileFailed", err.Error())
	} else if err := r.reconcileRedisService(ctx, &rdb); err != nil {
		reconcileErr = err
		result = r.setRedisPhase(&rdb, v1alpha1.RedisDatabasePhaseFailed,
			"ServiceReconcileFailed", err.Error())
	} else {
		sts, err := r.reconcileRedisStatefulSet(ctx, &rdb)
		if err != nil {
			if errors.Is(err, errRedisStatefulSetBeingRecreated) {
				result = r.setRedisPhase(&rdb, v1alpha1.RedisDatabasePhasePending,
					"StatefulSetBeingRecreated", "StatefulSet is being recreated to apply storage size changes")
			} else {
				reconcileErr = err
				result = r.setRedisPhase(&rdb, v1alpha1.RedisDatabasePhaseFailed,
					"StatefulSetReconcileFailed", err.Error())
			}
		} else {
			result = r.updateRedisPhaseFromStatefulSet(&rdb, sts)
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

	if err := r.client.updateStatus(ctx, &rdb); err != nil {
		if isConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return result, reconcileErr
}

// reconcileDelete handles deletion of owned resources and removes the finalizer.
func (r *RedisDatabaseReconciler) reconcileDelete(ctx context.Context, rdb *v1alpha1.RedisDatabase) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(rdb, redisDatabaseFinalizerName) {
		return ctrl.Result{}, nil
	}

	logger.Info("running finalizer cleanup")

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redisStatefulSetName(rdb),
			Namespace: rdb.Namespace,
		},
	}
	if err := r.client.delete(ctx, sts); err != nil {
		return ctrl.Result{}, fmt.Errorf("deleting StatefulSet: %w", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redisServiceName(rdb),
			Namespace: rdb.Namespace,
		},
	}
	if err := r.client.delete(ctx, svc); err != nil {
		return ctrl.Result{}, fmt.Errorf("deleting Service: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redisAdminSecretName(rdb),
			Namespace: rdb.Namespace,
		},
	}
	if err := r.client.delete(ctx, secret); err != nil {
		return ctrl.Result{}, fmt.Errorf("deleting admin Secret: %w", err)
	}

	controllerutil.RemoveFinalizer(rdb, redisDatabaseFinalizerName)
	if err := r.client.update(ctx, rdb); err != nil {
		if isConflict(err) || isNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	logger.Info("finalizer cleanup complete")
	return ctrl.Result{}, nil
}

// reconcileRedisAdminSecret ensures the admin credentials Secret exists.
// On first reconcile it generates a random password; on subsequent reconciles
// it verifies the Secret still exists (recreating if missing) but does NOT rotate the password.
func (r *RedisDatabaseReconciler) reconcileRedisAdminSecret(ctx context.Context, rdb *v1alpha1.RedisDatabase) error {
	name := redisAdminSecretName(rdb)

	var existing corev1.Secret
	found, err := r.client.get(ctx, client.ObjectKey{Namespace: rdb.Namespace, Name: name}, &existing)
	if err != nil {
		return fmt.Errorf("fetching admin Secret: %w", err)
	}
	if found {
		rdb.Status.SecretName = name
		return nil
	}

	secret, err := r.builder.desiredAdminSecret(rdb)
	if err != nil {
		return fmt.Errorf("building admin Secret: %w", err)
	}
	if err := r.client.create(ctx, secret); err != nil {
		return fmt.Errorf("creating admin Secret: %w", err)
	}

	rdb.Status.SecretName = name
	return nil
}

// reconcileRedisService ensures the headless Service exists and is up-to-date.
func (r *RedisDatabaseReconciler) reconcileRedisService(ctx context.Context, rdb *v1alpha1.RedisDatabase) error {
	desired := r.builder.desiredService(rdb)

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

// reconcileRedisStatefulSet ensures the StatefulSet exists and is up-to-date.
// It returns the StatefulSet as returned by the API server so callers can inspect
// the latest state without a redundant cache read.
//
// Because StatefulSet.spec.volumeClaimTemplates is immutable, a storage size
// change requires deleting the StatefulSet and its PVC, then recreating both.
// WARNING: this destroys all data in the Redis instance. During the deletion
// window the method returns errRedisStatefulSetBeingRecreated so the caller
// sets phase=Pending rather than phase=Failed.
func (r *RedisDatabaseReconciler) reconcileRedisStatefulSet(ctx context.Context, rdb *v1alpha1.RedisDatabase) (*appsv1.StatefulSet, error) {
	logger := log.FromContext(ctx)
	desired := r.builder.desiredStatefulSet(rdb)

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
		return nil, errRedisStatefulSetBeingRecreated
	}

	// volumeClaimTemplates is immutable. When storage has changed, delete both
	// the StatefulSet and its PVC — this destroys all data — then requeue so
	// the next reconcile recreates everything fresh with the new storage size.
	if volumeClaimStorageChanged(&existing, desired) {
		currentSize := existing.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
		desiredSize := desired.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
		logger.V(1).Info("WARNING: storageSize change requires destroying and recreating the database; all data will be lost",
			"name", rdb.Name, "namespace", rdb.Namespace,
			"currentSize", currentSize.String(),
			"desiredSize", desiredSize.String())

		name := pvcName(rdb.Name, 0)
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: rdb.Namespace},
		}
		if err := r.client.delete(ctx, pvc); err != nil {
			return nil, fmt.Errorf("deleting PVC %s for storage resize: %w", name, err)
		}

		if err := r.client.delete(ctx, &existing); err != nil {
			return nil, fmt.Errorf("deleting StatefulSet for storage resize: %w", err)
		}
		return nil, errRedisStatefulSetBeingRecreated
	}

	if !equality.Semantic.DeepEqual(existing.Spec.Template, desired.Spec.Template) {
		existing.Spec.Template = desired.Spec.Template
		if err := r.client.update(ctx, &existing); err != nil {
			return nil, fmt.Errorf("updating StatefulSet: %w", err)
		}
	}

	return &existing, nil
}

// updateRedisPhaseFromStatefulSet checks the StatefulSet readiness and sets the
// RedisDatabase phase accordingly in memory.
func (r *RedisDatabaseReconciler) updateRedisPhaseFromStatefulSet(rdb *v1alpha1.RedisDatabase, sts *appsv1.StatefulSet) ctrl.Result {
	if sts.Status.ReadyReplicas >= 1 && sts.Status.ReadyReplicas == *sts.Spec.Replicas {
		return r.setRedisPhase(rdb, v1alpha1.RedisDatabasePhaseReady,
			"StatefulSetReady", "StatefulSet has all replicas ready")
	}

	return r.setRedisPhase(rdb, v1alpha1.RedisDatabasePhasePending,
		"StatefulSetNotReady", "waiting for StatefulSet replicas to become ready")
}

// setRedisPhase mutates the RedisDatabase status phase and condition in memory.
// A requeue result is returned when the phase is Pending.
func (r *RedisDatabaseReconciler) setRedisPhase(
	rdb *v1alpha1.RedisDatabase,
	phase v1alpha1.RedisDatabasePhase,
	reason, message string,
) ctrl.Result {
	rdb.Status.Phase = phase

	conditionStatus := metav1.ConditionFalse
	if phase == v1alpha1.RedisDatabasePhaseReady {
		conditionStatus = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&rdb.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rdb.Generation,
	})

	if phase == v1alpha1.RedisDatabasePhasePending {
		return ctrl.Result{RequeueAfter: 5_000_000_000} // 5 seconds
	}

	return ctrl.Result{}
}

// SetupWithManager registers the RedisDatabaseReconciler with the controller manager.
func (r *RedisDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = redisDatabaseClient{inner: mgr.GetClient()}
	r.builder = redisDatabaseBuilder{instanceName: r.InstanceName, scheme: mgr.GetScheme()}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RedisDatabase{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
