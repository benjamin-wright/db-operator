package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	"github.com/lib/pq"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const migrationLockKey = int64(0x5f6d6967726174)

type migrationFenceSession struct {
	conn    *sql.Conn
	manager postgresManager
}

func (s migrationFenceSession) disarm(ctx context.Context, claimUID string, operationName ...string) error {
	if s.conn == nil {
		return nil
	}
	var exists bool
	if err := s.conn.QueryRowContext(ctx, "SELECT to_regclass('__dbop_control.migration_authority') IS NOT NULL").Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	query := "DELETE FROM __dbop_control.migration_authority WHERE claim_uid=$1"
	args := []any{claimUID}
	if len(operationName) > 0 {
		query += " AND operation_name=$2"
		args = append(args, operationName[0])
	}
	_, err := s.conn.ExecContext(ctx, query, args...)
	return err
}

func migrationLaunchBlocked(pgms *v1alpha1.PostgresMigrationSet) bool {
	op := pgms.Status.Operation
	return pgms.Spec.Paused || op.Artifact != pgms.Spec.Artifact || op.TargetRevision != pgms.Spec.TargetRevision
}

func validateMigrationLaunch(expected, live *v1alpha1.PostgresMigrationSet) error {
	op, current := expected.Status.Operation, live.Status.Operation
	if live.UID != expected.UID || !live.DeletionTimestamp.IsZero() || live.Generation != expected.Generation ||
		live.Spec.DatabaseRef != expected.Spec.DatabaseRef || live.Spec.Database != expected.Spec.Database ||
		op == nil || current == nil || current.Phase != "Pending" || current.JobRef.UID != "" ||
		current.JobRef.Name != op.JobRef.Name || current.Artifact != op.Artifact || current.ResolvedArtifact != op.ResolvedArtifact ||
		current.TargetRevision != op.TargetRevision || current.VerificationOnly != op.VerificationOnly || current.RetryAt != op.RetryAt || migrationLaunchBlocked(live) {
		return fmt.Errorf("migration operation is no longer eligible to launch")
	}
	return nil
}

var errMigrationWithdrawalChanged = errors.New("migration operation changed during withdrawal")

func (r *PostgresMigrationSetReconciler) withdrawPendingOperation(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet, claim *coordinationv1.Lease) (ctrl.Result, error) {
	withdraw := func() error {
		op := pgms.Status.Operation
		now := metav1.Now()
		op.Phase, op.Reason = "Failed", migrationOperationWithdrawn
		op.Message = "Unlaunched operation withdrawn; resuming reserves a new execution identity"
		op.FinishedAt = &now
		pgms.Status.ActiveJob = ""
		reason := migrationOperationWithdrawn
		if pgms.Spec.Paused {
			reason = "Paused"
		}
		r.setPhase(pgms, v1alpha1.MigrationSetPhasePending, reason, op.Message)
		return r.client.updateStatus(ctx, pgms)
	}
	// Arming must first replace this marker in a successful status CAS. This
	// permits withdrawal before the database exists without guessing about a
	// previous create or SQL grant. A lost CAS retains the journal for retry.
	if pgms.Status.Operation.Reason == migrationAuthorityPending {
		return ctrl.Result{Requeue: true}, withdraw()
	}
	err := r.withMigrationFence(ctx, pgms, func(session migrationFenceSession) error {
		var current coordinationv1.Lease
		if err := r.claims.reader.Get(ctx, client.ObjectKeyFromObject(claim), &current); err != nil {
			return err
		}
		var live v1alpha1.PostgresMigrationSet
		if err := r.claims.reader.Get(ctx, client.ObjectKeyFromObject(pgms), &live); err != nil {
			return err
		}
		if current.UID != claim.UID || !current.DeletionTimestamp.IsZero() || live.UID != pgms.UID || !live.DeletionTimestamp.IsZero() ||
			live.Status.Operation == nil || live.Status.Operation.Phase != "Pending" || live.Status.Operation.JobRef != pgms.Status.Operation.JobRef || !migrationLaunchBlocked(&live) {
			return errMigrationWithdrawalChanged
		}
		var job batchv1.Job
		if err := r.claims.reader.Get(ctx, client.ObjectKey{Namespace: live.Namespace, Name: live.Status.Operation.JobRef.Name}, &job); !apierrors.IsNotFound(err) {
			if err != nil {
				return err
			}
			return errMigrationWithdrawalChanged
		}
		// The SQL lock excludes already running execution. Exact identity keeps a
		// delayed withdrawal from revoking a newer operation's authority.
		if err := session.disarm(ctx, string(claim.UID), live.Status.Operation.JobRef.Name); err != nil {
			return err
		}
		*pgms = live
		return withdraw()
	})
	if errors.Is(err, errMigrationWithdrawalChanged) {
		return ctrl.Result{Requeue: true}, nil
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, err
}

// A cluster-level target lock covers database creation and a database-level
// lock covers SQL execution. All locks use dedicated connections. The callback
// runs only after all preceding executions have released their session.
func (r *PostgresMigrationSetReconciler) withMigrationFence(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet, fn func(migrationFenceSession) error, prepare ...*coordinationv1.Lease) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var pgdb v1alpha1.PostgresDatabase
	if err := r.claims.reader.Get(ctx, client.ObjectKey{Namespace: pgms.Namespace, Name: pgms.Spec.DatabaseRef}, &pgdb); err != nil {
		return err
	}
	var secret corev1.Secret
	if err := r.claims.reader.Get(ctx, client.ObjectKey{Namespace: pgms.Namespace, Name: pgdb.Status.SecretName}, &secret); err != nil {
		return err
	}
	user, password := string(secret.Data["PGUSER"]), string(secret.Data["PGPASSWORD"])
	db, err := openPostgres(postgresHost(&pgdb), user, password, "postgres")
	if err != nil {
		return err
	}
	defer db.Close()
	guard, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer guard.Close()
	key, _ := migrationTarget(pgms)
	var locked bool
	if err := guard.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", key).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("migration target guard is busy")
	}
	defer guard.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1,0))", key)
	manager := postgresManager{boundSessions: map[string]*sql.Conn{"postgres": guard}}
	if len(prepare) > 0 {
		var current coordinationv1.Lease
		if err := r.claims.reader.Get(ctx, client.ObjectKeyFromObject(prepare[0]), &current); err != nil {
			return err
		}
		var live v1alpha1.PostgresMigrationSet
		if err := r.claims.reader.Get(ctx, client.ObjectKeyFromObject(pgms), &live); err != nil {
			return err
		}
		if current.UID != prepare[0].UID || !current.DeletionTimestamp.IsZero() || live.UID != pgms.UID || !live.DeletionTimestamp.IsZero() {
			return fmt.Errorf("migration authority changed before provisioning")
		}
		if err := validateMigrationLaunch(pgms, &live); err != nil {
			return err
		}
	}
	var exists bool
	if err := guard.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", pgms.Spec.Database).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if len(prepare) == 0 {
			return fn(migrationFenceSession{manager: manager})
		}
		if err := manager.EnsureDatabase(postgresHost(&pgdb), user, password, pgms.Spec.Database); err != nil {
			return err
		}
	}
	target, err := openPostgres(postgresHost(&pgdb), user, password, pgms.Spec.Database)
	if err != nil {
		return err
	}
	defer target.Close()
	session, err := target.Conn(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	if err := session.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", migrationLockKey).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("migration SQL execution is still active")
	}
	defer session.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockKey)
	manager.boundSessions[pgms.Spec.Database] = session
	if len(prepare) > 0 {
		result, err := r.ensureMigrationDatabase(ctx, pgms, manager)
		if err != nil {
			return err
		}
		if result.RequeueAfter > 0 {
			return fmt.Errorf("migration database is not ready")
		}
	}
	return fn(migrationFenceSession{conn: session, manager: manager})
}

func (r *PostgresMigrationSetReconciler) armMigrationAuthority(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet, claim *coordinationv1.Lease) error {
	return r.withMigrationFence(ctx, pgms, func(session migrationFenceSession) error {
		var current coordinationv1.Lease
		if err := r.claims.reader.Get(ctx, client.ObjectKeyFromObject(claim), &current); err != nil {
			return err
		}
		if current.UID != claim.UID || !current.DeletionTimestamp.IsZero() {
			return fmt.Errorf("migration target claim changed")
		}
		var live v1alpha1.PostgresMigrationSet
		if err := r.claims.reader.Get(ctx, client.ObjectKeyFromObject(pgms), &live); err != nil {
			return err
		}
		if err := validateMigrationLaunch(pgms, &live); err != nil {
			return err
		}
		if session.conn == nil {
			return fmt.Errorf("migration database is not ready")
		}
		// Persist before granting SQL authority. A concurrent withdrawal either
		// wins this CAS or must acquire the SQL lock to disarm the exact grant.
		live.Status.Operation.Reason = migrationAuthorityArming
		if err := r.client.updateStatus(ctx, &live); err != nil {
			return err
		}
		*pgms = live
		role := migrationDatabaseRole(pgms)
		if _, err := session.conn.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS __dbop_control; REVOKE ALL ON SCHEMA __dbop_control FROM PUBLIC; CREATE TABLE IF NOT EXISTS __dbop_control.migration_authority (singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),claim_uid TEXT NOT NULL,operation_name TEXT NOT NULL DEFAULT ''); ALTER TABLE __dbop_control.migration_authority ADD COLUMN IF NOT EXISTS operation_name TEXT NOT NULL DEFAULT ''; REVOKE ALL ON __dbop_control.migration_authority FROM PUBLIC`); err != nil {
			return err
		}
		if _, err := session.conn.ExecContext(ctx, "GRANT USAGE ON SCHEMA __dbop_control TO "+pq.QuoteIdentifier(role)+"; GRANT SELECT ON __dbop_control.migration_authority TO "+pq.QuoteIdentifier(role)); err != nil {
			return err
		}
		if _, err := session.conn.ExecContext(ctx, "INSERT INTO __dbop_control.migration_authority(singleton,claim_uid,operation_name) VALUES(TRUE,$1,$2) ON CONFLICT(singleton) DO UPDATE SET claim_uid=EXCLUDED.claim_uid,operation_name=EXCLUDED.operation_name", string(claim.UID), pgms.Status.Operation.JobRef.Name); err != nil {
			return err
		}
		if pgms.Annotations == nil {
			pgms.Annotations = map[string]string{}
		}
		pgms.Annotations[migrationClaimAnnotation] = string(claim.UID)
		return nil
	}, claim)
}
