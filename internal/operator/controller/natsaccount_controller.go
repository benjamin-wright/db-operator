package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/benjamin-wright/db-operator/internal/natsconfig"
	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
)

// NatsAccountReconciler reconciles a NatsAccount object.
// For each user defined in the spec it ensures a Kubernetes Secret exists containing
// the generated credentials. Owned Secrets are garbage-collected automatically when
// the NatsAccount CR is deleted. Changes to NatsAccount CRs trigger the NatsCluster
// reconciler to regenerate the NATS server configuration.
type NatsAccountReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	InstanceName string
}

// +kubebuilder:rbac:groups=db-operator.benjamin-wright.github.com,resources=natsaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=db-operator.benjamin-wright.github.com,resources=natsaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=db-operator.benjamin-wright.github.com,resources=natsaccounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=db-operator.benjamin-wright.github.com,resources=natsclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles create/update/delete events for NatsAccount resources.
func (r *NatsAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var acct v1alpha1.NatsAccount
	if err := r.Get(ctx, req.NamespacedName, &acct); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("NatsAccount resource not found; ignoring")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching NatsAccount: %w", err)
	}

	result, reconcileErr := r.reconcileAccount(ctx, &acct)

	if apierrors.IsConflict(reconcileErr) {
		return ctrl.Result{Requeue: true}, nil
	}
	if apierrors.IsForbidden(reconcileErr) {
		return ctrl.Result{}, nil
	}

	if err := r.Status().Update(ctx, &acct); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return result, reconcileErr
}

// reconcileAccount verifies the referenced NatsCluster exists, provisions credential
// Secrets for every user, and updates the account phase in memory.
func (r *NatsAccountReconciler) reconcileAccount(ctx context.Context, acct *v1alpha1.NatsAccount) (ctrl.Result, error) {
	var cluster v1alpha1.NatsCluster
	clusterKey := types.NamespacedName{Name: acct.Spec.ClusterRef, Namespace: acct.Namespace}
	if err := r.Get(ctx, clusterKey, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return r.setNatsAccountPhase(acct, v1alpha1.NatsAccountPhasePending,
				"ClusterNotFound", fmt.Sprintf("target NatsCluster %q not found", acct.Spec.ClusterRef)), nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching target NatsCluster: %w", err)
	}

	for _, user := range acct.Spec.Users {
		if err := r.reconcileUserSecret(ctx, acct, &cluster, user); err != nil {
			return r.setNatsAccountPhase(acct, v1alpha1.NatsAccountPhaseFailed,
				"UserSecretReconcileFailed", err.Error()), err
		}
	}

	return r.setNatsAccountPhase(acct, v1alpha1.NatsAccountPhaseReady,
		"AccountReady", "all user credential Secrets are provisioned"), nil
}

// reconcileUserSecret ensures the credential Secret for a single NatsUser exists.
// On first reconcile it generates a random password. On subsequent reconciles it
// verifies the Secret still exists, recreating it with a new password if missing.
func (r *NatsAccountReconciler) reconcileUserSecret(
	ctx context.Context,
	acct *v1alpha1.NatsAccount,
	cluster *v1alpha1.NatsCluster,
	user v1alpha1.NatsUser,
) error {
	var existing corev1.Secret
	key := types.NamespacedName{Name: user.SecretName, Namespace: acct.Namespace}
	err := r.Get(ctx, key, &existing)
	if err == nil {
		return nil // already exists
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("fetching user Secret %q: %w", user.SecretName, err)
	}

	password, err := generatePassword(24)
	if err != nil {
		return fmt.Errorf("generating password for user %q: %w", user.Username, err)
	}

	host := natsClusterHost(cluster)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      user.SecretName,
			Namespace: acct.Namespace,
			Labels:    labelsForNatsAccount(acct, r.InstanceName),
		},
		StringData: map[string]string{
			"username": user.Username,
			"password": password,
			"account":  acct.Name,
			"host":     host,
			"port":     fmt.Sprintf("%d", natsconfig.ClientPort),
		},
	}
	if err := controllerutil.SetControllerReference(acct, secret, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on user Secret: %w", err)
	}
	if err := r.Create(ctx, secret); err != nil {
		return fmt.Errorf("creating user Secret %q: %w", user.SecretName, err)
	}
	return nil
}

// setNatsAccountPhase mutates the NatsAccount status phase and condition in memory.
// A requeue result is returned when the phase is Pending.
func (r *NatsAccountReconciler) setNatsAccountPhase(
	acct *v1alpha1.NatsAccount,
	phase v1alpha1.NatsAccountPhase,
	reason, message string,
) ctrl.Result {
	acct.Status.Phase = phase

	conditionStatus := metav1.ConditionFalse
	if phase == v1alpha1.NatsAccountPhaseReady {
		conditionStatus = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&acct.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: acct.Generation,
	})

	if phase == v1alpha1.NatsAccountPhasePending {
		return ctrl.Result{RequeueAfter: 5 * time.Second}
	}
	return ctrl.Result{}
}

// ---------- Helpers ----------

// natsClusterHost returns the in-cluster DNS name for the NATS service of the given cluster.
func natsClusterHost(cluster *v1alpha1.NatsCluster) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", cluster.Name, cluster.Namespace)
}

// labelsForNatsAccount returns the standard label set for resources owned by a NatsAccount.
func labelsForNatsAccount(acct *v1alpha1.NatsAccount, instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":                                   "nats-account",
		"app.kubernetes.io/instance":                               acct.Name,
		"app.kubernetes.io/managed-by":                             "db-operator",
		"db-operator.benjamin-wright.github.com/operator-instance": instanceName,
	}
}

// SetupWithManager registers the NatsAccountReconciler with the controller manager.
func (r *NatsAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NatsAccount{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
