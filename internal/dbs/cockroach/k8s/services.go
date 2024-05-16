package k8s

import (
	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type CockroachServiceComparable struct {
	Name      string
	Namespace string
}

type CockroachService struct {
	CockroachServiceComparable
	UID             string
	ResourceVersion string
}

func (s CockroachService) ToUnstructured() *unstructured.Unstructured {
	statefulset := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      s.Name,
				"namespace": s.Namespace,
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

func cockroachServiceFromUnstructured(obj *unstructured.Unstructured) (CockroachService, error) {
	s := CockroachService{}

	s.Name = obj.GetName()
	s.Namespace = obj.GetNamespace()
	s.UID = string(obj.GetUID())
	s.ResourceVersion = obj.GetResourceVersion()

	return s, nil
}

func (s CockroachService) GetName() string {
	return s.Name
}

func (s CockroachService) GetNamespace() string {
	return s.Namespace
}

func (s CockroachService) GetUID() string {
	return s.UID
}

func (s CockroachService) GetResourceVersion() string {
	return s.ResourceVersion
}

func (s CockroachService) Equal(obj k8s_generic.Resource) bool {
	cockroachService, ok := obj.(*CockroachService)
	if !ok {
		return false
	}
	return s.CockroachServiceComparable == cockroachService.CockroachServiceComparable
}

func (c *Client) Services() *k8s_generic.Client[CockroachService] {
	return k8s_generic.NewClient(
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
		cockroachServiceFromUnstructured,
	)
}
