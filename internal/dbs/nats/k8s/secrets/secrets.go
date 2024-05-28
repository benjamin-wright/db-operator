package secrets

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/benjamin-wright/db-operator/v2/internal/common"
	"github.com/benjamin-wright/db-operator/v2/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var ClientArgs = k8s_generic.ClientArgs[Resource]{
	Schema: schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	},
	Kind: "Secret",
	LabelFilters: k8s_generic.Merge(map[string]string{
		"ponglehub.co.uk/resource-type": "nats",
	}, common.LABEL_FILTERS),
	FromUnstructured: fromUnstructured,
}

type Cluster struct {
	Name      string
	Namespace string
}

type Comparable struct {
	Name      string
	Namespace string
	Cluster   Cluster
}

type Resource struct {
	Comparable
	UID             string
	ResourceVersion string
}

func (r Resource) GetHost() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", r.Cluster.Name, r.Cluster.Namespace)
}

func encode(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}

func (r Resource) ToUnstructured() *unstructured.Unstructured {
	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      r.Name,
				"namespace": r.Namespace,
				"labels": k8s_generic.Merge(map[string]string{
					"app":                           r.Name,
					"ponglehub.co.uk/resource-type": "nats",
				}, common.LABEL_FILTERS),
			},
			"data": map[string]interface{}{
				"NATS_HOST": encode(r.GetHost()),
				"NATS_PORT": encode("4222"),
			},
		},
	}

	return secret
}

func fromUnstructured(obj *unstructured.Unstructured) (Resource, error) {
	r := Resource{}

	r.Name = obj.GetName()
	r.Namespace = obj.GetNamespace()

	r.UID = string(obj.GetUID())
	r.ResourceVersion = obj.GetResourceVersion()

	hostname, err := k8s_generic.GetEncodedProperty(obj, "data", "NATS_HOST")
	if err != nil {
		return r, fmt.Errorf("failed to get NATS_HOST: %+v", err)
	}
	r.Cluster.Name = strings.Split(hostname, ".")[0]
	r.Cluster.Namespace = strings.Split(hostname, ".")[1]

	return r, nil
}

func (r Resource) GetName() string {
	return r.Name
}

func (r Resource) GetNamespace() string {
	return r.Namespace
}

func (r Resource) GetUID() string {
	return r.UID
}

func (r Resource) GetResourceVersion() string {
	return r.ResourceVersion
}

func (r Resource) Equal(obj k8s_generic.Resource) bool {
	other, ok := obj.(Resource)
	if !ok {
		return false
	}
	return r.Comparable == other.Comparable
}
