package clusters

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/v2/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var ClientArgs = k8s_generic.ClientArgs[Resource]{
	Schema: schema.GroupVersionResource{
		Group:    "ponglehub.co.uk",
		Version:  "v1alpha1",
		Resource: "postgresclusters",
	},
	Kind:             "PostgresCluster",
	FromUnstructured: fromUnstructured,
}

type Comparable struct {
	Name      string
	Namespace string
	Storage   string
	Ready     bool
}

type Resource struct {
	Comparable
	UID             string
	ResourceVersion string
}

func (r Resource) ToUnstructured() *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "PostgresCluster",
		"metadata": map[string]interface{}{
			"name":            r.Name,
			"namespace":       r.Namespace,
			"resourceVersion": r.ResourceVersion,
			"uid":             r.UID,
		},
		"spec": map[string]interface{}{
			"storage": r.Storage,
		},
		"status": map[string]interface{}{
			"ready": r.Ready,
		},
	})

	return result
}

func fromUnstructured(obj *unstructured.Unstructured) (Resource, error) {
	var err error
	r := Resource{}

	r.Name = obj.GetName()
	r.Namespace = obj.GetNamespace()
	r.UID = string(obj.GetUID())
	r.ResourceVersion = obj.GetResourceVersion()
	r.Storage, err = k8s_generic.GetProperty[string](obj, "spec", "storage")
	if err != nil {
		return r, fmt.Errorf("failed to get storage: %+v", err)
	}
	r.Ready, _, err = unstructured.NestedBool(obj.Object, "status", "ready")
	if err != nil {
		return r, fmt.Errorf("failed to get ready: %+v", err)
	}

	return r, nil
}

func (r Resource) GetID() string {
	return r.Name + "@" + r.Namespace
}

func (r Resource) GetName() string {
	return r.Name
}

func (r Resource) GetNamespace() string {
	return r.Namespace
}

func (r Resource) GetStorage() string {
	return r.Storage
}

func (r Resource) GetUID() string {
	return r.UID
}

func (r Resource) GetResourceVersion() string {
	return r.ResourceVersion
}

func (r Resource) Equal(obj k8s_generic.Resource) bool {
	if other, ok := obj.(Resource); ok {
		return r.Comparable == other.Comparable
	}

	return false
}
