package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type NatsClientComparable struct {
	Name      string
	Namespace string
	DBRef     DBRef
	Secret    string
}

type NatsClient struct {
	NatsClientComparable
	UID             string
	ResourceVersion string
}

func (cli NatsClient) ToUnstructured() *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "NatsClient",
		"metadata": map[string]interface{}{
			"name":      cli.Name,
			"namespace": cli.Namespace,
		},
		"spec": map[string]interface{}{
			"dbRef": map[string]interface{}{
				"name":      cli.DBRef.Name,
				"namespace": cli.DBRef.Namespace,
			},
			"secret": cli.Secret,
		},
	})

	return result
}

func natsClientFromUnstructured(obj *unstructured.Unstructured) (NatsClient, error) {
	var err error
	cli := NatsClient{}

	cli.Name = obj.GetName()
	cli.Namespace = obj.GetNamespace()
	cli.UID = string(obj.GetUID())
	cli.ResourceVersion = obj.GetResourceVersion()

	cli.DBRef.Name, err = k8s_generic.GetProperty[string](obj, "spec", "dbRef", "name")
	if err != nil {
		return cli, fmt.Errorf("failed to get db ref name: %+v", err)
	}

	cli.DBRef.Namespace, err = k8s_generic.GetProperty[string](obj, "spec", "dbRef", "namespace")
	if err != nil {
		return cli, fmt.Errorf("failed to get db ref namespace: %+v", err)
	}

	cli.Secret, err = k8s_generic.GetProperty[string](obj, "spec", "secret")
	if err != nil {
		return cli, fmt.Errorf("failed to get secret: %+v", err)
	}

	return cli, nil
}

func (cli NatsClient) GetName() string {
	return cli.Name
}

func (cli NatsClient) GetNamespace() string {
	return cli.Namespace
}

func (cli NatsClient) GetUID() string {
	return cli.UID
}

func (cli NatsClient) GetResourceVersion() string {
	return cli.ResourceVersion
}

func (cli NatsClient) GetTarget() string {
	return cli.DBRef.Name
}

func (cli NatsClient) GetTargetNamespace() string {
	return cli.DBRef.Namespace
}

func (cli NatsClient) Equal(obj k8s_generic.Resource) bool {
	if other, ok := obj.(NatsClient); ok {
		return cli.NatsClientComparable == other.NatsClientComparable
	}
	return false
}

func (c *Client) Clients() *k8s_generic.Client[NatsClient] {
	return k8s_generic.NewClient(
		c.builder,
		schema.GroupVersionResource{
			Group:    "ponglehub.co.uk",
			Version:  "v1alpha1",
			Resource: "natsclients",
		},
		"NatsClient",
		nil,
		natsClientFromUnstructured,
	)
}
