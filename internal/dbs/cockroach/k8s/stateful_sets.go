package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type CockroachStatefulSetComparable struct {
	Name    string
	Storage string
	Ready   bool
}

type CockroachStatefulSet struct {
	CockroachStatefulSetComparable
	UID             string
	ResourceVersion string
}

func (s *CockroachStatefulSet) ToUnstructured(namespace string) *unstructured.Unstructured {
	statefulset := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "StatefulSet",
			"metadata": map[string]interface{}{
				"name": s.Name,
				"labels": k8s_generic.Merge(map[string]string{
					"ponglehub.co.uk/resource-type": "cockroachdb",
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
								"image": "cockroachdb/cockroach:v22.2.8",
								"command": []string{
									"cockroach",
								},
								"args": []string{
									"--logtostderr",
									"start-single-node",
									"--insecure",
								},
								"resources": map[string]interface{}{
									"requests": map[string]interface{}{
										"cpu":    "0.1",
										"memory": "512Mi",
									},
									"limits": map[string]interface{}{
										"memory": "512Mi",
									},
								},
								"volumeMounts": []map[string]interface{}{
									{
										"name":      "datadir",
										"mountPath": "/cockroach/cockroach-data",
									},
								},
								"ports": []map[string]interface{}{
									{
										"name":          "http",
										"protocol":      "TCP",
										"containerPort": 8080,
									},
									{
										"name":          "grpc",
										"protocol":      "TCP",
										"containerPort": 26257,
									},
								},
								"readinessProbe": map[string]interface{}{
									"httpGet": map[string]interface{}{
										"path":   "/health?ready=1",
										"port":   "http",
										"scheme": "HTTP",
									},
									"initialDelaySeconds": 10,
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
								"ponglehub.co.uk/resource-type": "cockroachdb",
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

func (s *CockroachStatefulSet) FromUnstructured(obj *unstructured.Unstructured) error {
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
		return fmt.Errorf("failed to get replicas: %+v", err)
	}

	readyReplicas, err := k8s_generic.GetProperty[int64](obj, "status", "readyReplicas")
	if err != nil {
		readyReplicas = 0
	}

	s.Ready = replicas == readyReplicas && replicas > 0

	return nil
}

func (db *CockroachStatefulSet) GetName() string {
	return db.Name
}

func (db *CockroachStatefulSet) GetUID() string {
	return db.UID
}

func (db *CockroachStatefulSet) GetResourceVersion() string {
	return db.ResourceVersion
}

func (db *CockroachStatefulSet) GetStorage() string {
	return db.Storage
}

func (db *CockroachStatefulSet) IsReady() bool {
	return db.Ready
}

func (db *CockroachStatefulSet) Equal(obj CockroachStatefulSet) bool {
	return db.CockroachStatefulSetComparable == obj.CockroachStatefulSetComparable
}

func (c *Client) StatefulSets() *k8s_generic.Client[CockroachStatefulSet, *CockroachStatefulSet] {
	return k8s_generic.NewClient[CockroachStatefulSet](
		c.builder,
		schema.GroupVersionResource{
			Group:    "apps",
			Version:  "v1",
			Resource: "statefulsets",
		},
		"StatefulSet",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "cockroachdb",
		}, common.LABEL_FILTERS),
	)
}
