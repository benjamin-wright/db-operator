package secrets

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
)

func decode(t *testing.T, data interface{}) string {
	dataString, ok := data.(string)
	if !assert.True(t, ok, "data is not a string") {
		t.FailNow()
	}

	decoded, err := base64.StdEncoding.DecodeString(dataString)
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	return string(decoded)
}

func TestCockroachSecretFromUnstructured(t *testing.T) {
	secret := &Resource{
		Comparable: Comparable{
			Name:      "test-name",
			Namespace: "test-namespace",
			Cluster: Cluster{
				Name:      "test-db",
				Namespace: "db-namespace",
			},
			Database: "test-database",
			User:     "test-user",
		},
	}

	unstructured := secret.ToUnstructured()
	assert.Equal(t, "test-name", unstructured.GetName())
	assert.Equal(t, "test-namespace", unstructured.GetNamespace())
	assert.Equal(t, "test-name", unstructured.Object["metadata"].(map[string]interface{})["labels"].(map[string]string)["app"])

	assert.Equal(t, "test-db.db-namespace.svc.cluster.local", decode(t, unstructured.Object["data"].(map[string]interface{})["POSTGRES_HOST"]))
	assert.Equal(t, "26257", decode(t, unstructured.Object["data"].(map[string]interface{})["POSTGRES_PORT"]))
	assert.Equal(t, "test-user", decode(t, unstructured.Object["data"].(map[string]interface{})["POSTGRES_USER"]))
	assert.Equal(t, "test-database", decode(t, unstructured.Object["data"].(map[string]interface{})["POSTGRES_NAME"]))
}
