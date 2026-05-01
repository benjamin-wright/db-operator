package controller

import (
	"context"
	"fmt"
	"io"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

const (
	defaultJobTTL = time.Hour

	// migrationTriggerAnnotation is set on PostgresCredentials to force a
	// reconcile after a related migration Job succeeds. The credential
	// reconciler picks this up naturally because annotating an object causes
	// the controller-runtime informer to fire an update event.
	migrationTriggerAnnotation = "db-operator.benjamin-wright.github.com/last-migration-completed"
)

// PostgresMigrationSetReconciler reconciles a PostgresMigrationSet object.
type PostgresMigrationSetReconciler struct {
	InstanceName       string
	MigrationImage     string
	ServiceAccountName string
	client             postgresMigrationSetClient
	builder            postgresMigrationSetBuilder
	kube               kubernetes.Interface
	pgDB               PostgresManager
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

	if r.MigrationImage == "" {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "ConfigurationError", "migration image not configured; check MIGRATION_IMAGE env var")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	resolvedRef, err := r.client.resolveArtifact(ctx, pgms.Spec.Artifact)
	if err != nil {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "ArtifactResolutionFailed", err.Error())
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	pgms.Status.ObservedArtifact = resolvedRef

	if pgms.Spec.Paused {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "Paused", "migration set is paused")
		return ctrl.Result{}, nil
	}

	// Ensure the target logical database exists and the migrations role owns it
	// so that migration Jobs can run DDL without superuser-equivalent credentials.
	if stopped, result, err := r.reconcileMigrationsDatabase(ctx, pgms); stopped || err != nil {
		return result, err
	}

	key := migrationKey(pgms.Status.ObservedArtifact, pgms.Spec.TargetRevision)

	jobs, err := r.client.listOwnedJobs(ctx, pgms)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing owned Jobs: %w", err)
	}

	var matchingJob *batchv1.Job
	var inFlightOther bool
	for i := range jobs.Items {
		j := &jobs.Items[i]
		if j.Labels[migrationKeyLabel] == key {
			matchingJob = j
		} else if !isJobFinished(j) {
			inFlightOther = true
		}
	}

	if inFlightOther {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "WaitingForInFlightJob",
			"a prior migration Job is still running; waiting before applying the desired state")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if matchingJob != nil {
		if isJobSucceeded(matchingJob) {
			return r.handleSucceeded(ctx, pgms, matchingJob)
		}
		if isJobFailed(matchingJob) {
			return r.handleFailed(ctx, pgms, matchingJob)
		}
		r.setPhase(pgms, v1alpha1.MigrationSetPhaseRunning, "JobRunning", "migration Job is executing")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if result, err := r.ensureMigrationDatabase(ctx, pgms); err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	job := r.builder.desiredJob(pgms)
	if err := r.client.createOwned(ctx, pgms, job); err != nil {
		return ctrl.Result{}, fmt.Errorf("creating migration Job: %w", err)
	}
	r.setPhase(pgms, v1alpha1.MigrationSetPhaseRunning, "JobCreated", "migration Job has been created")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *PostgresMigrationSetReconciler) ensureMigrationDatabase(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet) (ctrl.Result, error) {
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

	if err := r.pgDB.EnsureDatabase(host, adminUser, adminPass, pgms.Spec.Database); err != nil {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "DatabaseCreationFailed", err.Error())
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	if err := r.pgDB.EnsureSchemaAccess(host, adminUser, adminPass, pgms.Spec.Database, migrationsRoleName); err != nil {
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, "SchemaAccessFailed", err.Error())
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *PostgresMigrationSetReconciler) handleSucceeded(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet, job *batchv1.Job) (ctrl.Result, error) {
	if pgms.Status.Phase != v1alpha1.MigrationSetPhaseReady {
		r.logJobPodOutput(ctx, job)
	}

	rev := pgms.Spec.TargetRevision
	pgms.Status.CurrentRevision = &rev
	r.setPhase(pgms, v1alpha1.MigrationSetPhaseReady, "JobSucceeded", "migrations applied successfully")

	if err := r.enqueueAffectedCredentials(ctx, pgms); err != nil {
		return ctrl.Result{}, fmt.Errorf("enqueuing affected credentials: %w", err)
	}

	ttl := defaultJobTTL
	if pgms.Spec.JobTTL != nil {
		ttl = pgms.Spec.JobTTL.Duration
	}

	if job.Status.CompletionTime != nil {
		deleteAt := job.Status.CompletionTime.Add(ttl)
		remaining := time.Until(deleteAt)
		if remaining <= 0 {
			if err := r.client.delete(ctx, job); err != nil {
				return ctrl.Result{}, fmt.Errorf("deleting completed Job: %w", err)
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	return ctrl.Result{}, nil
}

func (r *PostgresMigrationSetReconciler) handleFailed(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet, job *batchv1.Job) (ctrl.Result, error) {
	reason := "JobFailed"
	message := fmt.Sprintf("migration Job %q failed", job.Name)

	pods, err := r.client.listPodsForJob(ctx, job.Name, job.Namespace)
	if err == nil {
		for i := range pods.Items {
			for _, cs := range pods.Items[i].Status.ContainerStatuses {
				if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
					if cs.State.Terminated.Reason != "" {
						reason = cs.State.Terminated.Reason
					}
					if cs.State.Terminated.Message != "" {
						message = cs.State.Terminated.Message
					}
					break
				}
			}
		}
	}

	if pgms.Status.Phase != v1alpha1.MigrationSetPhaseFailed {
		r.logJobPodOutput(ctx, job)
	}

	r.setPhase(pgms, v1alpha1.MigrationSetPhaseFailed, reason, message)
	return ctrl.Result{}, nil
}

// logJobPodOutput fetches and logs the stdout/stderr of every container in the
// pods belonging to job. Errors are logged as warnings and do not fail the
// reconcile. This is called once per job completion transition so operators can
// inspect migration output without needing direct cluster access.
func (r *PostgresMigrationSetReconciler) logJobPodOutput(ctx context.Context, job *batchv1.Job) {
	logger := log.FromContext(ctx)
	if r.kube == nil {
		logger.Info("kubernetes client not available; skipping log collection", "job", job.Name)
		return
	}
	pods, err := r.client.listPodsForJob(ctx, job.Name, job.Namespace)
	if err != nil {
		logger.Info("could not list pods for log collection", "job", job.Name, "error", err)
		return
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		for _, c := range pod.Spec.Containers {
			req := r.kube.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: c.Name})
			rc, err := req.Stream(ctx)
			if err != nil {
				logger.Info("could not fetch container logs", "pod", pod.Name, "container", c.Name, "error", err)
				continue
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				logger.Info("could not read container logs", "pod", pod.Name, "container", c.Name, "error", err)
				continue
			}
			logger.Info("migration container output", "pod", pod.Name, "container", c.Name, "output", string(data))
		}
	}
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
	r.client = postgresMigrationSetClient{inner: mgr.GetClient(), scheme: mgr.GetScheme()}
	if r.pgDB == nil {
		r.pgDB = postgresManager{}
	}
	r.builder = postgresMigrationSetBuilder{
		migrationImage:     r.MigrationImage,
		serviceAccountName: r.ServiceAccountName,
		scheme:             mgr.GetScheme(),
	}
	r.pgDB = postgresManager{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PostgresMigrationSet{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
