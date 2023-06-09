package k8s

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type CockroachSecretComparable struct {
	Name      string
	Namespace string
	DB        DBRef
	Database  string
	User      string
}

type CockroachSecret struct {
	CockroachSecretComparable
	UID             string
	ResourceVersion string
}

func encode(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}

func (s *CockroachSecret) GetHost() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", s.DB.Name, s.DB.Namespace)
}

func (s *CockroachSecret) GetPort() string {
	return "26257"
}

func (s *CockroachSecret) ToUnstructured() *unstructured.Unstructured {
	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name": s.Name,
				"labels": k8s_generic.Merge(map[string]string{
					"app":                           s.Name,
					"ponglehub.co.uk/resource-type": "cockroachdb",
				}, common.LABEL_FILTERS),
				"namespace": s.Namespace,
			},
			"data": map[string]interface{}{
				"POSTGRES_HOST": encode(s.GetHost()),
				"POSTGRES_PORT": encode(s.GetPort()),
				"POSTGRES_USER": encode(s.User),
				"POSTGRES_NAME": encode(s.Database),
			},
		},
	}

	return secret
}

func (s *CockroachSecret) FromUnstructured(obj *unstructured.Unstructured) error {
	s.Name = obj.GetName()
	s.Namespace = obj.GetNamespace()

	s.UID = string(obj.GetUID())
	s.ResourceVersion = obj.GetResourceVersion()

	hostname, err := k8s_generic.GetEncodedProperty(obj, "data", "POSTGRES_HOST")
	if err != nil {
		return fmt.Errorf("failed to get POSTGRES_HOST: %+v", err)
	}
	s.DB.Name = strings.Split(hostname, ".")[0]
	s.DB.Namespace = strings.Split(hostname, ".")[1]

	s.User, err = k8s_generic.GetEncodedProperty(obj, "data", "POSTGRES_USER")
	if err != nil {
		return fmt.Errorf("failed to get POSTGRES_USER: %+v", err)
	}

	s.Database, err = k8s_generic.GetEncodedProperty(obj, "data", "POSTGRES_NAME")
	if err != nil {
		return fmt.Errorf("failed to get POSTGRES_NAME: %+v", err)
	}

	return nil
}

func (s *CockroachSecret) GetName() string {
	return s.Name
}

func (s *CockroachSecret) GetNamespace() string {
	return s.Namespace
}

func (s *CockroachSecret) GetUID() string {
	return s.UID
}

func (s *CockroachSecret) GetResourceVersion() string {
	return s.ResourceVersion
}

func (s *CockroachSecret) Equal(obj CockroachSecret) bool {
	return s.CockroachSecretComparable == obj.CockroachSecretComparable
}

func (c *Client) Secrets() *k8s_generic.Client[CockroachSecret, *CockroachSecret] {
	return k8s_generic.NewClient[CockroachSecret](
		c.builder,
		schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "secrets",
		},
		"Secret",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "cockroachdb",
		}, common.LABEL_FILTERS),
	)
}
