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
		Resource: "natsclients",
	},
	Kind:             "NatsClient",
	FromUnstructured: fromUnstructured,
}

type Cluster struct {
	Name      string
	Namespace string
}

type Comparable struct {
	Name      string
	Namespace string
	Cluster   Cluster
	Secret    string
	Ready     bool
}

type Resource struct {
	Comparable
	UID             string
	ResourceVersion string
}

func (cli Resource) ToUnstructured() *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "NatsClient",
		"metadata": map[string]interface{}{
			"name":            cli.Name,
			"namespace":       cli.Namespace,
			"resourceVersion": cli.ResourceVersion,
			"uid":             cli.UID,
		},
		"spec": map[string]interface{}{
			"cluster": map[string]interface{}{
				"name":      cli.Cluster.Name,
				"namespace": cli.Cluster.Namespace,
			},
			"secret": cli.Secret,
		},
		"status": map[string]interface{}{
			"ready": cli.Ready,
		},
	})

	return result
}

func fromUnstructured(obj *unstructured.Unstructured) (Resource, error) {
	var err error
	cli := Resource{}

	cli.Name = obj.GetName()
	cli.Namespace = obj.GetNamespace()
	cli.UID = string(obj.GetUID())
	cli.ResourceVersion = obj.GetResourceVersion()

	cli.Cluster.Name, err = k8s_generic.GetProperty[string](obj, "spec", "cluster", "name")
	if err != nil {
		return cli, fmt.Errorf("failed to get cluster name: %+v", err)
	}

	cli.Cluster.Namespace, err = k8s_generic.GetProperty[string](obj, "spec", "cluster", "namespace")
	if err != nil {
		return cli, fmt.Errorf("failed to get cluster namespace: %+v", err)
	}

	cli.Secret, err = k8s_generic.GetProperty[string](obj, "spec", "secret")
	if err != nil {
		return cli, fmt.Errorf("failed to get secret: %+v", err)
	}

	cli.Ready, _, err = unstructured.NestedBool(obj.Object, "status", "ready")
	if err != nil {
		return cli, fmt.Errorf("failed to get ready: %+v", err)
	}

	return cli, nil
}

func (cli Resource) GetID() string {
	return cli.Name + "@" + cli.Namespace
}

func (cli Resource) GetClusterID() string {
	return cli.Cluster.Name + "@" + cli.Cluster.Namespace
}

func (cli Resource) GetName() string {
	return cli.Name
}

func (cli Resource) GetNamespace() string {
	return cli.Namespace
}

func (cli Resource) GetUID() string {
	return cli.UID
}

func (cli Resource) GetResourceVersion() string {
	return cli.ResourceVersion
}

func (cli Resource) GetTargetID() string {
	return cli.Cluster.Name + "@" + cli.Cluster.Namespace
}

func (cli Resource) Equal(obj k8s_generic.Resource) bool {
	if other, ok := obj.(Resource); ok {
		return cli.Comparable == other.Comparable
	}
	return false
}
