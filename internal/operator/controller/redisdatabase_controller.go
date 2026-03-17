package controller

import (
	"context"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
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
	client.Client
	Scheme       *runtime.Scheme
	InstanceName string
}

// +kubebuilder:rbac:groups=games-hub.io,resources=redisdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=games-hub.io,resources=redisdatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=games-hub.io,resources=redisdatabases/finalizers,verbs=update

// Reconcile handles create/update/delete events for RedisDatabase resources.
func (r *RedisDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var rdb v1alpha1.RedisDatabase
	if err := r.Get(ctx, req.NamespacedName, &rdb); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("RedisDatabase resource not found; ignoring")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching RedisDatabase: %w", err)
	}

	if !rdb.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &rdb)
	}

	if !controllerutil.ContainsFinalizer(&rdb, redisDatabaseFinalizerName) {
		controllerutil.AddFinalizer(&rdb, redisDatabaseFinalizerName)
		if err := r.Update(ctx, &rdb); err != nil {
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
	if apierrors.IsConflict(reconcileErr) {
		return ctrl.Result{Requeue: true}, nil
	}
	if apierrors.IsForbidden(reconcileErr) {
		logger.Error(reconcileErr, "reconcile blocked by Forbidden error; namespace may be terminating")
		return ctrl.Result{}, nil
	}

	if err := r.Status().Update(ctx, &rdb); err != nil {
		if apierrors.IsConflict(err) {
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
	if err := r.Delete(ctx, sts); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting StatefulSet: %w", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redisServiceName(rdb),
			Namespace: rdb.Namespace,
		},
	}
	if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting Service: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redisAdminSecretName(rdb),
			Namespace: rdb.Namespace,
		},
	}
	if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting admin Secret: %w", err)
	}

	controllerutil.RemoveFinalizer(rdb, redisDatabaseFinalizerName)
	if err := r.Update(ctx, rdb); err != nil {
		if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
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
	err := r.Get(ctx, client.ObjectKey{Namespace: rdb.Namespace, Name: name}, &existing)
	if err == nil {
		rdb.Status.SecretName = name
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("fetching admin Secret: %w", err)
	}

	password, err := generatePassword(24)
	if err != nil {
		return fmt.Errorf("generating admin password: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: rdb.Namespace,
			Labels:    labelsForRedisDatabase(rdb, r.InstanceName),
		},
		StringData: map[string]string{
			"username": "default",
			"password": password,
		},
	}
	if err := controllerutil.SetControllerReference(rdb, secret, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on admin Secret: %w", err)
	}
	if err := r.Create(ctx, secret); err != nil {
		return fmt.Errorf("creating admin Secret: %w", err)
	}

	rdb.Status.SecretName = name
	return nil
}

// reconcileRedisService ensures the headless Service exists and is up-to-date.
func (r *RedisDatabaseReconciler) reconcileRedisService(ctx context.Context, rdb *v1alpha1.RedisDatabase) error {
	desired := r.desiredRedisService(rdb)

	var existing corev1.Service
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("creating Service: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching Service: %w", err)
	}

	if !equality.Semantic.DeepEqual(existing.Spec.Ports, desired.Spec.Ports) ||
		!equality.Semantic.DeepEqual(existing.Spec.Selector, desired.Spec.Selector) {
		existing.Spec.Ports = desired.Spec.Ports
		existing.Spec.Selector = desired.Spec.Selector
		if err := r.Update(ctx, &existing); err != nil {
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
	desired := r.desiredRedisStatefulSet(rdb)

	var existing appsv1.StatefulSet
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return nil, fmt.Errorf("creating StatefulSet: %w", err)
		}
		return desired, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching StatefulSet: %w", err)
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
		logger.Info("WARNING: storageSize change requires destroying and recreating the database; all data will be lost",
			"name", rdb.Name, "namespace", rdb.Namespace,
			"currentSize", currentSize.String(),
			"desiredSize", desiredSize.String())

		// PVC naming convention: {template.name}-{statefulset.name}-{ordinal}
		pvcName := fmt.Sprintf("data-%s-0", rdb.Name)
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: rdb.Namespace},
		}
		if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("deleting PVC %s for storage resize: %w", pvcName, err)
		}

		if err := r.Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("deleting StatefulSet for storage resize: %w", err)
		}
		return nil, errRedisStatefulSetBeingRecreated
	}

	if !equality.Semantic.DeepEqual(existing.Spec.Template, desired.Spec.Template) {
		existing.Spec.Template = desired.Spec.Template
		if err := r.Update(ctx, &existing); err != nil {
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

// ---------- Owned resource builders ----------

func (r *RedisDatabaseReconciler) desiredRedisService(rdb *v1alpha1.RedisDatabase) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redisServiceName(rdb),
			Namespace: rdb.Namespace,
			Labels:    labelsForRedisDatabase(rdb, r.InstanceName),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  labelsForRedisDatabase(rdb, r.InstanceName),
			Ports: []corev1.ServicePort{
				{
					Name:       "redis",
					Port:       redisPort,
					TargetPort: intstr.FromInt32(redisPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(rdb, svc, r.Scheme)
	return svc
}

func (r *RedisDatabaseReconciler) desiredRedisStatefulSet(rdb *v1alpha1.RedisDatabase) *appsv1.StatefulSet {
	replicas := int32(1)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redisStatefulSetName(rdb),
			Namespace: rdb.Namespace,
			Labels:    labelsForRedisDatabase(rdb, r.InstanceName),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: redisServiceName(rdb),
			Selector: &metav1.LabelSelector{
				MatchLabels: labelsForRedisDatabase(rdb, r.InstanceName),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labelsForRedisDatabase(rdb, r.InstanceName),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "redis",
							Image:   redisImage,
							Command: []string{"redis-server", "--requirepass", "$(REDIS_PASSWORD)"},
							Ports: []corev1.ContainerPort{
								{
									Name:          "redis",
									ContainerPort: redisPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name: "REDIS_PASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: redisAdminSecretName(rdb),
											},
											Key: "password",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "data",
									MountPath: "/data",
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"sh", "-c",
											"redis-cli -a $REDIS_PASSWORD ping",
										},
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"sh", "-c",
											"redis-cli -a $REDIS_PASSWORD ping",
										},
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       10,
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "data",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse(rdb.Spec.StorageSize.String()),
							},
						},
					},
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(rdb, sts, r.Scheme)
	return sts
}

// ---------- Naming helpers ----------

func redisStatefulSetName(rdb *v1alpha1.RedisDatabase) string {
	return rdb.Name
}

func redisServiceName(rdb *v1alpha1.RedisDatabase) string {
	return rdb.Name
}

func redisAdminSecretName(rdb *v1alpha1.RedisDatabase) string {
	return rdb.Name + "-admin"
}

func labelsForRedisDatabase(rdb *v1alpha1.RedisDatabase, instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":         "redis",
		"app.kubernetes.io/instance":     rdb.Name,
		"app.kubernetes.io/managed-by":   "db-operator",
		"games-hub.io/operator-instance": instanceName,
	}
}

// SetupWithManager registers the RedisDatabaseReconciler with the controller manager.
func (r *RedisDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RedisDatabase{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
