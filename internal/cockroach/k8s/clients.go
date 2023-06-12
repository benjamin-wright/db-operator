package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type CockroachClientComparable struct {
	Name       string
	Deployment string
	Database   string
	Username   string
	Secret     string
}

type CockroachClient struct {
	CockroachClientComparable
	UID             string
	ResourceVersion string
}

func (cli *CockroachClient) ToUnstructured(namespace string) *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "CockroachClient",
		"metadata": map[string]interface{}{
			"name": cli.Name,
		},
		"spec": map[string]interface{}{
			"deployment": cli.Deployment,
			"database":   cli.Database,
			"username":   cli.Username,
			"secret":     cli.Secret,
		},
	})

	return result
}

func (cli *CockroachClient) FromUnstructured(obj *unstructured.Unstructured) error {
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

	cli.Database, err = k8s_generic.GetProperty[string](obj, "spec", "database")
	if err != nil {
		return fmt.Errorf("failed to get database: %+v", err)
	}

	cli.Username, err = k8s_generic.GetProperty[string](obj, "spec", "username")
	if err != nil {
		return fmt.Errorf("failed to get username: %+v", err)
	}

	cli.Secret, err = k8s_generic.GetProperty[string](obj, "spec", "secret")
	if err != nil {
		return fmt.Errorf("failed to get secret: %+v", err)
	}

	return nil
}

func (cli *CockroachClient) GetName() string {
	return cli.Name
}

func (cli *CockroachClient) GetUID() string {
	return cli.UID
}

func (cli *CockroachClient) GetResourceVersion() string {
	return cli.ResourceVersion
}

func (cli *CockroachClient) GetTarget() string {
	return cli.Deployment
}

func (cli *CockroachClient) Equal(obj CockroachClient) bool {
	return cli.CockroachClientComparable == obj.CockroachClientComparable
}

func (c *Client) Clients() *k8s_generic.Client[CockroachClient, *CockroachClient] {
	return k8s_generic.NewClient[CockroachClient](
		c.builder,
		schema.GroupVersionResource{
			Group:    "ponglehub.co.uk",
			Version:  "v1alpha1",
			Resource: "cockroachclients",
		},
		"CockroachClient",
		nil,
	)
}
