package controller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/benjamin-wright/db-operator/internal/natsconfig"
	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

// natsAccountBuilder constructs the desired Kubernetes resources for a NatsAccount instance.
type natsAccountBuilder struct {
	instanceName string
	scheme       *runtime.Scheme
}

func (b natsAccountBuilder) desiredUserSecret(acct *v1alpha1.NatsAccount, cluster *v1alpha1.NatsCluster, user v1alpha1.NatsUser, password string) *corev1.Secret {
	host := natsClusterHost(cluster)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      user.SecretName,
			Namespace: acct.Namespace,
			Labels:    labelsForNatsAccount(acct, b.instanceName),
		},
		StringData: map[string]string{
			"NATS_USERNAME": user.Username,
			"NATS_PASSWORD": password,
			"NATS_ACCOUNT":  acct.Name,
			"NATS_HOST":     host,
			"NATS_PORT":     fmt.Sprintf("%d", natsconfig.ClientPort),
		},
	}
	_ = controllerutil.SetControllerReference(acct, secret, b.scheme)
	return secret
}

// natsClusterHost returns the in-cluster DNS name for the NATS service of the given cluster.
func natsClusterHost(cluster *v1alpha1.NatsCluster) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", cluster.Name, cluster.Namespace)
}

// labelsForNatsAccount returns the standard label set for resources owned by a NatsAccount.
func labelsForNatsAccount(acct *v1alpha1.NatsAccount, instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":                                   "nats-account",
		"app.kubernetes.io/instance":                               acct.Name,
		"app.kubernetes.io/managed-by":                             "db-operator",
		"db-operator.benjamin-wright.github.com/operator-instance": instanceName,
	}
}
