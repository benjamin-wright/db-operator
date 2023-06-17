package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type CockroachPVCComparable struct {
	Name      string
	Namespace string
	Database  string
	Storage   string
}

type CockroachPVC struct {
	CockroachPVCComparable
	UID             string
	ResourceVersion string
}

func (p *CockroachPVC) ToUnstructured() *unstructured.Unstructured {
	panic("not implemented")
}

func (p *CockroachPVC) FromUnstructured(obj *unstructured.Unstructured) error {
	var err error

	p.Name = obj.GetName()
	p.Namespace = obj.GetNamespace()
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

func (p *CockroachPVC) GetNamespace() string {
	return p.Namespace
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

func (c *Client) PVCs() *k8s_generic.Client[CockroachPVC, *CockroachPVC] {
	return k8s_generic.NewClient[CockroachPVC](
		c.builder,
		schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "persistentvolumeclaims",
		},
		"PersistentVolumeClaim",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "cockroachdb",
		}, common.LABEL_FILTERS),
	)
}
