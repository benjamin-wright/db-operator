package controller

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"

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
	// finalizerName is the finalizer added to PostgresDatabase resources to ensure
	// owned StatefulSet and Service are cleaned up before deletion completes.
	databaseFinalizerName = "games-hub.io/postgres-database"

	// postgresPort is the default port used by PostgreSQL.
	postgresPort = 5432
)

// PostgresDatabaseReconciler reconciles a PostgresDatabase object.
// It creates and owns a StatefulSet and headless Service that back the database instance.
type PostgresDatabaseReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	InstanceName string
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
	if err := r.Get(ctx, req.NamespacedName, &pgdb); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("PostgresDatabase resource not found; ignoring")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching PostgresDatabase: %w", err)
	}

	// Handle deletion via finalizer.
	if !pgdb.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &pgdb)
	}

	// Ensure the finalizer is present.
	if !controllerutil.ContainsFinalizer(&pgdb, databaseFinalizerName) {
		controllerutil.AddFinalizer(&pgdb, databaseFinalizerName)
		if err := r.Update(ctx, &pgdb); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
	}

	// Run sub-reconcilers, collecting the desired status in memory.
	// On the first failure, set the Failed phase and skip subsequent reconcilers.
	var result ctrl.Result
	if err := r.reconcileAdminSecret(ctx, &pgdb); err != nil {
		result = r.setPhase(&pgdb, v1alpha1.DatabasePhaseFailed,
			"AdminSecretReconcileFailed", err.Error())
	} else if err := r.reconcileService(ctx, &pgdb); err != nil {
		result = r.setPhase(&pgdb, v1alpha1.DatabasePhaseFailed,
			"ServiceReconcileFailed", err.Error())
	} else {
		sts, err := r.reconcileStatefulSet(ctx, &pgdb)
		if err != nil {
			result = r.setPhase(&pgdb, v1alpha1.DatabasePhaseFailed,
				"StatefulSetReconcileFailed", err.Error())
		} else {
			result = r.updatePhaseFromStatefulSet(&pgdb, sts)
		}
	}

	// Persist all accumulated status mutations in a single write.
	if err := r.Status().Update(ctx, &pgdb); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return result, nil
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
	if err := r.Delete(ctx, sts); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting StatefulSet: %w", err)
	}

	// Delete the headless Service if it exists.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(pgdb),
			Namespace: pgdb.Namespace,
		},
	}
	if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting Service: %w", err)
	}

	// Delete the admin credentials Secret if it exists.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      adminSecretName(pgdb),
			Namespace: pgdb.Namespace,
		},
	}
	if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting admin Secret: %w", err)
	}

	// Remove finalizer so the CR can be garbage-collected.
	controllerutil.RemoveFinalizer(pgdb, databaseFinalizerName)
	if err := r.Update(ctx, pgdb); err != nil {
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
	err := r.Get(ctx, client.ObjectKey{Namespace: pgdb.Namespace, Name: name}, &existing)
	if err == nil {
		// Secret already exists — ensure status is populated in memory.
		pgdb.Status.SecretName = name
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("fetching admin Secret: %w", err)
	}

	// Secret not found — generate a new password and create it.
	password, err := generatePassword(24)
	if err != nil {
		return fmt.Errorf("generating admin password: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: pgdb.Namespace,
			Labels:    labelsForDatabase(pgdb, r.InstanceName),
		},
		StringData: map[string]string{
			"username": "postgres",
			"password": password,
		},
	}
	if err := controllerutil.SetControllerReference(pgdb, secret, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on admin Secret: %w", err)
	}
	if err := r.Create(ctx, secret); err != nil {
		return fmt.Errorf("creating admin Secret: %w", err)
	}

	// Populate the status field in memory (persisted by the caller's single status write).
	pgdb.Status.SecretName = name

	return nil
}

// reconcileService ensures the headless Service exists and is up-to-date.
func (r *PostgresDatabaseReconciler) reconcileService(ctx context.Context, pgdb *v1alpha1.PostgresDatabase) error {
	desired := r.desiredService(pgdb)

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

	// Update if spec has drifted.
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

// reconcileStatefulSet ensures the StatefulSet exists and is up-to-date.
// It returns the StatefulSet as returned by the API server (from create or
// update) so callers can inspect the latest state without a redundant cache read.
func (r *PostgresDatabaseReconciler) reconcileStatefulSet(ctx context.Context, pgdb *v1alpha1.PostgresDatabase) (*appsv1.StatefulSet, error) {
	desired := r.desiredStatefulSet(pgdb)

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

	// Update mutable fields only if the spec template has drifted.
	if !equality.Semantic.DeepEqual(existing.Spec.Template, desired.Spec.Template) {
		existing.Spec.Template = desired.Spec.Template
		if err := r.Update(ctx, &existing); err != nil {
			return nil, fmt.Errorf("updating StatefulSet: %w", err)
		}
	}

	return &existing, nil
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

// ---------- Owned resource builders ----------

func (r *PostgresDatabaseReconciler) desiredService(pgdb *v1alpha1.PostgresDatabase) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(pgdb),
			Namespace: pgdb.Namespace,
			Labels:    labelsForDatabase(pgdb, r.InstanceName),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  labelsForDatabase(pgdb, r.InstanceName),
			Ports: []corev1.ServicePort{
				{
					Name:       "postgres",
					Port:       postgresPort,
					TargetPort: intstr.FromInt32(postgresPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(pgdb, svc, r.Scheme)
	return svc
}

func (r *PostgresDatabaseReconciler) desiredStatefulSet(pgdb *v1alpha1.PostgresDatabase) *appsv1.StatefulSet {
	replicas := int32(1)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      statefulSetName(pgdb),
			Namespace: pgdb.Namespace,
			Labels:    labelsForDatabase(pgdb, r.InstanceName),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: serviceName(pgdb),
			Selector: &metav1.LabelSelector{
				MatchLabels: labelsForDatabase(pgdb, r.InstanceName),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labelsForDatabase(pgdb, r.InstanceName),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "postgres",
							Image: fmt.Sprintf("postgres:%s", pgdb.Spec.PostgresVersion),
							Ports: []corev1.ContainerPort{
								{
									Name:          "postgres",
									ContainerPort: postgresPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "POSTGRES_DB",
									Value: pgdb.Spec.DatabaseName,
								},
								{
									Name:  "POSTGRES_USER",
									Value: "postgres",
								},
								{
									Name: "POSTGRES_PASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: adminSecretName(pgdb),
											},
											Key: "password",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "data",
									MountPath: "/var/lib/postgresql/data",
									SubPath:   "pgdata",
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"pg_isready",
											"-U", "postgres",
											"-d", pgdb.Spec.DatabaseName,
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
											"pg_isready",
											"-U", "postgres",
											"-d", pgdb.Spec.DatabaseName,
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
								corev1.ResourceStorage: resource.MustParse(pgdb.Spec.StorageSize.String()),
							},
						},
					},
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(pgdb, sts, r.Scheme)
	return sts
}

// ---------- Naming helpers ----------

func statefulSetName(pgdb *v1alpha1.PostgresDatabase) string {
	return pgdb.Name
}

func serviceName(pgdb *v1alpha1.PostgresDatabase) string {
	return pgdb.Name
}

func adminSecretName(pgdb *v1alpha1.PostgresDatabase) string {
	return pgdb.Name + "-admin"
}

// generatePassword returns a cryptographically random alphanumeric string of
// the specified length.
func generatePassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", fmt.Errorf("generating random byte: %w", err)
		}
		result[i] = charset[num.Int64()]
	}
	return string(result), nil
}

func labelsForDatabase(pgdb *v1alpha1.PostgresDatabase, instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":         "postgres",
		"app.kubernetes.io/instance":     pgdb.Name,
		"app.kubernetes.io/managed-by":   "db-operator",
		"games-hub.io/operator-instance": instanceName,
	}
}

// SetupWithManager registers the PostgresDatabaseReconciler with the controller manager.
func (r *PostgresDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PostgresDatabase{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
