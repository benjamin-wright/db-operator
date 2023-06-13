package k8s

import (
	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type CockroachServiceComparable struct {
	Name string
}

type CockroachService struct {
	CockroachServiceComparable
	UID             string
	ResourceVersion string
}

func (s *CockroachService) ToUnstructured(namespace string) *unstructured.Unstructured {
	statefulset := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name": s.Name,
				"labels": k8s_generic.Merge(map[string]string{
					"app":                           s.Name,
					"ponglehub.co.uk/resource-type": "cockroachdb",
				}, common.LABEL_FILTERS),
			},
			"spec": map[string]interface{}{
				"ports": []map[string]interface{}{
					{
						"name":       "grpc",
						"port":       26257,
						"protocol":   "TCP",
						"targetPort": "grpc",
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

func (s *CockroachService) FromUnstructured(obj *unstructured.Unstructured) error {
	s.Name = obj.GetName()
	s.UID = string(obj.GetUID())
	s.ResourceVersion = obj.GetResourceVersion()
	return nil
}

func (s *CockroachService) GetName() string {
	return s.Name
}

func (s *CockroachService) GetUID() string {
	return s.UID
}

func (s *CockroachService) GetResourceVersion() string {
	return s.ResourceVersion
}

func (s *CockroachService) Equal(obj CockroachService) bool {
	return s.CockroachServiceComparable == obj.CockroachServiceComparable
}

func (c *Client) Services() *k8s_generic.Client[CockroachService, *CockroachService] {
	return k8s_generic.NewClient[CockroachService](
		c.builder,
		schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "services",
		},
		"Service",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "cockroachdb",
		}, common.LABEL_FILTERS),
	)
}
