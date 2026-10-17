package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const claimNamespaceAnnotation = "db-operator.benjamin-wright.github.com/migration-set-namespace"
const claimTargetAnnotation = "db-operator.benjamin-wright.github.com/migration-target"
const claimLabel = "db-operator.benjamin-wright.github.com/migration-claim"

type migrationClaims struct {
	client    client.Client
	reader    client.Reader
	namespace string
}

func migrationTarget(pgms *v1alpha1.PostgresMigrationSet) (string, string) {
	target, _ := json.Marshal([3]string{pgms.Namespace, pgms.Spec.DatabaseRef, pgms.Spec.Database})
	hash := sha256.Sum256(target)
	return fmt.Sprintf("pgms-%x", hash), string(target)
}

func (c *migrationClaims) ensure(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet, dryRun bool) (*coordinationv1.Lease, error) {
	if c == nil || c.namespace == "" {
		return nil, fmt.Errorf("migration claim namespace is not configured")
	}
	name, target := migrationTarget(pgms)
	// This read also makes upgrading existing objects fail closed on duplicates.
	var existing v1alpha1.PostgresMigrationSetList
	if err := c.reader.List(ctx, &existing, client.InNamespace(pgms.Namespace)); err != nil {
		return nil, err
	}
	for _, other := range existing.Items {
		if other.Name != pgms.Name && other.Spec.DatabaseRef == pgms.Spec.DatabaseRef && other.Spec.Database == pgms.Spec.Database {
			return nil, fmt.Errorf("migration target is already bound to %s/%s", other.Namespace, other.Name)
		}
	}
	desired := &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: c.namespace, Name: name, Labels: map[string]string{claimLabel: "true"}, Annotations: map[string]string{migrationSetLabel: pgms.Name, claimNamespaceAnnotation: pgms.Namespace, claimTargetAnnotation: target}}}
	var claim coordinationv1.Lease
	err := c.reader.Get(ctx, client.ObjectKeyFromObject(desired), &claim)
	if apierrors.IsNotFound(err) {
		if dryRun {
			return desired, nil
		}
		if err = c.client.Create(ctx, desired); err == nil {
			return desired, nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		err = c.reader.Get(ctx, client.ObjectKeyFromObject(desired), &claim)
	}
	if err != nil {
		return nil, err
	}
	if claim.Annotations[migrationSetLabel] != pgms.Name || claim.Annotations[claimNamespaceAnnotation] != pgms.Namespace || claim.Annotations[claimTargetAnnotation] != target || len(claim.OwnerReferences) != 0 || !claim.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("migration target claim ownership conflicts")
	}
	return &claim, nil
}

// A claim has no TTL and is not renewable: only proven absence releases it.
// The database advisory lock below serializes release with both arming and SQL.
type migrationClaimReaper struct {
	claims     *migrationClaims
	reconciler *PostgresMigrationSetReconciler
}

func (*migrationClaimReaper) NeedLeaderElection() bool { return true }
func (r *migrationClaimReaper) Start(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := r.reap(ctx); err != nil {
			log.FromContext(ctx).Error(err, "migration claim cleanup deferred")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (r *migrationClaimReaper) reap(ctx context.Context) error {
	var claims coordinationv1.LeaseList
	if err := r.claims.reader.List(ctx, &claims, client.InNamespace(r.claims.namespace), client.MatchingLabels{claimLabel: "true"}); err != nil {
		return err
	}
	var failures []error
	for i := range claims.Items {
		claim := &claims.Items[i]
		if time.Since(claim.CreationTimestamp.Time) < time.Minute {
			continue
		}
		if err := r.reapClaim(ctx, claim); err != nil {
			failures = append(failures, fmt.Errorf("claim %s: %w", claim.Name, err))
		}
	}
	return errors.Join(failures...)
}
func (r *migrationClaimReaper) reapClaim(ctx context.Context, claim *coordinationv1.Lease) error {
	var target [3]string
	if json.Unmarshal([]byte(claim.Annotations[claimTargetAnnotation]), &target) != nil {
		return fmt.Errorf("malformed migration claim")
	}
	pgms := &v1alpha1.PostgresMigrationSet{ObjectMeta: metav1.ObjectMeta{Namespace: target[0], Name: claim.Annotations[migrationSetLabel]}, Spec: v1alpha1.PostgresMigrationSetSpec{DatabaseRef: target[1], Database: target[2]}}
	expected, _ := migrationTarget(pgms)
	if expected != claim.Name || target[0] != claim.Annotations[claimNamespaceAnnotation] || pgms.Name == "" || len(claim.OwnerReferences) != 0 {
		return fmt.Errorf("malformed migration claim ownership")
	}
	absent, err := r.absent(ctx, pgms)
	if err != nil || !absent {
		return err
	}
	return r.reconciler.withMigrationFence(ctx, pgms, func(session migrationFenceSession) error {
		// Re-read after acquiring the SQL lock; a late resource can persist after an
		// admission reservation. It can execute only after re-establishing this claim.
		absent, err := r.absent(ctx, pgms)
		if err != nil || !absent {
			return err
		}
		if err := session.disarm(ctx, string(claim.UID)); err != nil {
			return err
		}
		uid, rv := claim.UID, claim.ResourceVersion
		err = r.claims.client.Delete(ctx, claim, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	})
}
func (r *migrationClaimReaper) absent(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet) (bool, error) {
	var current v1alpha1.PostgresMigrationSet
	if err := r.claims.reader.Get(ctx, client.ObjectKeyFromObject(pgms), &current); err == nil {
		return false, nil
	} else if !apierrors.IsNotFound(err) {
		return false, err
	}
	var jobs batchv1.JobList
	if err := r.claims.reader.List(ctx, &jobs, client.InNamespace(pgms.Namespace), client.MatchingLabels{migrationSetLabel: pgms.Name}); err != nil {
		return false, err
	}
	for _, job := range jobs.Items {
		if !isJobFinished(&job) {
			return false, nil
		}
	}
	var pods corev1.PodList
	if err := r.claims.reader.List(ctx, &pods, client.InNamespace(pgms.Namespace), client.MatchingLabels{migrationSetLabel: pgms.Name}); err != nil {
		return false, err
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			return false, nil
		}
	}
	return true, nil
}
