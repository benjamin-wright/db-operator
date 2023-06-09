package k8s

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type RedisSecretComparable struct {
	Name      string
	Namespace string
	DB        DBRef
	Unit      int
}

type RedisSecret struct {
	RedisSecretComparable
	UID             string
	ResourceVersion string
}

func (s *RedisSecret) GetHost() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", s.DB.Name, s.DB.Namespace)
}

func encode(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}

func (s *RedisSecret) ToUnstructured() *unstructured.Unstructured {
	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      s.Name,
				"namespace": s.Namespace,
				"labels": k8s_generic.Merge(map[string]string{
					"app":                           s.Name,
					"ponglehub.co.uk/resource-type": "redis",
				}, common.LABEL_FILTERS),
			},
			"data": map[string]interface{}{
				"REDIS_HOST": encode(s.GetHost()),
				"REDIS_PORT": encode("6379"),
				"REDIS_UNIT": encode(strconv.FormatInt(int64(s.Unit), 10)),
			},
		},
	}

	return secret
}

func (s *RedisSecret) FromUnstructured(obj *unstructured.Unstructured) error {
	s.Name = obj.GetName()
	s.Namespace = obj.GetNamespace()

	s.UID = string(obj.GetUID())
	s.ResourceVersion = obj.GetResourceVersion()

	hostname, err := k8s_generic.GetEncodedProperty(obj, "data", "REDIS_HOST")
	if err != nil {
		return fmt.Errorf("failed to get REDIS_HOST: %+v", err)
	}
	s.DB.Name = strings.Split(hostname, ".")[0]
	s.DB.Namespace = strings.Split(hostname, ".")[1]

	unit, err := k8s_generic.GetEncodedProperty(obj, "data", "REDIS_UNIT")
	if err != nil {
		return fmt.Errorf("failed to get REDIS_UNIT: %+v", err)
	}

	s.Unit, err = strconv.Atoi(unit)
	if err != nil {
		return fmt.Errorf("failed to parse REDIS_UNIT: %+v", err)
	}

	return nil
}

func (s *RedisSecret) GetName() string {
	return s.Name
}

func (s *RedisSecret) GetNamespace() string {
	return s.Namespace
}

func (s *RedisSecret) GetUID() string {
	return s.UID
}

func (s *RedisSecret) GetResourceVersion() string {
	return s.ResourceVersion
}

func (s *RedisSecret) Equal(obj RedisSecret) bool {
	return s.RedisSecretComparable == obj.RedisSecretComparable
}

func (c *Client) Secrets() *k8s_generic.Client[RedisSecret, *RedisSecret] {
	return k8s_generic.NewClient[RedisSecret](
		c.builder,
		schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "secrets",
		},
		"Secret",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "redis",
		}, common.LABEL_FILTERS),
	)
}
