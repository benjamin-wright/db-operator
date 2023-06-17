package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type RedisClientComparable struct {
	Name      string
	Namespace string
	DBRef     DBRef
	Unit      int64
	Secret    string
}

type RedisClient struct {
	RedisClientComparable
	UID             string
	ResourceVersion string
}

func (cli *RedisClient) ToUnstructured() *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "RedisClient",
		"metadata": map[string]interface{}{
			"name":      cli.Name,
			"namespace": cli.Namespace,
		},
		"spec": map[string]interface{}{
			"dbRef": map[string]interface{}{
				"name":      cli.DBRef.Name,
				"namespace": cli.DBRef.Namespace,
			},
			"unit":   cli.Unit,
			"secret": cli.Secret,
		},
	})

	return result
}

func (cli *RedisClient) FromUnstructured(obj *unstructured.Unstructured) error {
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

	cli.Unit, err = k8s_generic.GetProperty[int64](obj, "spec", "unit")
	if err != nil {
		return fmt.Errorf("failed to get unit: %+v", err)
	}

	cli.Secret, err = k8s_generic.GetProperty[string](obj, "spec", "secret")
	if err != nil {
		return fmt.Errorf("failed to get secret: %+v", err)
	}

	return nil
}

func (cli *RedisClient) GetName() string {
	return cli.Name
}

func (cli *RedisClient) GetNamespace() string {
	return cli.Namespace
}

func (cli *RedisClient) GetUID() string {
	return cli.UID
}

func (cli *RedisClient) GetResourceVersion() string {
	return cli.ResourceVersion
}

func (cli *RedisClient) GetTarget() string {
	return cli.DBRef.Name
}

func (cli *RedisClient) GetTargetNamespace() string {
	return cli.DBRef.Namespace
}

func (cli *RedisClient) Equal(obj RedisClient) bool {
	return cli.RedisClientComparable == obj.RedisClientComparable
}

func (c *Client) Clients() *k8s_generic.Client[RedisClient, *RedisClient] {
	return k8s_generic.NewClient[RedisClient](
		c.builder,
		schema.GroupVersionResource{
			Group:    "ponglehub.co.uk",
			Version:  "v1alpha1",
			Resource: "redisclients",
		},
		"RedisClient",
		nil,
	)
}
