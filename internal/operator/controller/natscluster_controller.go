package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/benjamin-wright/db-operator/internal/natsconfig"
	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
)

const (
	// natsClusterFinalizerName is the finalizer added to NatsCluster resources to ensure
	// owned Deployment, Service, ConfigMap, and optional PVC are cleaned up before deletion.
	natsClusterFinalizerName = "games-hub.io/nats-cluster"

	// natsImage is the base NATS server image name (version is appended from the CR spec).
	natsImage = "nats"

	// natsConfigKey is the filename for the NATS server config inside the ConfigMap.
	natsConfigKey = "nats.conf"

	// natsConfigMountPath is the directory inside the container where the config is mounted.
	natsConfigMountPath = "/etc/nats"
)

// NatsClusterReconciler reconciles a NatsCluster object.
// It creates and owns a Deployment, Service, and ConfigMap for the NATS server, and an
// optional PersistentVolumeClaim when JetStream is enabled. The NATS server configuration
// is regenerated from all NatsAccount CRs that reference this cluster on every reconcile.
type NatsClusterReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	InstanceName string
}

// +kubebuilder:rbac:groups=db-operator.benjamin-wright.github.com,resources=natsclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=db-operator.benjamin-wright.github.com,resources=natsclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=db-operator.benjamin-wright.github.com,resources=natsclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=db-operator.benjamin-wright.github.com,resources=natsaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles create/update/delete events for NatsCluster resources.
func (r *NatsClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var nats v1alpha1.NatsCluster
	if err := r.Get(ctx, req.NamespacedName, &nats); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("NatsCluster resource not found; ignoring")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching NatsCluster: %w", err)
	}

	if !nats.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &nats)
	}

	if !controllerutil.ContainsFinalizer(&nats, natsClusterFinalizerName) {
		controllerutil.AddFinalizer(&nats, natsClusterFinalizerName)
		if err := r.Update(ctx, &nats); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
	}

	accounts, err := r.listAccountsForCluster(ctx, &nats)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing NatsAccounts: %w", err)
	}

	creds := make([]natsconfig.AccountCredentials, 0, len(accounts))
	for _, acct := range accounts {
		passwords, err := r.readUserPasswords(ctx, &acct)
		if err != nil {
			return ctrl.Result{}, err
		}
		creds = append(creds, natsconfig.AccountCredentials{Account: acct, Passwords: passwords})
	}

	config := natsconfig.Build(nats.Spec.JetStream != nil, creds)

	var result ctrl.Result
	var reconcileErr error

	if err := r.reconcileConfigMap(ctx, &nats, config); err != nil {
		reconcileErr = err
		result = r.setNatsClusterPhase(&nats, v1alpha1.NatsClusterPhaseFailed,
			"ConfigMapReconcileFailed", err.Error())
	} else if err := r.reconcileNatsService(ctx, &nats); err != nil {
		reconcileErr = err
		result = r.setNatsClusterPhase(&nats, v1alpha1.NatsClusterPhaseFailed,
			"ServiceReconcileFailed", err.Error())
	} else if err := r.reconcileJetStreamPVC(ctx, &nats); err != nil {
		reconcileErr = err
		result = r.setNatsClusterPhase(&nats, v1alpha1.NatsClusterPhaseFailed,
			"PVCReconcileFailed", err.Error())
	} else {
		deploy, err := r.reconcileNatsDeployment(ctx, &nats, config)
		if err != nil {
			reconcileErr = err
			result = r.setNatsClusterPhase(&nats, v1alpha1.NatsClusterPhaseFailed,
				"DeploymentReconcileFailed", err.Error())
		} else {
			result = r.updateNatsPhaseFromDeployment(&nats, deploy)
		}
	}

	if apierrors.IsConflict(reconcileErr) {
		return ctrl.Result{Requeue: true}, nil
	}
	if apierrors.IsForbidden(reconcileErr) {
		return ctrl.Result{}, nil
	}

	if err := r.Status().Update(ctx, &nats); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return result, reconcileErr
}

// reconcileDelete deletes owned resources and removes the finalizer.
func (r *NatsClusterReconciler) reconcileDelete(ctx context.Context, nats *v1alpha1.NatsCluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(nats, natsClusterFinalizerName) {
		return ctrl.Result{}, nil
	}

	logger.Info("running NatsCluster finalizer cleanup")

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: natsDeploymentName(nats), Namespace: nats.Namespace},
	}
	if err := r.Delete(ctx, deploy); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting Deployment: %w", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: natsServiceName(nats), Namespace: nats.Namespace},
	}
	if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting Service: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: natsConfigMapName(nats), Namespace: nats.Namespace},
	}
	if err := r.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting ConfigMap: %w", err)
	}

	if nats.Spec.JetStream != nil {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: natsJetStreamPVCName(nats), Namespace: nats.Namespace},
		}
		if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("deleting JetStream PVC: %w", err)
		}
	}

	controllerutil.RemoveFinalizer(nats, natsClusterFinalizerName)
	if err := r.Update(ctx, nats); err != nil {
		if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	logger.Info("NatsCluster finalizer cleanup complete")
	return ctrl.Result{}, nil
}

// listAccountsForCluster returns all NatsAccount CRs in the same namespace that reference this cluster.
func (r *NatsClusterReconciler) listAccountsForCluster(ctx context.Context, nats *v1alpha1.NatsCluster) ([]v1alpha1.NatsAccount, error) {
	var list v1alpha1.NatsAccountList
	if err := r.List(ctx, &list, client.InNamespace(nats.Namespace)); err != nil {
		return nil, fmt.Errorf("listing NatsAccounts: %w", err)
	}
	var matched []v1alpha1.NatsAccount
	for _, acct := range list.Items {
		if acct.Spec.ClusterRef == nats.Name {
			matched = append(matched, acct)
		}
	}
	return matched, nil
}

// readUserPasswords reads the credential Secret for each user in the account.
// Users whose Secret does not yet exist are silently skipped — they will be
// included in the config once the NatsAccount controller provisions their Secret.
func (r *NatsClusterReconciler) readUserPasswords(ctx context.Context, acct *v1alpha1.NatsAccount) (map[string]string, error) {
	passwords := make(map[string]string)
	for _, user := range acct.Spec.Users {
		var secret corev1.Secret
		key := types.NamespacedName{Name: user.SecretName, Namespace: acct.Namespace}
		if err := r.Get(ctx, key, &secret); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("reading user Secret %q: %w", user.SecretName, err)
		}
		passwords[user.Username] = string(secret.Data["password"])
	}
	return passwords, nil
}

// reconcileConfigMap ensures the NATS config ConfigMap exists and contains the current config.
func (r *NatsClusterReconciler) reconcileConfigMap(ctx context.Context, nats *v1alpha1.NatsCluster, config string) error {
	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      natsConfigMapName(nats),
			Namespace: nats.Namespace,
			Labels:    labelsForNatsCluster(nats, r.InstanceName),
		},
		Data: map[string]string{natsConfigKey: config},
	}
	if err := controllerutil.SetControllerReference(nats, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on ConfigMap: %w", err)
	}

	var existing corev1.ConfigMap
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("creating ConfigMap: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching ConfigMap: %w", err)
	}

	if !equality.Semantic.DeepEqual(existing.Data, desired.Data) {
		existing.Data = desired.Data
		if err := r.Update(ctx, &existing); err != nil {
			return fmt.Errorf("updating ConfigMap: %w", err)
		}
	}
	return nil
}

// reconcileNatsService ensures the NATS client Service exists and is up to date.
func (r *NatsClusterReconciler) reconcileNatsService(ctx context.Context, nats *v1alpha1.NatsCluster) error {
	desired := r.desiredNatsService(nats)

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

// reconcileJetStreamPVC ensures the JetStream PVC exists when JetStream is enabled.
// It is a no-op when JetStream is not configured.
func (r *NatsClusterReconciler) reconcileJetStreamPVC(ctx context.Context, nats *v1alpha1.NatsCluster) error {
	if nats.Spec.JetStream == nil {
		return nil
	}

	name := natsJetStreamPVCName(nats)
	var existing corev1.PersistentVolumeClaim
	err := r.Get(ctx, client.ObjectKey{Namespace: nats.Namespace, Name: name}, &existing)
	if err == nil {
		return nil // already exists; PVC size changes require manual intervention
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("fetching JetStream PVC: %w", err)
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: nats.Namespace,
			Labels:    labelsForNatsCluster(nats, r.InstanceName),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: nats.Spec.JetStream.StorageSize.DeepCopy(),
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(nats, pvc, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on JetStream PVC: %w", err)
	}
	if err := r.Create(ctx, pvc); err != nil {
		return fmt.Errorf("creating JetStream PVC: %w", err)
	}
	return nil
}

// reconcileNatsDeployment ensures the NATS Deployment exists and is up to date.
// Returns the Deployment as seen by the API server so callers can inspect the latest status.
func (r *NatsClusterReconciler) reconcileNatsDeployment(ctx context.Context, nats *v1alpha1.NatsCluster, config string) (*appsv1.Deployment, error) {
	desired := r.desiredNatsDeployment(nats, natsconfig.Checksum(config))

	var existing appsv1.Deployment
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return nil, fmt.Errorf("creating Deployment: %w", err)
		}
		return desired, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching Deployment: %w", err)
	}

	if !equality.Semantic.DeepEqual(existing.Spec.Template, desired.Spec.Template) {
		existing.Spec.Template = desired.Spec.Template
		if err := r.Update(ctx, &existing); err != nil {
			return nil, fmt.Errorf("updating Deployment: %w", err)
		}
	}
	return &existing, nil
}

// updateNatsPhaseFromDeployment sets the NatsCluster phase in memory based on Deployment readiness.
func (r *NatsClusterReconciler) updateNatsPhaseFromDeployment(nats *v1alpha1.NatsCluster, deploy *appsv1.Deployment) ctrl.Result {
	if deploy.Status.AvailableReplicas >= 1 && deploy.Status.AvailableReplicas == *deploy.Spec.Replicas {
		return r.setNatsClusterPhase(nats, v1alpha1.NatsClusterPhaseReady,
			"DeploymentReady", "NATS server Deployment is available")
	}
	return r.setNatsClusterPhase(nats, v1alpha1.NatsClusterPhasePending,
		"DeploymentNotReady", "waiting for NATS server Deployment to become available")
}

// setNatsClusterPhase mutates the NatsCluster status phase and condition in memory.
// A requeue result is returned when the phase is Pending.
func (r *NatsClusterReconciler) setNatsClusterPhase(
	nats *v1alpha1.NatsCluster,
	phase v1alpha1.NatsClusterPhase,
	reason, message string,
) ctrl.Result {
	nats.Status.Phase = phase

	conditionStatus := metav1.ConditionFalse
	if phase == v1alpha1.NatsClusterPhaseReady {
		conditionStatus = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&nats.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: nats.Generation,
	})

	if phase == v1alpha1.NatsClusterPhasePending {
		return ctrl.Result{RequeueAfter: 5 * time.Second}
	}
	return ctrl.Result{}
}

// ---------- Owned resource builders ----------

func (r *NatsClusterReconciler) desiredNatsService(nats *v1alpha1.NatsCluster) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      natsServiceName(nats),
			Namespace: nats.Namespace,
			Labels:    labelsForNatsCluster(nats, r.InstanceName),
		},
		Spec: corev1.ServiceSpec{
			Selector: labelsForNatsCluster(nats, r.InstanceName),
			Ports: []corev1.ServicePort{
				{
					Name:       "client",
					Port:       natsconfig.ClientPort,
					TargetPort: intstr.FromInt32(natsconfig.ClientPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(nats, svc, r.Scheme)
	return svc
}

func (r *NatsClusterReconciler) desiredNatsDeployment(nats *v1alpha1.NatsCluster, cfgChecksum string) *appsv1.Deployment {
	replicas := int32(1)

	volumes := []corev1.Volume{
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: natsConfigMapName(nats),
					},
				},
			},
		},
	}

	volumeMounts := []corev1.VolumeMount{
		{Name: "config", MountPath: natsConfigMountPath},
	}

	if nats.Spec.JetStream != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: natsJetStreamPVCName(nats),
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "data",
			MountPath: natsconfig.DataMountPath,
		})
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      natsDeploymentName(nats),
			Namespace: nats.Namespace,
			Labels:    labelsForNatsCluster(nats, r.InstanceName),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labelsForNatsCluster(nats, r.InstanceName),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labelsForNatsCluster(nats, r.InstanceName),
					// Changing the config checksum annotation triggers a rolling restart
					// when the ConfigMap contents change.
					Annotations: map[string]string{
						"checksum/config": cfgChecksum,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nats",
							Image: fmt.Sprintf("%s:%s", natsImage, nats.Spec.NatsVersion),
							Args: []string{
								"--config", fmt.Sprintf("%s/%s", natsConfigMountPath, natsConfigKey),
							},
							Ports: []corev1.ContainerPort{
								{Name: "client", ContainerPort: natsconfig.ClientPort, Protocol: corev1.ProtocolTCP},
								{Name: "monitor", ContainerPort: natsconfig.MonitorPort, Protocol: corev1.ProtocolTCP},
							},
							VolumeMounts: volumeMounts,
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt32(natsconfig.MonitorPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt32(natsconfig.MonitorPort),
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       10,
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(nats, deploy, r.Scheme)
	return deploy
}

// ---------- Naming helpers ----------

func natsConfigMapName(nats *v1alpha1.NatsCluster) string {
	return nats.Name + "-config"
}

func natsServiceName(nats *v1alpha1.NatsCluster) string {
	return nats.Name
}

func natsDeploymentName(nats *v1alpha1.NatsCluster) string {
	return nats.Name
}

func natsJetStreamPVCName(nats *v1alpha1.NatsCluster) string {
	return nats.Name + "-jetstream"
}

func labelsForNatsCluster(nats *v1alpha1.NatsCluster, instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":         "nats",
		"app.kubernetes.io/instance":     nats.Name,
		"app.kubernetes.io/managed-by":   "db-operator",
		"games-hub.io/operator-instance": instanceName,
	}
}

// SetupWithManager registers the NatsClusterReconciler with the controller manager.
// It watches NatsAccount CRs and enqueues the referenced NatsCluster for reconciliation
// whenever an account is created, updated, or deleted — ensuring the server config
// is regenerated promptly.
func (r *NatsClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NatsCluster{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Watches(
			&v1alpha1.NatsAccount{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
				acct := obj.(*v1alpha1.NatsAccount)
				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Name:      acct.Spec.ClusterRef,
						Namespace: acct.Namespace,
					}},
				}
			}),
		).
		Complete(r)
}
