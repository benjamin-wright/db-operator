package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type CockroachClientComparable struct {
	Name      string
	Namespace string
	DBRef     DBRef
	Database  string
	Username  string
	Secret    string
}

type CockroachClient struct {
	CockroachClientComparable
	UID             string
	ResourceVersion string
}

func (cli *CockroachClient) ToUnstructured() *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "CockroachClient",
		"metadata": map[string]interface{}{
			"name":      cli.Name,
			"namespace": cli.Namespace,
		},
		"spec": map[string]interface{}{
			"dbRef": map[string]interface{}{
				"name":      cli.DBRef.Name,
				"namespace": cli.DBRef.Namespace,
			},
			"database": cli.Database,
			"username": cli.Username,
			"secret":   cli.Secret,
		},
	})

	return result
}

func (cli *CockroachClient) FromUnstructured(obj *unstructured.Unstructured) error {
	var err error
	cli.Name = obj.GetName()
	cli.Namespace = obj.GetNamespace()
	cli.UID = string(obj.GetUID())
	cli.ResourceVersion = obj.GetResourceVersion()

	cli.DBRef.Name, err = k8s_generic.GetProperty[string](obj, "spec", "dbRef", "name")
	if err != nil {
		return fmt.Errorf("failed to get db ref name: %+v", err)
	}

	cli.DBRef.Namespace, err = k8s_generic.GetProperty[string](obj, "spec", "dbRef", "namespace")
	if err != nil {
		return fmt.Errorf("failed to get db ref namespace: %+v", err)
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

func (cli *CockroachClient) GetNamespace() string {
	return cli.Namespace
}

func (cli *CockroachClient) GetUID() string {
	return cli.UID
}

func (cli *CockroachClient) GetResourceVersion() string {
	return cli.ResourceVersion
}

func (cli *CockroachClient) GetTarget() string {
	return cli.DBRef.Name
}

func (cli *CockroachClient) GetTargetNamespace() string {
	return cli.DBRef.Namespace
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
