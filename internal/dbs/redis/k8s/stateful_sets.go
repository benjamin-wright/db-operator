package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type RedisStatefulSetComparable struct {
	Name    string
	Storage string
	Ready   bool
}

type RedisStatefulSet struct {
	RedisStatefulSetComparable
	UID             string
	ResourceVersion string
}

func (s *RedisStatefulSet) ToUnstructured(namespace string) *unstructured.Unstructured {
	statefulset := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "StatefulSet",
			"metadata": map[string]interface{}{
				"name": s.Name,
				"labels": k8s_generic.Merge(map[string]string{
					"ponglehub.co.uk/resource-type": "redis",
				}, common.LABEL_FILTERS),
			},
			"spec": map[string]interface{}{
				"replicas": 1,
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": s.Name,
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": s.Name,
						},
					},

					"spec": map[string]interface{}{
						"containers": []map[string]interface{}{
							{
								"name":  "database",
								"image": "redis:7.0.11-alpine3.17",
								"resources": map[string]interface{}{
									"requests": map[string]interface{}{
										"cpu":    "0.1",
										"memory": "256Mi",
									},
									"limits": map[string]interface{}{
										"memory": "256Mi",
									},
								},
								"volumeMounts": []map[string]interface{}{
									{
										"name":      "datadir",
										"mountPath": "/data",
									},
								},
								"ports": []map[string]interface{}{
									{
										"name":          "tcp",
										"protocol":      "TCP",
										"containerPort": 6379,
									},
								},
								"readinessProbe": map[string]interface{}{
									"exec": map[string]interface{}{
										"command": []string{
											"redis-cli",
											"ping",
										},
									},
									"initialDelaySeconds": 1,
									"periodSeconds":       5,
									"failureThreshold":    2,
								},
							},
						},
						"volumes": []map[string]interface{}{
							{
								"name": "datadir",
								"persistentVolumeClaim": map[string]interface{}{
									"claimName": "datadir",
								},
							},
						},
					},
				},
				"volumeClaimTemplates": []map[string]interface{}{
					{
						"metadata": map[string]interface{}{
							"name": "datadir",
							"labels": k8s_generic.Merge(map[string]string{
								"ponglehub.co.uk/resource-type": "redis",
							}, common.LABEL_FILTERS),
						},
						"spec": map[string]interface{}{
							"accessModes": []string{
								"ReadWriteOnce",
							},
							"resources": map[string]interface{}{
								"requests": map[string]interface{}{
									"storage": s.Storage,
								},
							},
						},
					},
				},
			},
		},
	}

	return statefulset
}

func (s *RedisStatefulSet) FromUnstructured(obj *unstructured.Unstructured) error {
	var err error

	s.Name = obj.GetName()
	s.UID = string(obj.GetUID())
	s.ResourceVersion = obj.GetResourceVersion()

	s.Storage, err = k8s_generic.GetProperty[string](obj, "spec", "volumeClaimTemplates", "0", "spec", "resources", "requests", "storage")
	if err != nil {
		return fmt.Errorf("failed to get storage: %+v", err)
	}

	replicas, err := k8s_generic.GetProperty[int64](obj, "status", "replicas")
	if err != nil {
		replicas = 0
	}

	readyReplicas, err := k8s_generic.GetProperty[int64](obj, "status", "readyReplicas")
	if err != nil {
		readyReplicas = 0
	}

	s.Ready = replicas > 0 && replicas == readyReplicas

	return nil
}

func (db *RedisStatefulSet) GetName() string {
	return db.Name
}

func (db *RedisStatefulSet) GetUID() string {
	return db.UID
}

func (db *RedisStatefulSet) GetResourceVersion() string {
	return db.ResourceVersion
}

func (db *RedisStatefulSet) GetStorage() string {
	return db.Storage
}

func (db *RedisStatefulSet) IsReady() bool {
	return db.Ready
}

func (db *RedisStatefulSet) Equal(obj RedisStatefulSet) bool {
	return db.RedisStatefulSetComparable == obj.RedisStatefulSetComparable
}

func (c *Client) StatefulSets() *k8s_generic.Client[RedisStatefulSet, *RedisStatefulSet] {
	return k8s_generic.NewClient[RedisStatefulSet](
		c.builder,
		schema.GroupVersionResource{
			Group:    "apps",
			Version:  "v1",
			Resource: "statefulsets",
		},
		"StatefulSet",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "redis",
		}, common.LABEL_FILTERS),
	)
}
