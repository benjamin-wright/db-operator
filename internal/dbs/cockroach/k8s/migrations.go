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

func (cm *CockroachMigration) ToUnstructured() *unstructured.Unstructured {
	result := &unstructured.Unstructured{}
	result.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "ponglehub.co.uk/v1alpha1",
		"kind":       "CockroachMigration",
		"metadata": map[string]interface{}{
			"name":      cm.Name,
			"namespace": cm.Namespace,
		},
		"spec": map[string]interface{}{
			"dbRef": map[string]interface{}{
				"name":      cm.DBRef.Name,
				"namespace": cm.DBRef.Namespace,
			},
			"database":  cm.Database,
			"migration": cm.Migration,
			"index":     cm.Index,
		},
	})

	return result
}

func (m *CockroachMigration) FromUnstructured(obj *unstructured.Unstructured) error {
	var err error

	m.Name = obj.GetName()
	m.Namespace = obj.GetNamespace()
	m.UID = string(obj.GetUID())
	m.ResourceVersion = obj.GetResourceVersion()

	m.DBRef.Name, err = k8s_generic.GetProperty[string](obj, "spec", "dbRef", "name")
	if err != nil {
		return fmt.Errorf("failed to get db ref name: %+v", err)
	}

	m.DBRef.Namespace, err = k8s_generic.GetProperty[string](obj, "spec", "dbRef", "namespace")
	if err != nil {
		return fmt.Errorf("failed to get db ref namespace: %+v", err)
	}

	m.Database, err = k8s_generic.GetProperty[string](obj, "spec", "database")
	if err != nil {
		return fmt.Errorf("failed to get database: %+v", err)
	}

	m.Migration, err = k8s_generic.GetProperty[string](obj, "spec", "migration")
	if err != nil {
		return fmt.Errorf("failed to get migration: %+v", err)
	}

	m.Index, err = k8s_generic.GetProperty[int64](obj, "spec", "index")
	if err != nil {
		return fmt.Errorf("failed to get index: %+v", err)
	}

	return nil
}

func (m *CockroachMigration) GetName() string {
	return m.Name
}

func (m *CockroachMigration) GetNamespace() string {
	return m.Namespace
}

func (m *CockroachMigration) GetUID() string {
	return m.UID
}

func (m *CockroachMigration) GetResourceVersion() string {
	return m.ResourceVersion
}

func (m *CockroachMigration) Equal(obj CockroachMigration) bool {
	return m.CockroachMigrationComparable == obj.CockroachMigrationComparable
}

func (c *Client) Migrations() *k8s_generic.Client[CockroachMigration, *CockroachMigration] {
	return k8s_generic.NewClient[CockroachMigration](
		c.builder,
		schema.GroupVersionResource{
			Group:    "ponglehub.co.uk",
			Version:  "v1alpha1",
			Resource: "cockroachmigrations",
		},
		"CockroachMigration",
		nil,
	)
}
