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
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

// NatsAccountReconciler reconciles a NatsAccount object.
// For each user defined in the spec it ensures a Kubernetes Secret exists containing
// the generated credentials. Owned Secrets are garbage-collected automatically when
// the NatsAccount CR is deleted. Changes to NatsAccount CRs trigger the NatsCluster
// reconciler to regenerate the NATS server configuration.
type NatsAccountReconciler struct {
	InstanceName string
	client       natsAccountClient
	builder      natsAccountBuilder
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
	found, err := r.client.get(ctx, req.NamespacedName, &acct)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching NatsAccount: %w", err)
	}
	if !found {
		logger.Info("NatsAccount resource not found; ignoring")
		return ctrl.Result{}, nil
	}

	result, reconcileErr := r.reconcileAccount(ctx, &acct)

	if isConflict(reconcileErr) {
		return ctrl.Result{Requeue: true}, nil
	}
	if isForbidden(reconcileErr) {
		logger.V(1).Info("reconcile blocked by Forbidden error; namespace may be terminating", "error", reconcileErr)
		return ctrl.Result{}, nil
	}

	if err := r.client.updateStatus(ctx, &acct); err != nil {
		if isConflict(err) {
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
	found, err := r.client.get(ctx, clusterKey, &cluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching target NatsCluster: %w", err)
	}
	if !found {
		return r.setNatsAccountPhase(acct, v1alpha1.NatsAccountPhasePending,
			"ClusterNotFound", fmt.Sprintf("target NatsCluster %q not found", acct.Spec.ClusterRef)), nil
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
	found, err := r.client.get(ctx, key, &existing)
	if err != nil {
		return fmt.Errorf("fetching user Secret %q: %w", user.SecretName, err)
	}
	if found {
		return nil // already exists
	}

	password, err := generatePassword(24)
	if err != nil {
		return fmt.Errorf("generating password for user %q: %w", user.Username, err)
	}

	secret := r.builder.desiredUserSecret(acct, cluster, user, password)
	if err := r.client.create(ctx, secret); err != nil {
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

// SetupWithManager registers the NatsAccountReconciler with the controller manager.
func (r *NatsAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = natsAccountClient{inner: mgr.GetClient()}
	r.builder = natsAccountBuilder{instanceName: r.InstanceName, scheme: mgr.GetScheme()}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NatsAccount{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
