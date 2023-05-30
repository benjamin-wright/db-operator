package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCockroachSecretFromUnstructured(t *testing.T) {
	secret := &CockroachSecret{
		CockroachSecretComparable: CockroachSecretComparable{
			Name: "test-name",
			DB:   "test-db",
		},
	}

	unstructured := secret.ToUnstructured("test-namespace")
	assert.Equal(t, "test-name", unstructured.GetName())
	assert.Equal(t, "test-namespace", unstructured.GetNamespace())
	assert.Equal(t, "test-name", unstructured.Object["metadata"].(map[string]interface{})["labels"].(map[string]string)["app"])
}
