package controller

import (
	"context"
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
	if err := r.reconcileRedisAdminSecret(ctx, &rdb); err != nil {
		result = r.setRedisPhase(&rdb, v1alpha1.RedisDatabasePhaseFailed,
			"AdminSecretReconcileFailed", err.Error())
	} else if err := r.reconcileRedisService(ctx, &rdb); err != nil {
		result = r.setRedisPhase(&rdb, v1alpha1.RedisDatabasePhaseFailed,
			"ServiceReconcileFailed", err.Error())
	} else {
		sts, err := r.reconcileRedisStatefulSet(ctx, &rdb)
		if err != nil {
			result = r.setRedisPhase(&rdb, v1alpha1.RedisDatabasePhaseFailed,
				"StatefulSetReconcileFailed", err.Error())
		} else {
			result = r.updateRedisPhaseFromStatefulSet(&rdb, sts)
		}
	}

	if err := r.Status().Update(ctx, &rdb); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return result, nil
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
func (r *RedisDatabaseReconciler) reconcileRedisStatefulSet(ctx context.Context, rdb *v1alpha1.RedisDatabase) (*appsv1.StatefulSet, error) {
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
