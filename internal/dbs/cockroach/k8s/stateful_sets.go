package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type CockroachStatefulSetComparable struct {
	Name      string
	Namespace string
	Storage   string
	Ready     bool
}

type CockroachStatefulSet struct {
	CockroachStatefulSetComparable
	UID             string
	ResourceVersion string
}

func (s CockroachStatefulSet) ToUnstructured() *unstructured.Unstructured {
	statefulset := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "StatefulSet",
			"metadata": map[string]interface{}{
				"name":      s.Name,
				"namespace": s.Namespace,
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

func cockroachStatefulSetFromUnstructured(obj *unstructured.Unstructured) (CockroachStatefulSet, error) {
	var err error
	s := CockroachStatefulSet{}

	s.Name = obj.GetName()
	s.Namespace = obj.GetNamespace()
	s.UID = string(obj.GetUID())
	s.ResourceVersion = obj.GetResourceVersion()

	s.Storage, err = k8s_generic.GetProperty[string](obj, "spec", "volumeClaimTemplates", "0", "spec", "resources", "requests", "storage")
	if err != nil {
		return s, fmt.Errorf("failed to get storage: %+v", err)
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

	return s, nil
}

func (s CockroachStatefulSet) GetName() string {
	return s.Name
}

func (s CockroachStatefulSet) GetNamespace() string {
	return s.Namespace
}

func (s CockroachStatefulSet) GetUID() string {
	return s.UID
}

func (s CockroachStatefulSet) GetResourceVersion() string {
	return s.ResourceVersion
}

func (s CockroachStatefulSet) GetStorage() string {
	return s.Storage
}

func (s CockroachStatefulSet) IsReady() bool {
	return s.Ready
}

func (s CockroachStatefulSet) Equal(obj k8s_generic.Resource) bool {
	other, ok := obj.(CockroachStatefulSet)
	if !ok {
		return false
	}
	return s.CockroachStatefulSetComparable == other.CockroachStatefulSetComparable
}

func (c *Client) StatefulSets() *k8s_generic.Client[CockroachStatefulSet] {
	return k8s_generic.NewClient(
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
		cockroachStatefulSetFromUnstructured,
	)
}
