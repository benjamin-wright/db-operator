package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

const (
	// redisCredentialFinalizerName is added to RedisCredential resources to ensure
	// the Redis ACL user and credential Secret are cleaned up before deletion.
	redisCredentialFinalizerName = "games-hub.io/redis-credential"
)

// RedisCredentialReconciler reconciles a RedisCredential object.
// It creates a Redis ACL user inside the target RedisDatabase instance and writes
// the generated credentials into a Kubernetes Secret.
type RedisCredentialReconciler struct {
	InstanceName string
	client       redisCredentialClient
	redisMgr     RedisManager
}

// +kubebuilder:rbac:groups=games-hub.io,resources=rediscredentials,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=games-hub.io,resources=rediscredentials/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=games-hub.io,resources=rediscredentials/finalizers,verbs=update
// +kubebuilder:rbac:groups=games-hub.io,resources=redisdatabases,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles create/update/delete events for RedisCredential resources.
func (r *RedisCredentialReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var rcred v1alpha1.RedisCredential
	found, err := r.client.get(ctx, req.NamespacedName, &rcred)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching RedisCredential: %w", err)
	}
	if !found {
		logger.Info("RedisCredential resource not found; ignoring")
		return ctrl.Result{}, nil
	}

	if !rcred.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &rcred)
	}

	if !controllerutil.ContainsFinalizer(&rcred, redisCredentialFinalizerName) {
		controllerutil.AddFinalizer(&rcred, redisCredentialFinalizerName)
		if err := r.client.update(ctx, &rcred); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
	}

	result, reconcileErr := r.reconcileRedisCredential(ctx, &rcred)

	if isConflict(reconcileErr) {
		return ctrl.Result{Requeue: true}, nil
	}
	if isForbidden(reconcileErr) {
		logger.V(1).Info("reconcile blocked by Forbidden error; namespace may be terminating", "error", reconcileErr)
		return ctrl.Result{}, nil
	}

	if err := r.client.updateStatus(ctx, &rcred); err != nil {
		if isConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return result, reconcileErr
}

// reconcileRedisCredential resolves the target database reference, provisions the
// Redis ACL user, and mutates rcred status in memory. The caller is responsible
// for persisting status via a single r.Status().Update() call.
func (r *RedisCredentialReconciler) reconcileRedisCredential(ctx context.Context, rcred *v1alpha1.RedisCredential) (ctrl.Result, error) {
	var rdb v1alpha1.RedisDatabase
	dbKey := types.NamespacedName{Name: rcred.Spec.DatabaseRef, Namespace: rcred.Namespace}
	rdbFound, err := r.client.get(ctx, dbKey, &rdb)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching target RedisDatabase: %w", err)
	}
	if !rdbFound {
		return r.setPhase(rcred, v1alpha1.RedisCredentialPhasePending,
			"DatabaseNotFound", fmt.Sprintf("target RedisDatabase %q not found", rcred.Spec.DatabaseRef)), nil
	}

	if rdb.Status.Phase != v1alpha1.RedisDatabasePhaseReady {
		return r.setPhase(rcred, v1alpha1.RedisCredentialPhasePending,
			"DatabaseNotReady", fmt.Sprintf("waiting for RedisDatabase %q to become Ready", rcred.Spec.DatabaseRef)), nil
	}

	if rdb.Status.SecretName == "" {
		return r.setPhase(rcred, v1alpha1.RedisCredentialPhasePending,
			"AdminSecretNotReady", "RedisDatabase admin Secret name is not yet populated"), nil
	}

	var adminSecret corev1.Secret
	adminSecretKey := types.NamespacedName{Name: rdb.Status.SecretName, Namespace: rdb.Namespace}
	adminFound, err := r.client.get(ctx, adminSecretKey, &adminSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching admin Secret %q: %w", rdb.Status.SecretName, err)
	}
	if !adminFound {
		return r.setPhase(rcred, v1alpha1.RedisCredentialPhasePending,
			"AdminSecretNotFound", fmt.Sprintf("admin Secret %q not yet visible in cache", rdb.Status.SecretName)), nil
	}

	adminPass := string(adminSecret.Data["REDIS_PASSWORD"])
	host := redisHost(&rdb)

	var existingSecret corev1.Secret
	credSecretKey := types.NamespacedName{Name: rcred.Spec.SecretName, Namespace: rcred.Namespace}
	secretFound, err := r.client.get(ctx, credSecretKey, &existingSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching credential Secret: %w", err)
	}
	if !secretFound {
		password, err := generatePassword(24)
		if err != nil {
			return r.setPhase(rcred, v1alpha1.RedisCredentialPhaseFailed,
				"PasswordGenerationFailed", err.Error()), err
		}

		if err := r.redisMgr.EnsureACLUser(ctx, host, adminPass, rcred.Spec.Username, password,
			rcred.Spec.KeyPatterns, rcred.Spec.ACLCategories, rcred.Spec.Commands); err != nil {
			return r.setPhase(rcred, v1alpha1.RedisCredentialPhaseFailed,
				"UserCreationFailed", err.Error()), err
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rcred.Spec.SecretName,
				Namespace: rcred.Namespace,
				Labels:    labelsForRedisCredential(rcred, r.InstanceName),
			},
			StringData: map[string]string{
				"REDIS_USERNAME": rcred.Spec.Username,
				"REDIS_PASSWORD": password,
				"REDIS_HOST":     host,
				"REDIS_PORT":     fmt.Sprintf("%d", redisPort),
			},
		}
		if err := r.client.createOwned(ctx, rcred, secret); err != nil {
			return ctrl.Result{}, fmt.Errorf("creating credential Secret: %w", err)
		}
	}

	rcred.Status.SecretName = rcred.Spec.SecretName
	return r.setPhase(rcred, v1alpha1.RedisCredentialPhaseReady,
		"CredentialReady", "Redis ACL user and credential Secret are ready"), nil
}

// reconcileDelete cleans up the Redis ACL user and credential Secret, then removes the finalizer.
func (r *RedisCredentialReconciler) reconcileDelete(ctx context.Context, rcred *v1alpha1.RedisCredential) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(rcred, redisCredentialFinalizerName) {
		return ctrl.Result{}, nil
	}

	logger.Info("running Redis credential finalizer cleanup")

	// Attempt to drop the ACL user. If the database is already gone, skip gracefully.
	var rdb v1alpha1.RedisDatabase
	dbKey := types.NamespacedName{Name: rcred.Spec.DatabaseRef, Namespace: rcred.Namespace}
	if rdbFound, _ := r.client.get(ctx, dbKey, &rdb); rdbFound &&
		rdb.Status.Phase == v1alpha1.RedisDatabasePhaseReady &&
		rdb.Status.SecretName != "" {
		var adminSecret corev1.Secret
		adminSecretKey := types.NamespacedName{Name: rdb.Status.SecretName, Namespace: rdb.Namespace}
		if adminFound, _ := r.client.get(ctx, adminSecretKey, &adminSecret); adminFound {
			adminPass := string(adminSecret.Data["REDIS_PASSWORD"])
			host := redisHost(&rdb)
			if err := r.redisMgr.DropACLUser(ctx, host, adminPass, rcred.Spec.Username); err != nil {
				logger.Error(err, "failed to drop Redis ACL user during cleanup", "username", rcred.Spec.Username)
				// Continue — the database may be going away too.
			}
		}
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rcred.Spec.SecretName,
			Namespace: rcred.Namespace,
		},
	}
	if err := r.client.delete(ctx, secret); err != nil {
		return ctrl.Result{}, fmt.Errorf("deleting credential Secret: %w", err)
	}

	controllerutil.RemoveFinalizer(rcred, redisCredentialFinalizerName)
	if err := r.client.update(ctx, rcred); err != nil {
		if isConflict(err) || isNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	logger.Info("Redis credential finalizer cleanup complete")
	return ctrl.Result{}, nil
}

// setPhase mutates the RedisCredential status phase and condition in memory.
// The caller is responsible for persisting via r.Status().Update().
func (r *RedisCredentialReconciler) setPhase(
	rcred *v1alpha1.RedisCredential,
	phase v1alpha1.RedisCredentialPhase,
	reason, message string,
) ctrl.Result {
	rcred.Status.Phase = phase

	conditionStatus := metav1.ConditionFalse
	if phase == v1alpha1.RedisCredentialPhaseReady {
		conditionStatus = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&rcred.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rcred.Generation,
	})

	if phase == v1alpha1.RedisCredentialPhasePending {
		return ctrl.Result{RequeueAfter: 5 * time.Second}
	}

	return ctrl.Result{}
}

// ---------- Helpers ----------

// redisHost returns the in-cluster DNS name for the Redis pod backing the given RedisDatabase.
func redisHost(rdb *v1alpha1.RedisDatabase) string {
	return fmt.Sprintf("%s-0.%s.%s.svc.cluster.local", rdb.Name, rdb.Name, rdb.Namespace)
}

// labelsForRedisCredential returns the standard label set for resources owned by a RedisCredential.
func labelsForRedisCredential(rcred *v1alpha1.RedisCredential, instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":                                   "redis-credential",
		"app.kubernetes.io/instance":                               rcred.Name,
		"app.kubernetes.io/managed-by":                             "db-operator",
		"db-operator.benjamin-wright.github.com/operator-instance": instanceName,
	}
}

// SetupWithManager registers the RedisCredentialReconciler with the controller manager.
func (r *RedisCredentialReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = redisCredentialClient{inner: mgr.GetClient(), scheme: mgr.GetScheme()}
	r.redisMgr = redisManager{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RedisCredential{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
