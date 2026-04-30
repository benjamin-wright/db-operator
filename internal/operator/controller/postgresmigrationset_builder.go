package controller

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

const (
	// migrationKeyLabel is the label key used on migration Jobs to record the
	// digest-pinned artifact reference and target revision that the Job was
	// built for. The reconciler uses this label to identify whether an
	// in-flight or completed Job corresponds to the current desired state.
	migrationKeyLabel = "db-operator.benjamin-wright.github.com/migration-key"

	// migrationSetLabel is the label key used on migration Jobs to associate
	// them with a specific PostgresMigrationSet. The client uses this label
	// to list all Jobs owned by a given migration set without a field index.
	migrationSetLabel = "db-operator.benjamin-wright.github.com/migration-set"
)

// postgresMigrationSetBuilder constructs the desired Kubernetes Job for a
// PostgresMigrationSet instance.
type postgresMigrationSetBuilder struct {
	migrationImage     string
	serviceAccountName string
	scheme             *runtime.Scheme
}

// migrationKey returns a short, deterministic identifier derived from the
// digest-pinned artifact reference and the target revision. The key is the
// first 12 hex characters of the SHA-256 digest of
// "<observedArtifact>|<targetRevision>".
func migrationKey(observedArtifact string, targetRevision int64) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s", observedArtifact, strconv.FormatInt(targetRevision, 10))
	return fmt.Sprintf("%x", h.Sum(nil))[:12]
}

// desiredJob builds the migration Job for pgms. The caller must have already
// written a resolved digest reference into pgms.Status.ObservedArtifact before
// calling this method.
func (b postgresMigrationSetBuilder) desiredJob(pgms *v1alpha1.PostgresMigrationSet) *batchv1.Job {
	key := migrationKey(pgms.Status.ObservedArtifact, pgms.Spec.TargetRevision)
	backoffLimit := int32(0)
	secretName := pgms.Spec.DatabaseRef + "-migrations-internal"

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: pgms.Name + "-",
			Namespace:    pgms.Namespace,
			Labels: map[string]string{
				migrationSetLabel: pgms.Name,
				migrationKeyLabel: key,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						migrationSetLabel: pgms.Name,
						migrationKeyLabel: key,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: b.serviceAccountName,
					Containers: []corev1.Container{
						{
							Name:  "migrations",
							Image: b.migrationImage,
							Args: []string{
								"--artifact", pgms.Status.ObservedArtifact,
								"--target", strconv.FormatInt(pgms.Spec.TargetRevision, 10),
							},
							Env: []corev1.EnvVar{
								{
									Name: "PGUSER",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
											Key:                  "PGUSER",
										},
									},
								},
								{
									Name: "PGPASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
											Key:                  "PGPASSWORD",
										},
									},
								},
								{
									Name:  "PGHOST",
									Value: pgms.Spec.DatabaseRef,
								},
								{
									Name:  "PGDATABASE",
									Value: pgms.Spec.Database,
								},
								{
									Name:  "PGPORT",
									Value: "5432",
								},
							},
						},
					},
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(pgms, job, b.scheme)
	return job
}
