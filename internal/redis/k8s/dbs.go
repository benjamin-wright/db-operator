package k8s

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
)

type RedisDBComparable struct {
	Name    string
	Storage string
}

type RedisDB struct {
	RedisDBComparable
	UID             string
	ResourceVersion string
}

func (db *RedisDB) ToUnstructured(namespace string) *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "RedisDB",
		"metadata": map[string]interface{}{
			"name": db.Name,
		},
		"spec": map[string]interface{}{
			"storage": db.Storage,
		},
	})

	return result
}

func (db *RedisDB) FromUnstructured(obj *unstructured.Unstructured) error {
	var err error

	db.Name = obj.GetName()
	db.UID = string(obj.GetUID())
	db.ResourceVersion = obj.GetResourceVersion()
	db.Storage, err = k8s_generic.GetProperty[string](obj, "spec", "storage")
	if err != nil {
		return fmt.Errorf("failed to get storage: %+v", err)
	}

	return nil
}

func (db *RedisDB) GetName() string {
	return db.Name
}

func (db *RedisDB) GetStorage() string {
	return db.Storage
}

func (db *RedisDB) GetUID() string {
	return db.UID
}

func (db *RedisDB) GetResourceVersion() string {
	return db.ResourceVersion
}

func (db *RedisDB) Equal(obj RedisDB) bool {
	return db.RedisDBComparable == obj.RedisDBComparable
}

func (c *Client) DBs() *k8s_generic.Client[RedisDB, *RedisDB] {
	return k8s_generic.NewClient[RedisDB](
		c.builder,
		schema.GroupVersionResource{
			Group:    "ponglehub.co.uk",
			Version:  "v1alpha1",
			Resource: "redisdbs",
		},
		"RedisDB",
		nil,
	)
}
