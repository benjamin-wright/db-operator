package k8s

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type NatsSecretComparable struct {
	Name      string
	Namespace string
	DB        DBRef
}

type NatsSecret struct {
	NatsSecretComparable
	UID             string
	ResourceVersion string
}

func (s NatsSecret) GetHost() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", s.DB.Name, s.DB.Namespace)
}

func encode(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}

func (s NatsSecret) ToUnstructured() *unstructured.Unstructured {
	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      s.Name,
				"namespace": s.Namespace,
				"labels": k8s_generic.Merge(map[string]string{
					"app":                           s.Name,
					"ponglehub.co.uk/resource-type": "nats",
				}, common.LABEL_FILTERS),
			},
			"data": map[string]interface{}{
				"NATS_HOST": encode(s.GetHost()),
				"NATS_PORT": encode("4222"),
			},
		},
	}

	return secret
}

func natsSecretFromUnstructured(obj *unstructured.Unstructured) (NatsSecret, error) {
	s := NatsSecret{}

	s.Name = obj.GetName()
	s.Namespace = obj.GetNamespace()

	s.UID = string(obj.GetUID())
	s.ResourceVersion = obj.GetResourceVersion()

	hostname, err := k8s_generic.GetEncodedProperty(obj, "data", "NATS_HOST")
	if err != nil {
		return s, fmt.Errorf("failed to get NATS_HOST: %+v", err)
	}
	s.DB.Name = strings.Split(hostname, ".")[0]
	s.DB.Namespace = strings.Split(hostname, ".")[1]

	return s, nil
}

func (s NatsSecret) GetName() string {
	return s.Name
}

func (s NatsSecret) GetNamespace() string {
	return s.Namespace
}

func (s NatsSecret) GetUID() string {
	return s.UID
}

func (s NatsSecret) GetResourceVersion() string {
	return s.ResourceVersion
}

func (s NatsSecret) Equal(obj k8s_generic.Resource) bool {
	other, ok := obj.(NatsSecret)
	if !ok {
		return false
	}
	return s.NatsSecretComparable == other.NatsSecretComparable
}

func (c *Client) Secrets() *k8s_generic.Client[NatsSecret] {
	return k8s_generic.NewClient(
		c.builder,
		schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "secrets",
		},
		"Secret",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "nats",
		}, common.LABEL_FILTERS),
		natsSecretFromUnstructured,
	)
}
