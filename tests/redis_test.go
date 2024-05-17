package tests

import (
	"context"
	"os"
	"testing"

	"github.com/benjamin-wright/db-operator/internal/dbs/redis/k8s"
	"github.com/benjamin-wright/db-operator/internal/dbs/redis/k8s/clients"
	"github.com/benjamin-wright/db-operator/internal/dbs/redis/k8s/clusters"
	"github.com/stretchr/testify/assert"
)

func TestRedisIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	namespace := os.Getenv("NAMESPACE")

	client, err := k8s.New()
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	mustPass(t, client.Clusters().DeleteAll(context.Background(), namespace))
	mustPass(t, client.Clients().DeleteAll(context.Background(), namespace))

	mustPass(t, client.Clusters().Create(context.Background(), clusters.Resource{
		Comparable: clusters.Comparable{
			Name:      "redis-db",
			Namespace: namespace,
			Storage:   "256Mi",
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), clients.Resource{
		Comparable: clients.Comparable{
			Name:      "my-secret",
			Namespace: namespace,
			Cluster: clients.Cluster{
				Name:      "redis-db",
				Namespace: namespace,
			},
			Unit:   1,
			Secret: "rdb-secret",
		},
	}))
}
