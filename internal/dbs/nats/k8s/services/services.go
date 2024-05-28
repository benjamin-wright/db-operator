package services

import (
	"github.com/benjamin-wright/db-operator/v2/internal/common"
	"github.com/benjamin-wright/db-operator/v2/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var ClientArgs = k8s_generic.ClientArgs[Resource]{
	Schema: schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "services",
	},
	Kind: "Service",
	LabelFilters: k8s_generic.Merge(map[string]string{
		"ponglehub.co.uk/resource-type": "nats",
	}, common.LABEL_FILTERS),
	FromUnstructured: fromUnstructured,
}

type Comparable struct {
	Name      string
	Namespace string
}

type Resource struct {
	Comparable
	UID             string
	ResourceVersion string
}

func (r Resource) ToUnstructured() *unstructured.Unstructured {
	statefulset := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      r.Name,
				"namespace": r.Namespace,
				"labels": k8s_generic.Merge(map[string]string{
					"app":                           r.Name,
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
					"app": r.Name,
				},
			},
		},
	}

	return statefulset
}

func fromUnstructured(obj *unstructured.Unstructured) (Resource, error) {
	r := Resource{}

	r.Name = obj.GetName()
	r.Namespace = obj.GetNamespace()
	r.UID = string(obj.GetUID())
	r.ResourceVersion = obj.GetResourceVersion()

	return r, nil
}

func (r Resource) GetName() string {
	return r.Name
}

func (r Resource) GetNamespace() string {
	return r.Namespace
}

func (r Resource) GetUID() string {
	return r.UID
}

func (r Resource) GetResourceVersion() string {
	return r.ResourceVersion
}

func (r Resource) Equal(obj k8s_generic.Resource) bool {
	if other, ok := obj.(*Resource); ok {
		return r.Comparable == other.Comparable
	}
	return false
}
