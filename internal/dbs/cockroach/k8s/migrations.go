package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type CockroachMigrationComparable struct {
	Name      string
	Namespace string
	DBRef     DBRef
	Database  string
	Migration string
	Index     int64
}

type CockroachMigration struct {
	CockroachMigrationComparable
	UID             string
	ResourceVersion string
}

func (m CockroachMigration) ToUnstructured() *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "CockroachMigration",
		"metadata": map[string]interface{}{
			"name":      m.Name,
			"namespace": m.Namespace,
		},
		"spec": map[string]interface{}{
			"dbRef": map[string]interface{}{
				"name":      m.DBRef.Name,
				"namespace": m.DBRef.Namespace,
			},
			"database":  m.Database,
			"migration": m.Migration,
			"index":     m.Index,
		},
	})

	return result
}

func cockroachMigrationFromUnstructured(obj *unstructured.Unstructured) (CockroachMigration, error) {
	var err error
	m := CockroachMigration{}

	m.Name = obj.GetName()
	m.Namespace = obj.GetNamespace()
	m.UID = string(obj.GetUID())
	m.ResourceVersion = obj.GetResourceVersion()

	m.DBRef.Name, err = k8s_generic.GetProperty[string](obj, "spec", "dbRef", "name")
	if err != nil {
		return m, fmt.Errorf("failed to get db ref name: %+v", err)
	}

	m.DBRef.Namespace, err = k8s_generic.GetProperty[string](obj, "spec", "dbRef", "namespace")
	if err != nil {
		return m, fmt.Errorf("failed to get db ref namespace: %+v", err)
	}

	m.Database, err = k8s_generic.GetProperty[string](obj, "spec", "database")
	if err != nil {
		return m, fmt.Errorf("failed to get database: %+v", err)
	}

	m.Migration, err = k8s_generic.GetProperty[string](obj, "spec", "migration")
	if err != nil {
		return m, fmt.Errorf("failed to get migration: %+v", err)
	}

	m.Index, err = k8s_generic.GetProperty[int64](obj, "spec", "index")
	if err != nil {
		return m, fmt.Errorf("failed to get index: %+v", err)
	}

	return m, nil
}

func (m CockroachMigration) GetName() string {
	return m.Name
}

func (m CockroachMigration) GetNamespace() string {
	return m.Namespace
}

func (m CockroachMigration) GetUID() string {
	return m.UID
}

func (m CockroachMigration) GetResourceVersion() string {
	return m.ResourceVersion
}

func (m CockroachMigration) Equal(obj k8s_generic.Resource) bool {
	if other, ok := obj.(*CockroachMigration); ok {
		return m.CockroachMigrationComparable == other.CockroachMigrationComparable
	}

	return false
}

func (c *Client) Migrations() *k8s_generic.Client[CockroachMigration] {
	return k8s_generic.NewClient(
		c.builder,
		schema.GroupVersionResource{
			Group:    "ponglehub.co.uk",
			Version:  "v1alpha1",
			Resource: "cockroachmigrations",
		},
		"CockroachMigration",
		nil,
		cockroachMigrationFromUnstructured,
	)
}
