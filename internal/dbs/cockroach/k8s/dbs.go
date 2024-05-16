package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type CockroachDBComparable struct {
	Name      string
	Namespace string
	Storage   string
}

type CockroachDB struct {
	CockroachDBComparable
	UID             string
	ResourceVersion string
}

func (db CockroachDB) ToUnstructured() *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "CockroachDB",
		"metadata": map[string]interface{}{
			"name":      db.Name,
			"namespace": db.Namespace,
		},
		"spec": map[string]interface{}{
			"storage": db.Storage,
		},
	})

	return result
}

func cockroachDBFromUnstructured(obj *unstructured.Unstructured) (CockroachDB, error) {
	var err error
	db := CockroachDB{}

	db.Name = obj.GetName()
	db.Namespace = obj.GetNamespace()
	db.UID = string(obj.GetUID())
	db.ResourceVersion = obj.GetResourceVersion()
	db.Storage, err = k8s_generic.GetProperty[string](obj, "spec", "storage")
	if err != nil {
		return db, fmt.Errorf("failed to get storage: %+v", err)
	}

	return db, nil
}

func (db CockroachDB) GetName() string {
	return db.Name
}

func (db CockroachDB) GetNamespace() string {
	return db.Namespace
}

func (db CockroachDB) GetStorage() string {
	return db.Storage
}

func (db CockroachDB) GetUID() string {
	return db.UID
}

func (db CockroachDB) GetResourceVersion() string {
	return db.ResourceVersion
}

func (db CockroachDB) Equal(obj k8s_generic.Resource) bool {
	if other, ok := obj.(CockroachDB); ok {
		return db.CockroachDBComparable == other.CockroachDBComparable
	}

	return false
}

func (c *Client) DBs() *k8s_generic.Client[CockroachDB] {
	return k8s_generic.NewClient(
		c.builder,
		schema.GroupVersionResource{
			Group:    "ponglehub.co.uk",
			Version:  "v1alpha1",
			Resource: "cockroachdbs",
		},
		"CockroachDB",
		nil,
		cockroachDBFromUnstructured,
	)
}
