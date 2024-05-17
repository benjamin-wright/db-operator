package k8s

import (
	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type RedisServiceComparable struct {
	Name      string
	Namespace string
}

type RedisService struct {
	RedisServiceComparable
	UID             string
	ResourceVersion string
}

func (s RedisService) ToUnstructured() *unstructured.Unstructured {
	statefulset := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      s.Name,
				"namespace": s.Namespace,
				"labels": k8s_generic.Merge(map[string]string{
					"app":                           s.Name,
					"ponglehub.co.uk/resource-type": "redis",
				}, common.LABEL_FILTERS),
			},
			"spec": map[string]interface{}{
				"ports": []map[string]interface{}{
					{
						"name":       "tcp",
						"port":       6379,
						"protocol":   "TCP",
						"targetPort": "tcp",
					},
				},
				"selector": map[string]interface{}{
					"app": s.Name,
				},
			},
		},
	}

	return statefulset
}

func redisServiceFromUnstructured(obj *unstructured.Unstructured) (RedisService, error) {
	s := RedisService{}

	s.Name = obj.GetName()
	s.Namespace = obj.GetNamespace()
	s.UID = string(obj.GetUID())
	s.ResourceVersion = obj.GetResourceVersion()

	return s, nil
}

func (s RedisService) GetName() string {
	return s.Name
}

func (s RedisService) GetNamespace() string {
	return s.Namespace
}

func (s RedisService) GetUID() string {
	return s.UID
}

func (s RedisService) GetResourceVersion() string {
	return s.ResourceVersion
}

func (s RedisService) Equal(obj k8s_generic.Resource) bool {
	redisService, ok := obj.(*RedisService)
	if !ok {
		return false
	}
	return s.RedisServiceComparable == redisService.RedisServiceComparable
}

func (c *Client) Services() *k8s_generic.Client[RedisService] {
	return k8s_generic.NewClient(
		c.builder,
		schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "services",
		},
		"Service",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "redis",
		}, common.LABEL_FILTERS),
		redisServiceFromUnstructured,
	)
}
