package k8s

import (
	"github.com/benjamin-wright/db-operator/internal/common"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type NatsDeploymentComparable struct {
	Name      string
	Namespace string
	Ready     bool
}

type NatsDeployment struct {
	NatsDeploymentComparable
	UID             string
	ResourceVersion string
}

func (d NatsDeployment) ToUnstructured() *unstructured.Unstructured {
	deployment := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      d.Name,
				"namespace": d.Namespace,
				"labels": k8s_generic.Merge(map[string]string{
					"ponglehub.co.uk/resource-type": "nats",
				}, common.LABEL_FILTERS),
			},
			"spec": map[string]interface{}{
				"replicas": 1,
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": d.Name,
					},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": d.Name,
						},
					},

					"spec": map[string]interface{}{
						"containers": []map[string]interface{}{
							{
								"name":  "database",
								"image": "nats:2.9.17-alpine",
								"resources": map[string]interface{}{
									"requests": map[string]interface{}{
										"cpu":    "0.1",
										"memory": "256Mi",
									},
									"limits": map[string]interface{}{
										"memory": "256Mi",
									},
								},
								"ports": []map[string]interface{}{
									{
										"name":          "tcp",
										"protocol":      "TCP",
										"containerPort": 4222,
									},
								},
								"readinessProbe": map[string]interface{}{
									"httpGet": map[string]interface{}{
										"path": "/",
										"port": 8222,
									},
									"initialDelaySeconds": 1,
									"periodSeconds":       5,
									"failureThreshold":    2,
								},
								"lifecycle": map[string]interface{}{
									"preStop": map[string]interface{}{
										"exec": map[string]interface{}{
											"command": []string{
												"nats-server",
												"-sl=ldm=/var/run/nats/nats.pid",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return deployment
}

func natsDeploymentFromUnstructured(obj *unstructured.Unstructured) (NatsDeployment, error) {
	var err error
	d := NatsDeployment{}

	d.Name = obj.GetName()
	d.Namespace = obj.GetNamespace()
	d.UID = string(obj.GetUID())
	d.ResourceVersion = obj.GetResourceVersion()

	replicas, err := k8s_generic.GetProperty[int64](obj, "status", "replicas")
	if err != nil {
		replicas = 0
	}

	readyReplicas, err := k8s_generic.GetProperty[int64](obj, "status", "readyReplicas")
	if err != nil {
		readyReplicas = 0
	}

	d.Ready = replicas > 0 && replicas == readyReplicas

	return d, nil
}

func (d NatsDeployment) GetName() string {
	return d.Name
}

func (d NatsDeployment) GetNamespace() string {
	return d.Namespace
}

func (d NatsDeployment) GetUID() string {
	return d.UID
}

func (d NatsDeployment) GetResourceVersion() string {
	return d.ResourceVersion
}

func (d NatsDeployment) IsReady() bool {
	return d.Ready
}

func (d NatsDeployment) Equal(obj k8s_generic.Resource) bool {
	if natsDeployment, ok := obj.(*NatsDeployment); ok {
		return d.NatsDeploymentComparable == natsDeployment.NatsDeploymentComparable
	}
	return false
}

func (c *Client) Deployments() *k8s_generic.Client[NatsDeployment] {
	return k8s_generic.NewClient(
		c.builder,
		schema.GroupVersionResource{
			Group:    "apps",
			Version:  "v1",
			Resource: "deployments",
		},
		"Deployment",
		k8s_generic.Merge(map[string]string{
			"ponglehub.co.uk/resource-type": "nats",
		}, common.LABEL_FILTERS),
		natsDeploymentFromUnstructured,
	)
}
