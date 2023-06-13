package k8s

import (
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type NatsDBComparable struct {
	Name string
}

type NatsDB struct {
	NatsDBComparable
	UID             string
	ResourceVersion string
}

func (db *NatsDB) ToUnstructured(namespace string) *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "NatsDB",
		"metadata": map[string]interface{}{
			"name": db.Name,
		},
	})

	return result
}

func (db *NatsDB) FromUnstructured(obj *unstructured.Unstructured) error {
	db.Name = obj.GetName()
	db.UID = string(obj.GetUID())
	db.ResourceVersion = obj.GetResourceVersion()

	return nil
}

func (db *NatsDB) GetName() string {
	return db.Name
}

func (db *NatsDB) GetUID() string {
	return db.UID
}

func (db *NatsDB) GetResourceVersion() string {
	return db.ResourceVersion
}

func (db *NatsDB) Equal(obj NatsDB) bool {
	return db.NatsDBComparable == obj.NatsDBComparable
}

func (c *Client) DBs() *k8s_generic.Client[NatsDB, *NatsDB] {
	return k8s_generic.NewClient[NatsDB](
		c.builder,
		schema.GroupVersionResource{
			Group:    "ponglehub.co.uk",
			Version:  "v1alpha1",
			Resource: "natsdbs",
		},
		"NatsDB",
		nil,
	)
}
