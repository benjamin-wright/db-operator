package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type NatsClientComparable struct {
	Name       string
	Deployment string
	Secret     string
}

type NatsClient struct {
	NatsClientComparable
	UID             string
	ResourceVersion string
}

func (cli *NatsClient) ToUnstructured(namespace string) *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "NatsClient",
		"metadata": map[string]interface{}{
			"name": cli.Name,
		},
		"spec": map[string]interface{}{
			"deployment": cli.Deployment,
			"secret":     cli.Secret,
		},
	})

	return result
}

func (cli *NatsClient) FromUnstructured(obj *unstructured.Unstructured) error {
	var err error
	cli.Name = obj.GetName()
	cli.UID = string(obj.GetUID())
	cli.ResourceVersion = obj.GetResourceVersion()

	cli.Deployment, err = k8s_generic.GetProperty[string](obj, "spec", "deployment")
	if err != nil {
		return fmt.Errorf("failed to get deployment: %+v", err)
	}

	cli.Deployment, err = k8s_generic.GetProperty[string](obj, "spec", "deployment")
	if err != nil {
		return fmt.Errorf("failed to get deployment: %+v", err)
	}

	cli.Secret, err = k8s_generic.GetProperty[string](obj, "spec", "secret")
	if err != nil {
		return fmt.Errorf("failed to get secret: %+v", err)
	}

	return nil
}

func (cli *NatsClient) GetName() string {
	return cli.Name
}

func (cli *NatsClient) GetUID() string {
	return cli.UID
}

func (cli *NatsClient) GetResourceVersion() string {
	return cli.ResourceVersion
}

func (cli *NatsClient) GetTarget() string {
	return cli.Deployment
}

func (cli *NatsClient) Equal(obj NatsClient) bool {
	return cli.NatsClientComparable == obj.NatsClientComparable
}

func (c *Client) Clients() *k8s_generic.Client[NatsClient, *NatsClient] {
	return k8s_generic.NewClient[NatsClient](
		c.builder,
		schema.GroupVersionResource{
			Group:    "ponglehub.co.uk",
			Version:  "v1alpha1",
			Resource: "natsclients",
		},
		"NatsClient",
		nil,
	)
}
