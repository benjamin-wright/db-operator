package controller

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/benjamin-wright/db-operator/internal/natsconfig"
	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

const (
	// natsImage is the base NATS server image name (version is appended from the CR spec).
	natsImage = "nats"

	// natsConfigKey is the filename for the NATS server config inside the ConfigMap.
	natsConfigKey = "nats.conf"

	// natsConfigMountPath is the directory inside the container where the config is mounted.
	natsConfigMountPath = "/etc/nats"
)

// natsClusterBuilder constructs the desired Kubernetes resources for a NatsCluster instance.
type natsClusterBuilder struct {
	instanceName string
	scheme       *runtime.Scheme
}

func (b natsClusterBuilder) desiredConfigMap(nats *v1alpha1.NatsCluster, config string) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      natsConfigMapName(nats),
			Namespace: nats.Namespace,
			Labels:    labelsForNatsCluster(nats, b.instanceName),
		},
		Data: map[string]string{natsConfigKey: config},
	}
	_ = controllerutil.SetControllerReference(nats, cm, b.scheme)
	return cm
}

func (b natsClusterBuilder) desiredService(nats *v1alpha1.NatsCluster) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      natsServiceName(nats),
			Namespace: nats.Namespace,
			Labels:    labelsForNatsCluster(nats, b.instanceName),
		},
		Spec: corev1.ServiceSpec{
			Selector: labelsForNatsCluster(nats, b.instanceName),
			Ports: []corev1.ServicePort{
				{
					Name:       "client",
					Port:       natsconfig.ClientPort,
					TargetPort: intstr.FromInt32(natsconfig.ClientPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(nats, svc, b.scheme)
	return svc
}

func (b natsClusterBuilder) desiredDeployment(nats *v1alpha1.NatsCluster, cfgChecksum string) *appsv1.Deployment {
	replicas := int32(1)

	volumes := []corev1.Volume{
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: natsConfigMapName(nats),
					},
				},
			},
		},
	}

	volumeMounts := []corev1.VolumeMount{
		{Name: "config", MountPath: natsConfigMountPath},
	}

	if nats.Spec.JetStream != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: natsJetStreamPVCName(nats),
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "data",
			MountPath: natsconfig.DataMountPath,
		})
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      natsDeploymentName(nats),
			Namespace: nats.Namespace,
			Labels:    labelsForNatsCluster(nats, b.instanceName),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labelsForNatsCluster(nats, b.instanceName),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labelsForNatsCluster(nats, b.instanceName),
					// Changing the config checksum annotation triggers a rolling restart
					// when the ConfigMap contents change.
					Annotations: map[string]string{
						"checksum/config": cfgChecksum,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nats",
							Image: fmt.Sprintf("%s:%s", natsImage, nats.Spec.NatsVersion),
							Args: []string{
								"--config", fmt.Sprintf("%s/%s", natsConfigMountPath, natsConfigKey),
							},
							Ports: []corev1.ContainerPort{
								{Name: "client", ContainerPort: natsconfig.ClientPort, Protocol: corev1.ProtocolTCP},
								{Name: "monitor", ContainerPort: natsconfig.MonitorPort, Protocol: corev1.ProtocolTCP},
							},
							VolumeMounts: volumeMounts,
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt32(natsconfig.MonitorPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt32(natsconfig.MonitorPort),
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       10,
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(nats, deploy, b.scheme)
	return deploy
}

// desiredJetStreamPVC constructs the PVC for NATS JetStream storage.
// Callers must only invoke this when nats.Spec.JetStream is non-nil.
func (b natsClusterBuilder) desiredJetStreamPVC(nats *v1alpha1.NatsCluster) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      natsJetStreamPVCName(nats),
			Namespace: nats.Namespace,
			Labels:    labelsForNatsCluster(nats, b.instanceName),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: nats.Spec.JetStream.StorageSize.DeepCopy(),
				},
			},
		},
	}
	_ = controllerutil.SetControllerReference(nats, pvc, b.scheme)
	return pvc
}

// ---------- Naming helpers ----------

func natsConfigMapName(nats *v1alpha1.NatsCluster) string {
	return nats.Name + "-config"
}

func natsServiceName(nats *v1alpha1.NatsCluster) string {
	return nats.Name
}

func natsDeploymentName(nats *v1alpha1.NatsCluster) string {
	return nats.Name
}

func natsJetStreamPVCName(nats *v1alpha1.NatsCluster) string {
	return nats.Name + "-jetstream"
}

func labelsForNatsCluster(nats *v1alpha1.NatsCluster, instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":                                   "nats",
		"app.kubernetes.io/instance":                               nats.Name,
		"app.kubernetes.io/managed-by":                             "db-operator",
		"db-operator.benjamin-wright.github.com/operator-instance": instanceName,
	}
}
