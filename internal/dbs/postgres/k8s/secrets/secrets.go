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
		"ponglehub.co.uk/resource-type": "postgrescluster",
	}, common.LABEL_FILTERS),
	FromUnstructured: fromUnstructured,
}

type Cluster struct {
	Name      string
	Namespace string
}

func (c Cluster) GetNamespace() string {
	return c.Namespace + ":" + c.Name
}

type Comparable struct {
	Name      string
	Namespace string
	Cluster   Cluster
	Database  string
	User      string
	Password  string
}

type Resource struct {
	Comparable
	UID             string
	ResourceVersion string
}

func encode(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}

func (r Resource) GetHost() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", r.Cluster.Name, r.Cluster.Namespace)
}

func (r Resource) GetPort() string {
	return "26257"
}

func (r Resource) ToUnstructured() *unstructured.Unstructured {
	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name": r.Name,
				"labels": k8s_generic.Merge(map[string]string{
					"app":                           r.Name,
					"ponglehub.co.uk/resource-type": "postgrescluster",
				}, common.LABEL_FILTERS),
				"namespace": r.Namespace,
			},
			"data": map[string]interface{}{
				"POSTGRES_HOST":     encode(r.GetHost()),
				"POSTGRES_PORT":     encode(r.GetPort()),
				"POSTGRES_USER":     encode(r.User),
				"POSTGRES_PASSWORD": encode(r.Password),
				"POSTGRES_NAME":     encode(r.Database),
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

	hostname, err := k8s_generic.GetEncodedProperty(obj, "data", "POSTGRES_HOST")
	if err != nil {
		return r, fmt.Errorf("failed to get POSTGRES_HOST: %+v", err)
	}
	r.Cluster.Name = strings.Split(hostname, ".")[0]
	r.Cluster.Namespace = strings.Split(hostname, ".")[1]

	r.User, err = k8s_generic.GetEncodedProperty(obj, "data", "POSTGRES_USER")
	if err != nil {
		return r, fmt.Errorf("failed to get POSTGRES_USER: %+v", err)
	}

	r.Password, err = k8s_generic.GetEncodedProperty(obj, "data", "POSTGRES_PASSWORD")
	if err != nil {
		return r, fmt.Errorf("failed to get POSTGRES_PASSWORD: %+v", err)
	}

	r.Database, err = k8s_generic.GetEncodedProperty(obj, "data", "POSTGRES_NAME")
	if err != nil {
		return r, fmt.Errorf("failed to get POSTGRES_NAME: %+v", err)
	}

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
	if other, ok := obj.(*Resource); ok {
		return r.Comparable == other.Comparable
	}

	return false
}
