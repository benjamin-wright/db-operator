package resources

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
)

type CockroachPVCComparable struct {
	Name     string
	Database string
	Storage  string
}

type CockroachPVC struct {
	CockroachPVCComparable
	UID             string
	ResourceVersion string
}

func (p *CockroachPVC) ToUnstructured(namespace string) *unstructured.Unstructured {
	pvc := &unstructured.Unstructured{
		Object: map[string]interface{}{},
	}

	return pvc
}

func (p *CockroachPVC) FromUnstructured(obj *unstructured.Unstructured) error {
	var err error

	p.Name = obj.GetName()
	p.UID = string(obj.GetUID())
	p.ResourceVersion = obj.GetResourceVersion()

	p.Storage, err = k8s_generic.GetProperty[string](obj, "spec", "resources", "requests", "storage")
	if err != nil {
		return fmt.Errorf("failed to get storage: %+v", err)
	}

	p.Database, err = k8s_generic.GetProperty[string](obj, "metadata", "labels", "app")
	if err != nil {
		return fmt.Errorf("failed to get database from app label: %+v", err)
	}

	return nil
}

func (p *CockroachPVC) GetName() string {
	return p.Name
}

func (p *CockroachPVC) GetUID() string {
	return p.UID
}

func (p *CockroachPVC) GetResourceVersion() string {
	return p.ResourceVersion
}

func (p *CockroachPVC) Equal(obj CockroachPVC) bool {
	return p.CockroachPVCComparable == obj.CockroachPVCComparable
}

func NewCockroachPVCClient(namespace string) (*k8s_generic.Client[CockroachPVC, *CockroachPVC], error) {
	return k8s_generic.New[CockroachPVC](
		schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "persistentvolumeclaims",
		},
		"PersistentVolumeClaim",
		namespace,
		k8s_generic.Merge(map[string]interface{}{
			"ponglehub.co.uk/resource-type": "cockroachdb",
		}, LABEL_FILTERS),
	)
}
