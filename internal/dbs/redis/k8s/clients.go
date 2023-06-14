package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type RedisClientComparable struct {
	Name       string
	Deployment string
	Unit       int64
	Secret     string
}

type RedisClient struct {
	RedisClientComparable
	UID             string
	ResourceVersion string
}

func (cli *RedisClient) ToUnstructured(namespace string) *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "RedisClient",
		"metadata": map[string]interface{}{
			"name": cli.Name,
		},
		"spec": map[string]interface{}{
			"deployment": cli.Deployment,
			"unit":       cli.Unit,
			"secret":     cli.Secret,
		},
	})

	return result
}

func (cli *RedisClient) FromUnstructured(obj *unstructured.Unstructured) error {
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

func (cli *RedisClient) GetUID() string {
	return cli.UID
}

func (cli *RedisClient) GetResourceVersion() string {
	return cli.ResourceVersion
}

func (cli *RedisClient) GetTarget() string {
	return cli.Deployment
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