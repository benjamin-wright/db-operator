package controller

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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

	// Pure Go Postgres driver.
	"github.com/lib/pq"

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
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
	client.Client
	Scheme       *runtime.Scheme
	InstanceName string
}

// +kubebuilder:rbac:groups=games-hub.io,resources=postgrescredentials,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=games-hub.io,resources=postgrescredentials/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=games-hub.io,resources=postgrescredentials/finalizers,verbs=update
// +kubebuilder:rbac:groups=games-hub.io,resources=postgresdatabases,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles create/update/delete events for PostgresCredential resources.
func (r *PostgresCredentialReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the PostgresCredential instance.
	var pgcred v1alpha1.PostgresCredential
	if err := r.Get(ctx, req.NamespacedName, &pgcred); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("PostgresCredential resource not found; ignoring")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching PostgresCredential: %w", err)
	}

	// Handle deletion via finalizer.
	if !pgcred.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &pgcred)
	}

	// Ensure the finalizer is present.
	if !controllerutil.ContainsFinalizer(&pgcred, credentialFinalizerName) {
		controllerutil.AddFinalizer(&pgcred, credentialFinalizerName)
		if err := r.Update(ctx, &pgcred); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
	}

	result, reconcileErr := r.reconcileCredential(ctx, &pgcred)

	// Conflict means the cached object is stale; requeue without logging an error
	// and let the informer provide the latest version.
	// Forbidden typically means the namespace is terminating; stop without marking Failed.
	if apierrors.IsConflict(reconcileErr) {
		return ctrl.Result{Requeue: true}, nil
	}
	if apierrors.IsForbidden(reconcileErr) {
		return ctrl.Result{}, nil
	}

	if err := r.Status().Update(ctx, &pgcred); err != nil {
		if apierrors.IsConflict(err) {
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
	if err := r.Get(ctx, dbKey, &pgdb); err != nil {
		if apierrors.IsNotFound(err) {
			return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhasePending,
				"DatabaseNotFound", fmt.Sprintf("target PostgresDatabase %q not found", pgcred.Spec.DatabaseRef)), nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching target PostgresDatabase: %w", err)
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
	if err := r.Get(ctx, adminSecretKey, &adminSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhasePending,
				"AdminSecretNotFound", fmt.Sprintf("admin Secret %q not yet visible in cache", pgdb.Status.SecretName)), nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching admin Secret %q: %w", pgdb.Status.SecretName, err)
	}

	adminUser := string(adminSecret.Data["username"])
	adminPass := string(adminSecret.Data["password"])
	dbName := pgdb.Spec.DatabaseName
	host := postgresHost(&pgdb)

	var existingSecret corev1.Secret
	credSecretKey := types.NamespacedName{Name: pgcred.Spec.SecretName, Namespace: pgcred.Namespace}
	if err := r.Get(ctx, credSecretKey, &existingSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("fetching credential Secret: %w", err)
		}

		password, err := generatePassword(24)
		if err != nil {
			return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhaseFailed,
				"PasswordGenerationFailed", err.Error()), err
		}

		if err := r.ensurePostgresUser(host, adminUser, adminPass, dbName, pgcred.Spec.Username, password, pgcred.Spec.Permissions); err != nil {
			return r.setCredentialPhase(pgcred, v1alpha1.CredentialPhaseFailed,
				"UserCreationFailed", err.Error()), err
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pgcred.Spec.SecretName,
				Namespace: pgcred.Namespace,
				Labels:    labelsForCredential(pgcred, r.InstanceName),
			},
			StringData: map[string]string{
				"username": pgcred.Spec.Username,
				"password": password,
				"host":     host,
				"port":     fmt.Sprintf("%d", postgresPort),
				"database": dbName,
			},
		}
		if err := controllerutil.SetControllerReference(pgcred, secret, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("setting owner reference on credential Secret: %w", err)
		}
		if err := r.Create(ctx, secret); err != nil {
			return ctrl.Result{}, fmt.Errorf("creating credential Secret: %w", err)
		}
	}

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

	// Attempt to drop the Postgres user. We need the target database to still
	// exist and be reachable. If the database is already gone, we skip the
	// drop gracefully — the user will have been removed with the database.
	var pgdb v1alpha1.PostgresDatabase
	dbKey := types.NamespacedName{Name: pgcred.Spec.DatabaseRef, Namespace: pgcred.Namespace}
	if err := r.Get(ctx, dbKey, &pgdb); err == nil && pgdb.Status.Phase == v1alpha1.DatabasePhaseReady && pgdb.Status.SecretName != "" {
		var adminSecret corev1.Secret
		adminSecretKey := types.NamespacedName{Name: pgdb.Status.SecretName, Namespace: pgdb.Namespace}
		if err := r.Get(ctx, adminSecretKey, &adminSecret); err == nil {
			adminUser := string(adminSecret.Data["username"])
			adminPass := string(adminSecret.Data["password"])
			dbName := pgdb.Spec.DatabaseName
			host := postgresHost(&pgdb)

			if err := r.dropPostgresUser(host, adminUser, adminPass, dbName, pgcred.Spec.Username); err != nil {
				logger.Error(err, "failed to drop Postgres user during cleanup", "username", pgcred.Spec.Username)
				// Continue with finalizer removal — the database may be going away too.
			}
		}
	}

	// Delete the credential Secret if it exists.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pgcred.Spec.SecretName,
			Namespace: pgcred.Namespace,
		},
	}
	if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting credential Secret: %w", err)
	}

	// Remove the finalizer so the CR can be garbage-collected.
	controllerutil.RemoveFinalizer(pgcred, credentialFinalizerName)
	if err := r.Update(ctx, pgcred); err != nil {
		if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	logger.Info("credential finalizer cleanup complete")
	return ctrl.Result{}, nil
}

// validPermissions is the exhaustive set of SQL privilege keywords the
// controller may embed in DDL statements. Every permission value is checked
// against this set before being interpolated into a query, so even if the CRD
// validation were bypassed, only known-safe keywords reach SQL.
var validPermissions = map[v1alpha1.DatabasePermission]struct{}{
	v1alpha1.PermissionSelect:     {},
	v1alpha1.PermissionInsert:     {},
	v1alpha1.PermissionUpdate:     {},
	v1alpha1.PermissionDelete:     {},
	v1alpha1.PermissionTruncate:   {},
	v1alpha1.PermissionReferences: {},
	v1alpha1.PermissionTrigger:    {},
	v1alpha1.PermissionAll:        {},
}

// ensurePostgresUser connects to the target Postgres instance and creates the
// specified role with the given password and permissions if it does not already exist.
func (r *PostgresCredentialReconciler) ensurePostgresUser(host, adminUser, adminPass, dbName, username, password string, permissions []v1alpha1.DatabasePermission) error {
	db, err := openPostgres(host, adminUser, adminPass, dbName)
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer db.Close()

	// Check if the role already exists.
	var exists bool
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)", username).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking if role exists: %w", err)
	}

	if !exists {
		createSQL := fmt.Sprintf("CREATE ROLE %s WITH LOGIN PASSWORD %s",
			pq.QuoteIdentifier(username), pq.QuoteLiteral(password))
		if _, err := db.Exec(createSQL); err != nil {
			return fmt.Errorf("creating role %q: %w", username, err)
		}
	}

	// GRANT permissions on all tables in the database.
	if len(permissions) > 0 {
		// Validate every permission against the whitelist before building SQL.
		privs := make([]string, len(permissions))
		for i, p := range permissions {
			if _, ok := validPermissions[p]; !ok {
				return fmt.Errorf("unknown permission %q", p)
			}
			privs[i] = string(p)
		}
		privClause := strings.Join(privs, ", ")
		quotedUser := pq.QuoteIdentifier(username)

		grantSQL := fmt.Sprintf("GRANT %s ON ALL TABLES IN SCHEMA public TO %s",
			privClause, quotedUser)
		if _, err := db.Exec(grantSQL); err != nil {
			return fmt.Errorf("granting permissions to %q: %w", username, err)
		}

		// Also set default privileges so future tables get the same grants.
		defaultSQL := fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT %s ON TABLES TO %s",
			privClause, quotedUser)
		if _, err := db.Exec(defaultSQL); err != nil {
			return fmt.Errorf("setting default privileges for %q: %w", username, err)
		}
	}

	return nil
}

// dropPostgresUser connects to the target Postgres instance and drops the specified role.
func (r *PostgresCredentialReconciler) dropPostgresUser(host, adminUser, adminPass, dbName, username string) error {
	db, err := openPostgres(host, adminUser, adminPass, dbName)
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer db.Close()

	// Revoke all privileges and default privileges before dropping to avoid
	// dependency errors from ALTER DEFAULT PRIVILEGES grants.
	quotedUser := pq.QuoteIdentifier(username)

	revokeSQL := fmt.Sprintf("REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM %s",
		quotedUser)
	if _, err := db.Exec(revokeSQL); err != nil {
		// Ignore errors — the role may never have been granted anything.
		_ = err
	}

	revokeDefaultSQL := fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON TABLES FROM %s",
		quotedUser)
	if _, err := db.Exec(revokeDefaultSQL); err != nil {
		// Ignore errors — default privileges may not have been set.
		_ = err
	}

	dropSQL := fmt.Sprintf("DROP ROLE IF EXISTS %s", quotedUser)
	if _, err := db.Exec(dropSQL); err != nil {
		return fmt.Errorf("dropping role %q: %w", username, err)
	}

	return nil
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

// postgresHost returns the in-cluster DNS name for the Postgres instance
// backed by a headless Service. The StatefulSet pod is <name>-0.<name>.<ns>.svc.cluster.local.
func postgresHost(pgdb *v1alpha1.PostgresDatabase) string {
	return fmt.Sprintf("%s-0.%s.%s.svc.cluster.local", pgdb.Name, pgdb.Name, pgdb.Namespace)
}

// openPostgres opens a connection to a Postgres instance.
func openPostgres(host, user, password, dbName string) (*sql.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable connect_timeout=5",
		host, postgresPort, user, password, dbName)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	// Verify the connection is live.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PostgresCredential{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
