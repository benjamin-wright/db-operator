package stateful_sets

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var ClientArgs = k8s_generic.ClientArgs[Resource]{
	Schema: schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "statefulsets",
	},
	Kind: "StatefulSet",
	LabelFilters: k8s_generic.Merge(map[string]string{
		"ponglehub.co.uk/resource-type": "postgrescluster",
	}, common.LABEL_FILTERS),
	FromUnstructured: fromUnstructured,
}

type Comparable struct {
	Name      string
	Namespace string
	Storage   string
	Ready     bool
}

type Resource struct {
	Comparable
	UID             string
	ResourceVersion string
}

func (r Resource) ToUnstructured() *unstructured.Unstructured {
	statefulset := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "StatefulSet",
			"metadata": map[string]interface{}{
				"name":      r.Name,
				"namespace": r.Namespace,
				"labels": k8s_generic.Merge(map[string]string{
					"ponglehub.co.uk/resource-type": "postgrescluster",
				}, common.LABEL_FILTERS),
			},
			"spec": map[string]interface{}{
				"replicas": 1,
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": r.Name,
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": r.Name,
						},
					},

					"spec": map[string]interface{}{
						"containers": []map[string]interface{}{
							{
								"name":  "database",
								"image": "postgres:16.3",
								"env": []map[string]interface{}{
									{
										"name":  "POSTGRES_USER",
										"value": "postgres",
									},
									{
										"name":  "POSTGRES_PASSWORD",
										"value": "postgres",
									},
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
										"mountPath": "/var/lib/postgresql/data",
									},
								},
								"ports": []map[string]interface{}{
									{
										"name":          "grpc",
										"protocol":      "TCP",
										"containerPort": 5432,
									},
								},
								"readinessProbe": map[string]interface{}{
									"exec": map[string]interface{}{
										"command": []string{
											"/bin/sh",
											"-c",
											"-e",
											"exec pg_isready -U postgres -h 127.0.0.1 -p 5432",
										},
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
								"ponglehub.co.uk/resource-type": "postgrescluster",
							}, common.LABEL_FILTERS),
						},
						"spec": map[string]interface{}{
							"accessModes": []string{
								"ReadWriteOnce",
							},
							"resources": map[string]interface{}{
								"requests": map[string]interface{}{
									"storage": r.Storage,
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

func fromUnstructured(obj *unstructured.Unstructured) (Resource, error) {
	var err error
	r := Resource{}

	r.Name = obj.GetName()
	r.Namespace = obj.GetNamespace()
	r.UID = string(obj.GetUID())
	r.ResourceVersion = obj.GetResourceVersion()

	r.Storage, err = k8s_generic.GetProperty[string](obj, "spec", "volumeClaimTemplates", "0", "spec", "resources", "requests", "storage")
	if err != nil {
		return r, fmt.Errorf("failed to get storage: %+v", err)
	}

	replicas, err := k8s_generic.GetProperty[int64](obj, "status", "replicas")
	if err != nil {
		replicas = 0
	}

	readyReplicas, err := k8s_generic.GetProperty[int64](obj, "status", "readyReplicas")
	if err != nil {
		readyReplicas = 0
	}

	r.Ready = replicas > 0 && replicas == readyReplicas

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

func (r Resource) GetStorage() string {
	return r.Storage
}

func (r Resource) IsReady() bool {
	return r.Ready
}

func (r Resource) Equal(obj k8s_generic.Resource) bool {
	other, ok := obj.(Resource)
	if !ok {
		return false
	}
	return r.Comparable == other.Comparable
}
