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
	Name string
	DB   string
}

type NatsSecret struct {
	NatsSecretComparable
	UID             string
	ResourceVersion string
}

func (s *NatsSecret) GetHost(namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", s.DB, namespace)
}

func encode(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}

func (s *NatsSecret) ToUnstructured(namespace string) *unstructured.Unstructured {
	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name": s.Name,
				"labels": k8s_generic.Merge(map[string]string{
					"app":                           s.Name,
					"ponglehub.co.uk/resource-type": "nats",
				}, common.LABEL_FILTERS),
			},
			"data": map[string]interface{}{
				"NATS_HOST": encode(s.GetHost(namespace)),
				"NATS_PORT": encode("4222"),
			},
		},
	}

	return secret
}

func (s *NatsSecret) FromUnstructured(obj *unstructured.Unstructured) error {
	s.Name = obj.GetName()

	s.UID = string(obj.GetUID())
	s.ResourceVersion = obj.GetResourceVersion()

	hostname, err := k8s_generic.GetEncodedProperty(obj, "data", "NATS_HOST")
	if err != nil {
		return fmt.Errorf("failed to get NATS_HOST: %+v", err)
	}
	s.DB = strings.Split(hostname, ".")[0]

	return nil
}

func (s *NatsSecret) GetName() string {
	return s.Name
}

func (s *NatsSecret) GetUID() string {
	return s.UID
}

func (s *NatsSecret) GetResourceVersion() string {
	return s.ResourceVersion
}

func (s *NatsSecret) Equal(obj NatsSecret) bool {
	return s.NatsSecretComparable == obj.NatsSecretComparable
}

func (c *Client) Secrets() *k8s_generic.Client[NatsSecret, *NatsSecret] {
	return k8s_generic.NewClient[NatsSecret](
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
	)
}
