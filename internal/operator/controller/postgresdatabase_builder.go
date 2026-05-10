package controller

import (
	"crypto/rand"
	"fmt"
	"math/big"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

// postgresDatabaseBuilder constructs the desired Kubernetes resources for a
// PostgresDatabase instance. It owns the "how" — the shape of each resource —
// leaving the reconciler free to own the "when".
type postgresDatabaseBuilder struct {
	instanceName string
	scheme       *runtime.Scheme
}

func (b postgresDatabaseBuilder) desiredAdminSecret(pgdb *v1alpha1.PostgresDatabase) (*corev1.Secret, error) {
	password, err := generatePassword(24)
	if err != nil {
		return nil, fmt.Errorf("generating admin password: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      adminSecretName(pgdb),
			Namespace: pgdb.Namespace,
			Labels:    labelsForDatabase(pgdb, b.instanceName),
		},
		StringData: map[string]string{
			"PGUSER":     "postgres",
			"PGPASSWORD": password,
		},
	}
	_ = controllerutil.SetControllerReference(pgdb, secret, b.scheme)
	return secret, nil
}

func (b postgresDatabaseBuilder) desiredMigrationsSecret(pgdb *v1alpha1.PostgresDatabase) (*corev1.Secret, error) {
	password, err := generatePassword(24)
	if err != nil {
		return nil, fmt.Errorf("generating migrations password: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migrationsSecretName(pgdb),
			Namespace: pgdb.Namespace,
			Labels:    labelsForDatabase(pgdb, b.instanceName),
		},
		StringData: map[string]string{
			"PGUSER":     migrationsRoleName,
			"PGPASSWORD": password,
		},
	}
	_ = controllerutil.SetControllerReference(pgdb, secret, b.scheme)
	return secret, nil
}

func (b postgresDatabaseBuilder) desiredService(pgdb *v1alpha1.PostgresDatabase) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(pgdb),
			Namespace: pgdb.Namespace,
			Labels:    labelsForDatabase(pgdb, b.instanceName),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  labelsForDatabase(pgdb, b.instanceName),
			Ports: []corev1.ServicePort{
				{
					Name:       "postgres",
					Port:       postgresPort,
					TargetPort: intstr.FromInt32(postgresPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(pgdb, svc, b.scheme)
	return svc
}

func (b postgresDatabaseBuilder) desiredStatefulSet(pgdb *v1alpha1.PostgresDatabase) *appsv1.StatefulSet {
	replicas := int32(1)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      statefulSetName(pgdb),
			Namespace: pgdb.Namespace,
			Labels:    labelsForDatabase(pgdb, b.instanceName),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: serviceName(pgdb),
			Selector: &metav1.LabelSelector{
				MatchLabels: labelsForDatabase(pgdb, b.instanceName),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labelsForDatabase(pgdb, b.instanceName),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "postgres",
							Image: fmt.Sprintf("postgres:%s", pgdb.Spec.PostgresVersion),
							Ports: []corev1.ContainerPort{
								{
									Name:          "postgres",
									ContainerPort: postgresPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "POSTGRES_USER",
									Value: "postgres",
								},
								{
									Name: "POSTGRES_PASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: adminSecretName(pgdb),
											},
											Key: "PGPASSWORD",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "data",
									MountPath: "/var/lib/postgresql/data",
									SubPath:   "pgdata",
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"pg_isready",
											"-U", "postgres",
											"-d", "postgres",
										},
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"pg_isready",
											"-U", "postgres",
											"-d", "postgres",
										},
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       10,
							},
							// postStart syncs the admin password from $POSTGRES_PASSWORD into
							// pg_authid on every pod start. It connects via Unix socket using
							// pg_hba.conf's "local all all trust" rule, so no password is needed
							// to issue the ALTER USER — making the Kubernetes Secret authoritative
							// even when the pod mounts an existing PVC with stale credentials.
							// Kubernetes requires this hook to exit before the container can be
							// marked Ready, so the operator will never attempt a TCP connection
							// until after the ALTER USER has executed.
							Lifecycle: &corev1.Lifecycle{
								PostStart: &corev1.LifecycleHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"/bin/sh", "-c",
											"timeout 60 sh -c 'until pg_isready -U postgres; do sleep 1; done' && psql -U postgres -c \"ALTER USER postgres PASSWORD '$POSTGRES_PASSWORD'\"",
										},
									},
								},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "data",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse(pgdb.Spec.StorageSize.String()),
							},
						},
					},
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(pgdb, sts, b.scheme)
	return sts
}

func statefulSetName(pgdb *v1alpha1.PostgresDatabase) string {
	return pgdb.Name
}

func serviceName(pgdb *v1alpha1.PostgresDatabase) string {
	return pgdb.Name
}

func adminSecretName(pgdb *v1alpha1.PostgresDatabase) string {
	return pgdb.Name + "-admin"
}

func migrationsSecretName(pgdb *v1alpha1.PostgresDatabase) string {
	return pgdb.Name + "-migrations-internal"
}

func generatePassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", fmt.Errorf("generating random byte: %w", err)
		}
		result[i] = charset[num.Int64()]
	}
	return string(result), nil
}

func labelsForDatabase(pgdb *v1alpha1.PostgresDatabase, instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":                                   "postgres",
		"app.kubernetes.io/instance":                               pgdb.Name,
		"app.kubernetes.io/managed-by":                             "db-operator",
		"db-operator.benjamin-wright.github.com/operator-instance": instanceName,
	}
}
