package k8s

import (
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type NatsDBComparable struct {
	Name      string
	Namespace string
}

type NatsDB struct {
	NatsDBComparable
	UID             string
	ResourceVersion string
}

func (db NatsDB) ToUnstructured() *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "NatsDB",
		"metadata": map[string]interface{}{
			"name":      db.Name,
			"namespace": db.Namespace,
		},
	})

	return result
}

func natsDBFromUnstructured(obj *unstructured.Unstructured) (NatsDB, error) {
	db := NatsDB{}

	db.Name = obj.GetName()
	db.Namespace = obj.GetNamespace()
	db.UID = string(obj.GetUID())
	db.ResourceVersion = obj.GetResourceVersion()

	return db, nil
}

func (db NatsDB) GetName() string {
	return db.Name
}

func (db NatsDB) GetNamespace() string {
	return db.Namespace
}

func (db NatsDB) GetUID() string {
	return db.UID
}

func (db NatsDB) GetResourceVersion() string {
	return db.ResourceVersion
}

func (db NatsDB) Equal(obj k8s_generic.Resource) bool {
	natsDB, ok := obj.(*NatsDB)
	if !ok {
		return false
	}
	return db.NatsDBComparable == natsDB.NatsDBComparable
}

func (c *Client) DBs() *k8s_generic.Client[NatsDB] {
	return k8s_generic.NewClient(
		c.builder,
		schema.GroupVersionResource{
			Group:    "ponglehub.co.uk",
			Version:  "v1alpha1",
			Resource: "natsdbs",
		},
		"NatsDB",
		nil,
		natsDBFromUnstructured,
	)
}
