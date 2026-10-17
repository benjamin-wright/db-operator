package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	// credentialFinalizerName is added to PostgresCredential resources to ensure
	// the Postgres user and credential Secret are cleaned up before deletion.
	credentialFinalizerName = "games-hub.io/postgres-credential"
)

// PostgresCredentialReconciler reconciles a PostgresCredential object.
// It creates a Postgres user inside the target database and writes the
// generated credentials into a Kubernetes Secret.
type PostgresCredentialReconciler struct {
	InstanceName string
	client       postgresCredentialClient
	reader       client.Reader
	pgDB         PostgresManager
}

// +kubebuilder:rbac:groups=games-hub.io,resources=postgrescredentials,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=games-hub.io,resources=postgrescredentials/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=games-hub.io,resources=postgrescredentials/finalizers,verbs=update
// +kubebuilder:rbac:groups=games-hub.io,resources=postgresdatabases,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles create/update/delete events for PostgresCredential resources.
func (r *PostgresCredentialReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pgcred v1alpha1.PostgresCredential
	found, err := r.client.get(ctx, req.NamespacedName, &pgcred)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching PostgresCredential: %w", err)
	}
	if !found {
		logger.Info("PostgresCredential resource not found; ignoring")
		return ctrl.Result{}, nil
	}

	// Handle deletion via finalizer.
	if !pgcred.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &pgcred)
	}

	// Ensure the finalizer is present.
	if !controllerutil.ContainsFinalizer(&pgcred, credentialFinalizerName) {
		controllerutil.AddFinalizer(&pgcred, credentialFinalizerName)
		if err := r.client.update(ctx, &pgcred); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
	}

	result, reconcileErr := r.reconcileCredential(ctx, &pgcred)

	if isConflict(reconcileErr) {
		return ctrl.Result{Requeue: true}, nil
	}
	if isForbidden(reconcileErr) {
		logger.V(1).Info("reconcile blocked by Forbidden error; namespace may be terminating", "error", reconcileErr)
		return ctrl.Result{}, nil
	}

	if err := r.client.updateStatus(ctx, &pgcred); err != nil {
		if isConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return result, reconcileErr
}

// reconcileCredential resolves the target database reference, provisions the
// Postgres user, and mutates pgcred status in memory. The caller is responsible
// for persisting status via a single r.Status().Update() call.
func (r *PostgresCredentialReconciler) reconcileCredential(ctx context.Context, pgcred *v1alpha1.PostgresCredential) (ctrl.Result, error) {
	var pgdb v1alpha1.PostgresDatabase
	dbKey := types.NamespacedName{Name: pgcred.Spec.DatabaseRef, Namespace: pgcred.Namespace}
	pgdbFound, err := r.client.get(ctx, dbKey, &pgdb)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching target PostgresDatabase: %w", err)
	}
	if !pgdbFound {
		return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhasePending,
			"DatabaseNotFound", fmt.Sprintf("target PostgresDatabase %q not found", pgcred.Spec.DatabaseRef)), nil
	}

	if pgdb.Status.Phase != v1alpha1.DatabasePhaseReady {
		return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhasePending,
			"DatabaseNotReady", fmt.Sprintf("waiting for PostgresDatabase %q to become Ready", pgcred.Spec.DatabaseRef)), nil
	}

	if pgdb.Status.SecretName == "" {
		return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhasePending,
			"AdminSecretNotReady", "PostgresDatabase admin Secret name is not yet populated"), nil
	}

	var adminSecret corev1.Secret
	adminSecretKey := types.NamespacedName{Name: pgdb.Status.SecretName, Namespace: pgdb.Namespace}
	adminFound, err := r.client.get(ctx, adminSecretKey, &adminSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching admin Secret %q: %w", pgdb.Status.SecretName, err)
	}
	if !adminFound {
		return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhasePending,
			"AdminSecretNotFound", fmt.Sprintf("admin Secret %q not yet visible in cache", pgdb.Status.SecretName)), nil
	}

	adminUser := string(adminSecret.Data["PGUSER"])
	adminPass := string(adminSecret.Data["PGPASSWORD"])
	host := postgresHost(&pgdb)

	var existingSecret corev1.Secret
	credSecretKey := types.NamespacedName{Name: pgcred.Spec.SecretName, Namespace: pgcred.Namespace}
	secretFound, err := r.client.get(ctx, credSecretKey, &existingSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching credential Secret: %w", err)
	}

	if secretFound {
		owner := metav1.GetControllerOf(&existingSecret)
		if owner == nil || owner.UID != pgcred.UID || owner.Name != pgcred.Name || owner.Kind != "PostgresCredential" || owner.APIVersion != v1alpha1.GroupVersion.String() {
			return ctrl.Result{}, fmt.Errorf("credential Secret has conflicting ownership")
		}
	}
	var credentials v1alpha1.PostgresCredentialList
	if err := r.client.list(ctx, &credentials, client.InNamespace(pgcred.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	for _, other := range credentials.Items {
		if other.UID != pgcred.UID && other.Spec.DatabaseRef == pgcred.Spec.DatabaseRef && other.Spec.Username == pgcred.Spec.Username {
			return ctrl.Result{}, fmt.Errorf("credential role has a conflicting owner")
		}
	}

	ownership, err := r.pgDB.AcquireUserOwnership(host, adminUser, adminPass, pgcred.Spec.Username, string(pgcred.UID), pgcred.Generation, secretFound && string(existingSecret.Data["PGUSER"]) == pgcred.Spec.Username, credentialFence(ctx, r.reader, pgcred, credSecretKey, &existingSecret, secretFound, false))
	if err != nil {
		return ctrl.Result{}, err
	}
	defer ownership.Close()
	manager := ownership.Manager()
	password := string(existingSecret.Data["PGPASSWORD"])
	if !secretFound || password == "" {
		password, err = generatePassword(24)
		if err != nil {
			return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhaseFailed,
				"PasswordGenerationFailed", err.Error()), err
		}
	}

	policyBytes, err := json.Marshal(struct {
		Spec          v1alpha1.PostgresCredentialSpec
		Password      string
		UID           string
		Generation    int64
		SecretUID     string
		SecretVersion string
	}{pgcred.Spec, password, string(pgcred.UID), pgcred.Generation, string(existingSecret.UID), existingSecret.ResourceVersion})
	if err != nil {
		return ctrl.Result{}, err
	}
	policyDigest := fmt.Sprintf("%x", sha256.Sum256(policyBytes))
	previous := meta.FindStatusCondition(pgcred.Status.Conditions, "PolicyEnforced")
	changed := previous == nil || previous.Status != metav1.ConditionTrue || previous.Message != policyDigest
	if changed {
		meta.SetStatusCondition(&pgcred.Status.Conditions, metav1.Condition{Type: "PolicyEnforced", Status: metav1.ConditionFalse, Reason: "ReplacingPolicy", Message: "Credential policy replacement is pending", ObservedGeneration: pgcred.Generation})
		if err := manager.ResetUserPolicy(host, adminUser, adminPass, pgcred.Spec.Username); err != nil {
			return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhasePending, "PolicyRevocationPending", "Previous privileges or sessions have not been revoked"), err
		}
	}

	// EnsureUserExists is only needed for clusterRoles-only credentials —
	// EnsureUser inside the Permissions loop creates the role otherwise.
	// Both functions update the password when the role already exists, keeping
	// the Kubernetes Secret as the authoritative credential source.
	if len(pgcred.Spec.Permissions) == 0 {
		if err := manager.EnsureUserExists(host, adminUser, adminPass, pgcred.Spec.Username, password); err != nil {
			return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhaseFailed,
				"UserCreationFailed", err.Error()), err
		}
	}

	for _, entry := range pgcred.Spec.Permissions {
		for _, dbName := range entry.Databases {
			if err := manager.EnsureDatabase(host, adminUser, adminPass, dbName); err != nil {
				return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhaseFailed,
					"DatabaseCreationFailed", err.Error()), err
			}
			if err := manager.EnsureUser(host, adminUser, adminPass, dbName, pgcred.Spec.Username, password, entry.Permissions, entry.Tables); err != nil {
				if isTableNotFoundError(err) {
					return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhasePending,
						"WaitingForTable", err.Error()), err
				}
				return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhaseFailed,
					"UserCreationFailed", err.Error()), err
			}
		}
	}

	if len(pgcred.Spec.ClusterRoles) > 0 {
		if err := manager.EnsureRoleMemberships(host, adminUser, adminPass, pgcred.Spec.Username, pgcred.Spec.ClusterRoles); err != nil {
			return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhaseFailed,
				"ClusterRoleGrantFailed", err.Error()), err
		}
	}

	if !secretFound {
		// PGDATABASE is only set when the credential targets exactly one
		// database; otherwise applications must name the target explicitly.
		secretData := map[string]string{
			"PGUSER":     pgcred.Spec.Username,
			"PGPASSWORD": password,
			"PGHOST":     host,
			"PGPORT":     fmt.Sprintf("%d", postgresPort),
		}
		if db := singleDatabase(pgcred.Spec.Permissions); db != "" {
			secretData["PGDATABASE"] = db
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pgcred.Spec.SecretName,
				Namespace: pgcred.Namespace,
				Labels:    labelsForCredential(pgcred, r.InstanceName),
			},
			StringData: secretData,
		}
		if err := r.client.createOwned(ctx, pgcred, secret); err != nil {
			return ctrl.Result{}, fmt.Errorf("creating credential Secret: %w", err)
		}
	}

	if err := manager.EnableUser(host, adminUser, adminPass, pgcred.Spec.Username); err != nil {
		return ctrl.Result{}, err
	}
	meta.SetStatusCondition(&pgcred.Status.Conditions, metav1.Condition{Type: "PolicyEnforced", Status: metav1.ConditionTrue, Reason: "EffectivePolicyV1", Message: policyDigest, ObservedGeneration: pgcred.Generation})
	pgcred.Status.SecretName = pgcred.Spec.SecretName
	return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhaseReady,
		"CredentialReady", "Postgres user and credential Secret are ready"), nil
}

// reconcileDelete handles cleanup when a PostgresCredential is being deleted.
// It drops the Postgres user and deletes the credential Secret.
func (r *PostgresCredentialReconciler) reconcileDelete(ctx context.Context, pgcred *v1alpha1.PostgresCredential) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(pgcred, credentialFinalizerName) {
		return ctrl.Result{}, nil
	}

	logger.Info("running credential finalizer cleanup")

	var pgdb v1alpha1.PostgresDatabase
	found, err := r.client.get(ctx, types.NamespacedName{Name: pgcred.Spec.DatabaseRef, Namespace: pgcred.Namespace}, &pgdb)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !found || pgdb.Status.Phase != v1alpha1.DatabasePhaseReady || pgdb.Status.SecretName == "" {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf("cannot verify backend credential revocation while database is unavailable")
	}
	var adminSecret corev1.Secret
	found, err = r.client.get(ctx, types.NamespacedName{Name: pgdb.Status.SecretName, Namespace: pgdb.Namespace}, &adminSecret)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !found {
		return ctrl.Result{}, fmt.Errorf("cannot verify backend credential revocation without admin credentials")
	}
	var secret corev1.Secret
	found, err = r.client.get(ctx, types.NamespacedName{Name: pgcred.Spec.SecretName, Namespace: pgcred.Namespace}, &secret)
	if err != nil {
		return ctrl.Result{}, err
	}
	if found {
		owner := metav1.GetControllerOf(&secret)
		if owner == nil || owner.UID != pgcred.UID || owner.Kind != "PostgresCredential" || owner.Name != pgcred.Name || owner.APIVersion != v1alpha1.GroupVersion.String() {
			return ctrl.Result{}, fmt.Errorf("credential Secret has conflicting ownership")
		}
	}
	ownership, err := r.pgDB.AcquireUserOwnership(postgresHost(&pgdb), string(adminSecret.Data["PGUSER"]), string(adminSecret.Data["PGPASSWORD"]), pgcred.Spec.Username, string(pgcred.UID), pgcred.Generation, found && string(secret.Data["PGUSER"]) == pgcred.Spec.Username, credentialFence(ctx, r.reader, pgcred, client.ObjectKey{Namespace: pgcred.Namespace, Name: pgcred.Spec.SecretName}, &secret, found, true))
	if err != nil {
		return ctrl.Result{}, err
	}
	defer ownership.Close()
	manager := ownership.Manager()
	if err := manager.DropUser(postgresHost(&pgdb), string(adminSecret.Data["PGUSER"]), string(adminSecret.Data["PGPASSWORD"]), "postgres", pgcred.Spec.Username); err != nil {
		return ctrl.Result{}, fmt.Errorf("revoking credential before deletion: %w", err)
	}
	if found {
		if err := r.client.delete(ctx, &secret); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := ownership.Forget(); err != nil {
		return ctrl.Result{}, err
	}

	// Remove the finalizer so the CR can be garbage-collected.
	controllerutil.RemoveFinalizer(pgcred, credentialFinalizerName)
	if err := r.client.update(ctx, pgcred); err != nil {
		if isConflict(err) || isNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	logger.Info("credential finalizer cleanup complete")
	return ctrl.Result{}, nil
}

// setCredentialPhase mutates the PostgresCredential status phase and condition
// in memory. The caller is responsible for persisting via r.Status().Update().
func (r *PostgresCredentialReconciler) setCredentialPhase(
	pgcred *v1alpha1.PostgresCredential,
	phase v1alpha1.CredentialPhase,
	reason, message string,
) ctrl.Result {
	pgcred.Status.Phase = phase

	conditionStatus := metav1.ConditionFalse
	if phase == v1alpha1.CredentialPhaseReady {
		conditionStatus = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&pgcred.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: pgcred.Generation,
	})

	if phase == v1alpha1.CredentialPhasePending {
		return ctrl.Result{RequeueAfter: 5 * time.Second}
	}

	return ctrl.Result{}
}

// ---------- Helpers ----------

// singleDatabase returns the database name when the permissions entries
// collectively reference exactly one database, and "" otherwise.
func singleDatabase(entries []v1alpha1.DatabasePermissionEntry) string {
	var found string
	for _, entry := range entries {
		for _, db := range entry.Databases {
			if found == "" {
				found = db
			} else if db != found {
				return ""
			}
		}
	}
	return found
}

// postgresHost returns the in-cluster DNS name for the Postgres instance
// backed by a headless Service. The StatefulSet pod is <name>-0.<name>.<ns>.svc.cluster.local.
func postgresHost(pgdb *v1alpha1.PostgresDatabase) string {
	return fmt.Sprintf("%s-0.%s.%s.svc.cluster.local", pgdb.Name, pgdb.Name, pgdb.Namespace)
}

// labelsForCredential returns the standard label set for resources owned by a
// PostgresCredential.
func labelsForCredential(pgcred *v1alpha1.PostgresCredential, instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":                                   "postgres-credential",
		"app.kubernetes.io/instance":                               pgcred.Name,
		"app.kubernetes.io/managed-by":                             "db-operator",
		"db-operator.benjamin-wright.github.com/operator-instance": instanceName,
	}
}

// SetupWithManager registers the PostgresCredentialReconciler with the controller manager.
func (r *PostgresCredentialReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.client = postgresCredentialClient{inner: mgr.GetClient(), scheme: mgr.GetScheme()}
	r.reader = mgr.GetAPIReader()
	r.pgDB = postgresManager{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PostgresCredential{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
