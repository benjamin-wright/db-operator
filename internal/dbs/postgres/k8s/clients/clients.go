package clients

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
		Resource: "postgresclients",
	},
	Kind:             "PostgresClient",
	FromUnstructured: fromUnstructured,
}

const (
	PermissionAdmin string = "admin"
	PermissionWrite string = "writer"
	PermissionRead  string = "reader"
)

type Cluster struct {
	Name      string
	Namespace string
}

func (c Cluster) GetID() string {
	return c.Name + "@" + c.Namespace
}

type Comparable struct {
	Name       string
	Namespace  string
	Cluster    Cluster
	Username   string
	Secret     string
	Database   string
	Permission string
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
		"kind":       "PostgresClient",
		"metadata": map[string]interface{}{
			"name":      r.Name,
			"namespace": r.Namespace,
		},
		"spec": map[string]interface{}{
			"cluster": map[string]interface{}{
				"name":      r.Cluster.Name,
				"namespace": r.Cluster.Namespace,
			},
			"permission": r.Permission,
			"username":   r.Username,
			"secret":     r.Secret,
			"database":   r.Database,
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

	r.Cluster.Name, err = k8s_generic.GetProperty[string](obj, "spec", "cluster", "name")
	if err != nil {
		return r, fmt.Errorf("failed to get cluster ref name: %+v", err)
	}

	r.Cluster.Namespace, err = k8s_generic.GetProperty[string](obj, "spec", "cluster", "namespace")
	if err != nil {
		return r, fmt.Errorf("failed to get cluster ref namespace: %+v", err)
	}

	r.Permission, err = k8s_generic.GetProperty[string](obj, "spec", "permission")
	if err != nil {
		return r, fmt.Errorf("failed to get permission: %+v", err)
	}

	r.Database, err = k8s_generic.GetProperty[string](obj, "spec", "database")
	if err != nil {
		return r, fmt.Errorf("failed to get database: %+v", err)
	}

	r.Username, err = k8s_generic.GetProperty[string](obj, "spec", "username")
	if err != nil {
		return r, fmt.Errorf("failed to get username: %+v", err)
	}

	r.Secret, err = k8s_generic.GetProperty[string](obj, "spec", "secret")
	if err != nil {
		return r, fmt.Errorf("failed to get secret: %+v", err)
	}

	return r, nil
}

func (r Resource) GetID() string {
	return r.Name + "@" + r.Namespace
}

func (r Resource) GetClusterID() string {
	return r.Cluster.Name + "@" + r.Cluster.Namespace
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

func (r Resource) GetTarget() string {
	return r.Cluster.Name
}

func (r Resource) GetTargetNamespace() string {
	return r.Cluster.Namespace
}

func (r Resource) Equal(obj k8s_generic.Resource) bool {
	other, ok := obj.(Resource)
	if !ok {
		return false
	}

	if r.Comparable != other.Comparable {
		return false
	}

	return true
}
