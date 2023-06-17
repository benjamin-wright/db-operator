package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestCockroachPVCFromUnstructured(t *testing.T) {
	pvc := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":              "test-name",
				"namespace":         "test-namespace",
				"uid":               "test-uid",
				"resourceVersion":   "test-resource-version",
				"creationTimestamp": "test-creation-timestamp",
				"labels": map[string]interface{}{
					"app": "test-database",
				},
			},
			"spec": map[string]interface{}{
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{
						"storage": "test-storage",
					},
				},
			},
		},
	}

	cockroachPVC := &CockroachPVC{}
	assert.NoError(t, cockroachPVC.FromUnstructured(pvc))

	assert.Equal(t, "test-name", cockroachPVC.Name)
	assert.Equal(t, "test-namespace", cockroachPVC.Namespace)
	assert.Equal(t, "test-uid", cockroachPVC.UID)
	assert.Equal(t, "test-resource-version", cockroachPVC.ResourceVersion)
	assert.Equal(t, "test-storage", cockroachPVC.Storage)
	assert.Equal(t, "test-database", cockroachPVC.Database)
}
