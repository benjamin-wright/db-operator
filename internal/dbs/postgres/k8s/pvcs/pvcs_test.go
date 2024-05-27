package pvcs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestPostgresPVCFromUnstructured(t *testing.T) {
	pvc := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":              "test-name",
				"namespace":         "test-namespace",
				"uid":               "test-uid",
				"resourceVersion":   "test-resource-version",
				"creationTimestamp": "test-creation-timestamp",
				"labels": map[string]interface{}{
					"app": "test-cluster",
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

	postgresPVC, err := fromUnstructured(pvc)
	assert.NoError(t, err)

	assert.Equal(t, "test-name", postgresPVC.Name)
	assert.Equal(t, "test-namespace", postgresPVC.Namespace)
	assert.Equal(t, "test-uid", postgresPVC.UID)
	assert.Equal(t, "test-resource-version", postgresPVC.ResourceVersion)
	assert.Equal(t, "test-storage", postgresPVC.Storage)
	assert.Equal(t, "test-cluster", postgresPVC.Cluster)
}
