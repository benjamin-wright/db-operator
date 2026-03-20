package controller

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
)

// redisDatabaseBuilder constructs the desired Kubernetes resources for a
// RedisDatabase instance. It owns the "how" — the shape of each resource —
// leaving the reconciler free to own the "when".
type redisDatabaseBuilder struct {
	instanceName string
	scheme       *runtime.Scheme
}

func (b redisDatabaseBuilder) desiredAdminSecret(rdb *v1alpha1.RedisDatabase) (*corev1.Secret, error) {
	password, err := generatePassword(24)
	if err != nil {
		return nil, fmt.Errorf("generating admin password: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redisAdminSecretName(rdb),
			Namespace: rdb.Namespace,
			Labels:    labelsForRedisDatabase(rdb, b.instanceName),
		},
		StringData: map[string]string{
			"REDIS_USERNAME": "default",
			"REDIS_PASSWORD": password,
		},
	}
	_ = controllerutil.SetControllerReference(rdb, secret, b.scheme)
	return secret, nil
}

func (b redisDatabaseBuilder) desiredService(rdb *v1alpha1.RedisDatabase) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redisServiceName(rdb),
			Namespace: rdb.Namespace,
			Labels:    labelsForRedisDatabase(rdb, b.instanceName),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  labelsForRedisDatabase(rdb, b.instanceName),
			Ports: []corev1.ServicePort{
				{
					Name:       "redis",
					Port:       redisPort,
					TargetPort: intstr.FromInt32(redisPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(rdb, svc, b.scheme)
	return svc
}

func (b redisDatabaseBuilder) desiredStatefulSet(rdb *v1alpha1.RedisDatabase) *appsv1.StatefulSet {
	replicas := int32(1)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redisStatefulSetName(rdb),
			Namespace: rdb.Namespace,
			Labels:    labelsForRedisDatabase(rdb, b.instanceName),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: redisServiceName(rdb),
			Selector: &metav1.LabelSelector{
				MatchLabels: labelsForRedisDatabase(rdb, b.instanceName),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labelsForRedisDatabase(rdb, b.instanceName),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "redis",
							Image:   redisImage,
							Command: []string{"redis-server", "--requirepass", "$(REDIS_PASSWORD)"},
							Ports: []corev1.ContainerPort{
								{
									Name:          "redis",
									ContainerPort: redisPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name: "REDIS_PASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: redisAdminSecretName(rdb),
											},
											Key: "REDIS_PASSWORD",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "data",
									MountPath: "/data",
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"sh", "-c",
											"redis-cli -a $REDIS_PASSWORD ping",
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
											"sh", "-c",
											"redis-cli -a $REDIS_PASSWORD ping",
										},
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       10,
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
								corev1.ResourceStorage: resource.MustParse(rdb.Spec.StorageSize.String()),
							},
						},
					},
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(rdb, sts, b.scheme)
	return sts
}

// ---------- Naming helpers ----------

func redisStatefulSetName(rdb *v1alpha1.RedisDatabase) string {
	return rdb.Name
}

func redisServiceName(rdb *v1alpha1.RedisDatabase) string {
	return rdb.Name
}

func redisAdminSecretName(rdb *v1alpha1.RedisDatabase) string {
	return rdb.Name + "-admin"
}

func labelsForRedisDatabase(rdb *v1alpha1.RedisDatabase, instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":                                   "redis",
		"app.kubernetes.io/instance":                               rdb.Name,
		"app.kubernetes.io/managed-by":                             "db-operator",
		"db-operator.benjamin-wright.github.com/operator-instance": instanceName,
	}
}
