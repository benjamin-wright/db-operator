package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type RedisPVCComparable struct {
	Name      string
	Namespace string
	Database  string
	Storage   string
}

type RedisPVC struct {
	RedisPVCComparable
	UID             string
	ResourceVersion string
}

func (p *RedisPVC) ToUnstructured() *unstructured.Unstructured {
	panic("not implemented")
}

func (p *RedisPVC) FromUnstructured(obj *unstructured.Unstructured) error {
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

func (p *RedisPVC) GetName() string {
	return p.Name
}

func (p *RedisPVC) GetNamespace() string {
	return p.Namespace
}

func (p *RedisPVC) GetUID() string {
	return p.UID
}

func (p *RedisPVC) GetResourceVersion() string {
	return p.ResourceVersion
}

func (p *RedisPVC) Equal(obj RedisPVC) bool {
	return p.RedisPVCComparable == obj.RedisPVCComparable
}

func (c *Client) PVCs() *k8s_generic.Client[RedisPVC, *RedisPVC] {
	return k8s_generic.NewClient[RedisPVC](
		c.builder,
		schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "persistentvolumeclaims",
		},
		"PersistentVolumeClaim",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "redis",
		}, common.LABEL_FILTERS),
	)
}
