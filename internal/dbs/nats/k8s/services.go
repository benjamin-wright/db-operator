package k8s

import (
	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type NatsServiceComparable struct {
	Name      string
	Namespace string
}

type NatsService struct {
	NatsServiceComparable
	UID             string
	ResourceVersion string
}

func (s *NatsService) ToUnstructured() *unstructured.Unstructured {
	statefulset := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      s.Name,
				"namespace": s.Namespace,
				"labels": k8s_generic.Merge(map[string]string{
					"app":                           s.Name,
					"ponglehub.co.uk/resource-type": "nats",
				}, common.LABEL_FILTERS),
			},
			"spec": map[string]interface{}{
				"ports": []map[string]interface{}{
					{
						"name":       "tcp",
						"port":       4222,
						"protocol":   "TCP",
						"targetPort": "tcp",
					},
				},
				"selector": map[string]interface{}{
					"app": s.Name,
				},
			},
		},
	}

	return statefulset
}

func (s *NatsService) FromUnstructured(obj *unstructured.Unstructured) error {
	s.Name = obj.GetName()
	s.Namespace = obj.GetNamespace()
	s.UID = string(obj.GetUID())
	s.ResourceVersion = obj.GetResourceVersion()
	return nil
}

func (s *NatsService) GetName() string {
	return s.Name
}

func (s *NatsService) GetNamespace() string {
	return s.Namespace
}

func (s *NatsService) GetUID() string {
	return s.UID
}

func (s *NatsService) GetResourceVersion() string {
	return s.ResourceVersion
}

func (s *NatsService) Equal(obj NatsService) bool {
	return s.NatsServiceComparable == obj.NatsServiceComparable
}

func (c *Client) Services() *k8s_generic.Client[NatsService, *NatsService] {
	return k8s_generic.NewClient[NatsService](
		c.builder,
		schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "services",
		},
		"Service",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "nats",
		}, common.LABEL_FILTERS),
	)
}
