package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

const (
	defaultJobTTL = time.Hour

	// migrationTriggerAnnotation is set on PostgresCredentials to force a
	// reconcile after a related migration Job succeeds. The credential
	// reconciler picks this up naturally because annotating an object causes
	// the controller-runtime informer to fire an update event.
	migrationRetryAnnotation    = "db-operator.benjamin-wright.github.com/retryAt"
	migrationTriggerAnnotation  = "db-operator.benjamin-wright.github.com/last-migration-completed"
	migrationAuthorityPending   = "AuthorityPending"
	migrationAuthorityArming    = "AuthorityArming"
	migrationOperationWithdrawn = "OperationWithdrawn"
)

// PostgresMigrationSetReconciler reconciles a PostgresMigrationSet object.
type PostgresMigrationSetReconciler struct {
	ClaimNamespace           string
	claims                   *migrationClaims
	InstanceName             string
	MigrationImage           string
	MigrationImagePullPolicy corev1.PullPolicy
	ServiceAccountName       string
	// JobRegistryHost, when non-empty, replaces the registry host in the
	// ObservedArtifact reference that is passed to migration Job pods. Use
	// this when the operator resolves artifacts via a host-side registry
	// address (e.g. localhost:5001) that is not reachable from inside the
	// cluster, and pods must use a different address (e.g.
	// db-operator-registry.localhost:5000).
	JobRegistryHost string
	client          postgresMigrationSetClient
	builder         postgresMigrationSetBuilder
	kube            kubernetes.Interface
	pgDB            PostgresManager
}

// Reconcile handles create/update/delete events for PostgresMigrationSet resources.
func (r *PostgresMigrationSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pgms v1alpha1.PostgresMigrationSet
	found, err := r.client.get(ctx, req.NamespacedName, &pgms)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching PostgresMigrationSet: %w", err)
	}
	if !found {
		logger.Info("PostgresMigrationSet not found; ignoring")
		return ctrl.Result{}, nil
	}

	const finalizer = "db-operator.benjamin-wright.github.com/migration-execution"
	if !pgms.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&pgms, finalizer) {
			return ctrl.Result{}, nil
		}
		jobs, err := r.client.listOwnedJobs(ctx, &pgms)
		if err != nil {
			return ctrl.Result{}, err
		}
		for _, job := range jobs.Items {
			if !isJobFinished(&job) {
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
		}
		if err := r.revokeMigrationIdentity(ctx, &pgms); err != nil {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(&pgms, finalizer)
		return ctrl.Result{}, r.client.inner.Update(ctx, &pgms)
	}
	if !controllerutil.ContainsFinalizer(&pgms, finalizer) {
		controllerutil.AddFinalizer(&pgms, finalizer)
		if err := r.client.inner.Update(ctx, &pgms); err != nil {
			return ctrl.Result{}, err
		}
	}

	result, reconcileErr := r.reconcileMigrationSet(ctx, &pgms)

	if isConflict(reconcileErr) {
		return ctrl.Result{Requeue: true}, nil
	}
	if isForbidden(reconcileErr) {
		logger.V(1).Info("reconcile blocked by Forbidden error; namespace may be terminating", "error", reconcileErr)
		return ctrl.Result{}, nil
	}

	if err := r.client.updateStatus(ctx, &pgms); err != nil {
		if isConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return result, reconcileErr
}

func (r *PostgresMigrationSetReconciler) reconcileMigrationSet(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet) (ctrl.Result, error) {
	pgms.Status.ObservedGeneration = pgms.Generation
	claim, err := r.claims.ensure(ctx, pgms, false)
	if err != nil {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "TargetClaimConflict", "Migration target ownership could not be established")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}
	if r.MigrationImage == "" {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "ConfigurationError", "Migration image is not configured")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	jobs, err := r.client.listOwnedJobs(ctx, pgms)
	if err != nil {
		return ctrl.Result{}, err
	}
	op := pgms.Status.Operation
	// Older deployments may have produced a Job before journals were introduced.
	// Only an exact owned Job can establish its outcome; legacy status alone cannot.
	if op == nil && (pgms.Status.ActiveJob != "" || pgms.Status.Phase == v1alpha1.MigrationSetPhaseReady) {
		op = &v1alpha1.MigrationOperation{Generation: pgms.Generation, Artifact: pgms.Spec.Artifact, TargetRevision: pgms.Spec.TargetRevision, ResolvedArtifact: pgms.Status.ObservedArtifact, Phase: "Unknown", JobRef: v1alpha1.MigrationJobReference{Name: pgms.Status.ActiveJob}, Reason: "LegacyExecutionUnknown", Message: "Inspect the migration ledger and set retryAt to authorize recovery"}
		for i := range jobs.Items {
			j := &jobs.Items[i]
			if j.Name == op.JobRef.Name && migrationJobMatches(j, op) && ownedMigrationJob(j, pgms) {
				op.JobRef.UID = string(j.UID)
				op.Phase = "Running"
				op.Reason = "ImportedLegacyJob"
				break
			}
		}
		if op.Phase == "Unknown" && pgms.Status.Phase == v1alpha1.MigrationSetPhaseReady && pgms.Status.CurrentRevision != nil && *pgms.Status.CurrentRevision == pgms.Spec.TargetRevision && strings.Contains(op.ResolvedArtifact, "@sha256:") && (!strings.Contains(pgms.Spec.Artifact, "@sha256:") || pgms.Spec.Artifact == op.ResolvedArtifact) {
			name, err := newMigrationJobName()
			if err != nil {
				return ctrl.Result{}, err
			}
			op.VerificationOnly = true
			op.Phase = "Pending"
			op.JobRef = v1alpha1.MigrationJobReference{Name: name}
			op.Reason = "VerifyingLegacyLedger"
			op.Message = "Verifying exact ledger and hashes without executing migration SQL"
		}
		pgms.Status.Operation = op
		if err := r.client.updateStatus(ctx, pgms); err != nil {
			return ctrl.Result{}, err
		}
	}
	if op != nil && (op.Phase == "Pending" || op.Phase == "Running") {
		return r.observeOperation(ctx, pgms, claim, &jobs)
	}
	for i := range jobs.Items {
		if !isJobFinished(&jobs.Items[i]) {
			r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "WaitingForInFlightJob", "A previous migration Job is still active")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
	}
	retry := pgms.Annotations[migrationRetryAnnotation]
	changedRetry := retry != "" && retry != pgms.Status.LastHandledRetryAt
	same := op != nil && op.Artifact == pgms.Spec.Artifact && op.TargetRevision == pgms.Spec.TargetRevision
	if same && op.Phase == "Succeeded" {
		if changedRetry {
			pgms.Status.LastHandledRetryAt = retry
		}
		r.setPhase(pgms, v1alpha1.MigrationSetPhaseReady, "JobSucceeded", "The requested migration operation succeeded")
		return r.expireCompletedJobs(ctx, pgms, &jobs)
	}
	if same && !changedRetry && op.Reason != migrationOperationWithdrawn {
		r.setPhase(pgms, v1alpha1.MigrationSetPhaseFailed, op.Reason, op.Message)
		return ctrl.Result{}, nil
	}
	if pgms.Spec.Paused {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "Paused", "Migration set is paused")
		return ctrl.Result{}, nil
	}
	resolved, err := r.client.resolveArtifact(ctx, pgms.Spec.Artifact)
	if err != nil {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "ArtifactResolutionFailed", "The migration artifact could not be resolved")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, err
	}
	name, err := newMigrationJobName()
	if err != nil {
		return ctrl.Result{}, err
	}
	now := metav1.Now()
	pgms.Status.Operation = &v1alpha1.MigrationOperation{Generation: pgms.Generation, Artifact: pgms.Spec.Artifact, TargetRevision: pgms.Spec.TargetRevision, RetryAt: retry, ResolvedArtifact: resolved, Phase: "Pending", Reason: migrationAuthorityPending, JobRef: v1alpha1.MigrationJobReference{Name: name}, StartedAt: &now}
	if retry != "" {
		pgms.Status.LastHandledRetryAt = retry
	}
	pgms.Status.ObservedArtifact = resolved
	pgms.Status.ActiveJob = pgms.Status.Operation.JobRef.Name
	r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "OperationReserved", "Migration operation is durably reserved")
	// The request, retry token and Job identity are one optimistic status update.
	if err := r.client.updateStatus(ctx, pgms); err != nil {
		return ctrl.Result{}, err
	}
	return r.observeOperation(ctx, pgms, claim, &jobs)
}

func newMigrationJobName() (string, error) {
	random, err := generatePassword(20)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(random))
	return fmt.Sprintf("dbop-migrate-%x", digest[:16]), nil
}

func migrationJobMatches(job *batchv1.Job, op *v1alpha1.MigrationOperation) bool {
	if len(job.Spec.Template.Spec.Containers) != 1 {
		return false
	}
	args := job.Spec.Template.Spec.Containers[0].Args
	return len(args) >= 4 && args[0] == "--artifact" && args[1] == op.ResolvedArtifact && args[2] == "--target" && args[3] == strconv.FormatInt(op.TargetRevision, 10)
}

func (r *PostgresMigrationSetReconciler) observeOperation(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet, claim *coordinationv1.Lease, jobs *batchv1.JobList) (ctrl.Result, error) {
	op := pgms.Status.Operation
	var job *batchv1.Job
	for i := range jobs.Items {
		j := &jobs.Items[i]
		if j.Name == op.JobRef.Name {
			job = j
		} else if !isJobFinished(j) {
			r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "WaitingForInFlightJob", "A prior operation is still active")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
	}
	if job == nil {
		var exact batchv1.Job
		err := r.claims.reader.Get(ctx, client.ObjectKey{Namespace: pgms.Namespace, Name: op.JobRef.Name}, &exact)
		if err == nil {
			owner := metav1.GetControllerOf(&exact)
			if owner == nil || owner.UID != pgms.UID || owner.Name != pgms.Name || owner.Kind != "PostgresMigrationSet" || owner.APIVersion != v1alpha1.GroupVersion.String() {
				return r.unknownOperation(pgms, "JobIdentityMismatch", "The reserved Job name belongs to another object")
			}
			job = &exact
		} else if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}
	if job != nil {
		if !ownedMigrationJob(job, pgms) {
			return r.unknownOperation(pgms, "JobIdentityMismatch", "The recorded Job belongs to another migration owner")
		}
		if (op.JobRef.UID != "" && op.JobRef.UID != string(job.UID)) || (job.Labels[migrationOperationLabel] != op.JobRef.Name && !(op.Reason == "ImportedLegacyJob" && migrationJobMatches(job, op))) {
			return r.unknownOperation(pgms, "JobIdentityMismatch", "The migration Job identity does not match the reserved operation")
		}
		op.JobRef.UID = string(job.UID)
		if isJobFinished(job) {
			now := metav1.Now()
			op.FinishedAt = &now
			if isJobSucceeded(job) {
				op.Phase = "Succeeded"
				op.Reason = "JobSucceeded"
				op.Message = "Migration operation succeeded"
				copy := *op
				pgms.Status.LastSuccessfulOperation = &copy
				rev := op.TargetRevision
				pgms.Status.CurrentRevision = &rev
				if err := r.enqueueAffectedCredentials(ctx, pgms); err != nil {
					return ctrl.Result{}, err
				}
			} else {
				op.Phase = "Failed"
				op.Reason = "JobFailed"
				op.Message = "Migration Job failed; inspect its logs and the ledger before setting retryAt"
				if op.VerificationOnly {
					op.Phase = "Unknown"
					op.Reason = "LegacyLedgerUnverified"
					op.Message = "Legacy success could not be verified against the ledger; explicit recovery is required"
				}
			}
			// Preserve the outcome before cleanup, including when desired spec changed.
			if err := r.client.updateStatus(ctx, pgms); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		op.Phase = "Running"
		r.setPhase(pgms, v1alpha1.MigrationSetPhaseRunning, "JobRunning", "The reserved migration operation is running")
		if op.Artifact != pgms.Spec.Artifact || op.TargetRevision != pgms.Spec.TargetRevision {
			r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "WaitingForInFlightJob", "Waiting for the prior operation to finish before the new request")
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if op.JobRef.UID != "" {
		return r.unknownOperation(pgms, "ExecutionUnknown", "Recorded migration Job disappeared; inspect the ledger before setting retryAt")
	}
	if migrationLaunchBlocked(pgms) {
		return r.withdrawPendingOperation(ctx, pgms, claim)
	}
	if err := r.armMigrationAuthority(ctx, pgms, claim); err != nil {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	}
	op = pgms.Status.Operation
	desired := r.builder.desiredJob(pgms)
	if err := r.client.createOwned(ctx, pgms, desired); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	op.JobRef.UID = string(desired.UID)
	op.Phase = "Running"
	r.setPhase(pgms, v1alpha1.MigrationSetPhaseRunning, "JobCreated", "Migration Job was created from the reserved operation")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *PostgresMigrationSetReconciler) unknownOperation(pgms *v1alpha1.PostgresMigrationSet, reason, message string) (ctrl.Result, error) {
	pgms.Status.Operation.Phase = "Unknown"
	pgms.Status.Operation.Reason = reason
	pgms.Status.Operation.Message = message
	r.setPhase(pgms, v1alpha1.MigrationSetPhaseFailed, reason, message)
	return ctrl.Result{}, nil
}

func (r *PostgresMigrationSetReconciler) expireCompletedJobs(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet, jobs *batchv1.JobList) (ctrl.Result, error) {
	ttl := defaultJobTTL
	if pgms.Spec.JobTTL != nil {
		ttl = pgms.Spec.JobTTL.Duration
	}
	remaining := time.Duration(0)
	for i := range jobs.Items {
		j := &jobs.Items[i]
		if !isJobFinished(j) || j.Status.CompletionTime == nil {
			continue
		}
		wait := time.Until(j.Status.CompletionTime.Add(ttl))
		if wait <= 0 {
			if err := r.client.delete(ctx, j); err != nil {
				return ctrl.Result{}, err
			}
		} else if remaining == 0 || wait < remaining {
			remaining = wait
		}
	}
	return ctrl.Result{RequeueAfter: remaining}, nil
}

func (r *PostgresMigrationSetReconciler) ensureMigrationDatabase(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet, manager PostgresManager) (ctrl.Result, error) {
	var pgdb v1alpha1.PostgresDatabase
	dbKey := types.NamespacedName{Name: pgms.Spec.DatabaseRef, Namespace: pgms.Namespace}
	found, err := r.client.get(ctx, dbKey, &pgdb)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching PostgresDatabase %q: %w", pgms.Spec.DatabaseRef, err)
	}
	if !found {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "DatabaseNotFound",
			fmt.Sprintf("target PostgresDatabase %q not found", pgms.Spec.DatabaseRef))
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	if pgdb.Status.Phase != v1alpha1.DatabasePhaseReady {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "DatabaseNotReady",
			fmt.Sprintf("waiting for PostgresDatabase %q to become Ready", pgms.Spec.DatabaseRef))
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	if pgdb.Status.SecretName == "" {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "AdminSecretNotReady",
			"PostgresDatabase admin Secret name is not yet populated")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	var adminSecret corev1.Secret
	secretKey := types.NamespacedName{Name: pgdb.Status.SecretName, Namespace: pgdb.Namespace}
	secretFound, err := r.client.get(ctx, secretKey, &adminSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching admin Secret %q: %w", pgdb.Status.SecretName, err)
	}
	if !secretFound {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "AdminSecretNotFound",
			fmt.Sprintf("admin Secret %q not yet visible in cache", pgdb.Status.SecretName))
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	adminUser := string(adminSecret.Data["PGUSER"])
	adminPass := string(adminSecret.Data["PGPASSWORD"])
	host := fmt.Sprintf("%s-0.%s.%s.svc.cluster.local", pgdb.Name, pgdb.Name, pgdb.Namespace)

	if err := manager.EnsureDatabase(host, adminUser, adminPass, pgms.Spec.Database); err != nil {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "DatabaseCreationFailed", err.Error())
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	secretName := migrationCredentialName(pgms)
	var credentials corev1.Secret
	found, err = r.client.get(ctx, types.NamespacedName{Namespace: pgms.Namespace, Name: secretName}, &credentials)
	if err != nil {
		return ctrl.Result{}, err
	}
	role := migrationDatabaseRole(pgms)
	if !found {
		password, err := generatePassword(32)
		if err != nil {
			return ctrl.Result{}, err
		}
		credentials = corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: pgms.Namespace, Name: secretName}, Data: map[string][]byte{"PGUSER": []byte(role), "PGPASSWORD": []byte(password)}}
		if err := r.client.createOwned(ctx, pgms, &credentials); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		owner := metav1.GetControllerOf(&credentials)
		if owner == nil || owner.UID != pgms.UID || owner.Kind != "PostgresMigrationSet" || string(credentials.Data["PGUSER"]) != role {
			return ctrl.Result{}, fmt.Errorf("migration credential ownership mismatch")
		}
	}
	if err := manager.EnsureUserExists(host, adminUser, adminPass, role, string(credentials.Data["PGPASSWORD"])); err != nil {
		return ctrl.Result{}, err
	}
	if err := manager.EnsureOwner(host, adminUser, adminPass, pgms.Spec.Database, role); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// enqueueAffectedCredentials touches (annotation-patches) every PostgresCredential
// in the same namespace that references the same databaseRef and database as
// pgms. This causes the credential reconciler to re-run, resolving any
// WaitingForTable conditions that would have cleared once the migration ran.
func (r *PostgresMigrationSetReconciler) enqueueAffectedCredentials(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet) error {
	var credList v1alpha1.PostgresCredentialList
	if err := r.client.list(ctx, &credList, client.InNamespace(pgms.Namespace)); err != nil {
		return fmt.Errorf("listing PostgresCredentials: %w", err)
	}

	now := metav1.Now().UTC().Format(time.RFC3339)
	for i := range credList.Items {
		cred := &credList.Items[i]
		if cred.Spec.DatabaseRef != pgms.Spec.DatabaseRef {
			continue
		}
		if !credentialReferencesDatabase(cred, pgms.Spec.Database) {
			continue
		}

		patch := client.MergeFrom(cred.DeepCopy())
		if cred.Annotations == nil {
			cred.Annotations = map[string]string{}
		}
		cred.Annotations[migrationTriggerAnnotation] = now

		if err := r.client.inner.Patch(ctx, cred, patch); err != nil && !isNotFound(err) {
			return fmt.Errorf("patching credential %s/%s: %w", cred.Namespace, cred.Name, err)
		}
	}
	return nil
}

func credentialReferencesDatabase(cred *v1alpha1.PostgresCredential, database string) bool {
	for _, entry := range cred.Spec.Permissions {
		for _, db := range entry.Databases {
			if db == database {
				return true
			}
		}
	}
	return false
}

func isJobFinished(job *batchv1.Job) bool {
	return isJobSucceeded(job) || isJobFailed(job)
}

func isJobSucceeded(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isJobFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (r *PostgresMigrationSetReconciler) setPhase(pgms *v1alpha1.PostgresMigrationSet, phase v1alpha1.MigrationSetPhase, reason, message string) {
	pgms.Status.Phase = phase

	conditionStatus := metav1.ConditionFalse
	if phase == v1alpha1.MigrationSetPhaseReady {
		conditionStatus = metav1.ConditionTrue
	}

	apimeta.SetStatusCondition(&pgms.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: pgms.Generation,
	})
}

// SetupWithManager registers the PostgresMigrationSetReconciler with the controller manager.
func (r *PostgresMigrationSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	kube, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("creating kubernetes client: %w", err)
	}
	r.kube = kube
	r.client = postgresMigrationSetClient{inner: mgr.GetClient(), scheme: mgr.GetScheme(), jobRegistryHost: r.JobRegistryHost}
	r.builder = postgresMigrationSetBuilder{
		migrationImage:           r.MigrationImage,
		migrationImagePullPolicy: r.MigrationImagePullPolicy,
		serviceAccountName:       r.ServiceAccountName,
		jobRegistryHost:          r.JobRegistryHost,
		scheme:                   mgr.GetScheme(),
	}
	r.pgDB = postgresManager{}
	r.claims = &migrationClaims{client: mgr.GetClient(), reader: mgr.GetAPIReader(), namespace: r.ClaimNamespace}
	if r.ClaimNamespace == "" {
		return fmt.Errorf("claim namespace is required")
	}
	if err := mgr.Add(&migrationClaimReaper{claims: r.claims, reconciler: r}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PostgresMigrationSet{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

func (r *PostgresMigrationSetReconciler) revokeMigrationIdentity(ctx context.Context, migration *v1alpha1.PostgresMigrationSet) error {
	var database v1alpha1.PostgresDatabase
	found, err := r.client.get(ctx, types.NamespacedName{Namespace: migration.Namespace, Name: migration.Spec.DatabaseRef}, &database)
	if err != nil {
		return err
	}
	if !found || database.Status.Phase != v1alpha1.DatabasePhaseReady || database.Status.SecretName == "" {
		return fmt.Errorf("migration cleanup requires an available database")
	}
	var secret corev1.Secret
	found, err = r.client.get(ctx, types.NamespacedName{Namespace: migration.Namespace, Name: database.Status.SecretName}, &secret)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("migration cleanup requires admin credentials")
	}
	claim, err := r.claims.ensure(ctx, migration, false)
	if err != nil {
		return err
	}
	return r.withMigrationFence(ctx, migration, func(session migrationFenceSession) error {
		var live v1alpha1.PostgresMigrationSet
		if err := r.claims.reader.Get(ctx, client.ObjectKeyFromObject(migration), &live); err != nil {
			return err
		}
		var current coordinationv1.Lease
		if err := r.claims.reader.Get(ctx, client.ObjectKeyFromObject(claim), &current); err != nil {
			return err
		}
		if live.UID != migration.UID || live.DeletionTimestamp.IsZero() || current.UID != claim.UID || !current.DeletionTimestamp.IsZero() {
			return fmt.Errorf("migration cleanup ownership changed")
		}
		if err := session.disarm(ctx, string(claim.UID)); err != nil {
			return err
		}
		return session.manager.dropMigrationUser(migration.Spec.Database, migrationDatabaseRole(migration), string(secret.Data["PGUSER"]))
	})
}

func ownedMigrationJob(job *batchv1.Job, pgms *v1alpha1.PostgresMigrationSet) bool {
	owner := metav1.GetControllerOf(job)
	return owner != nil && owner.UID == pgms.UID && owner.Name == pgms.Name && owner.Kind == "PostgresMigrationSet" && owner.APIVersion == v1alpha1.GroupVersion.String()
}
