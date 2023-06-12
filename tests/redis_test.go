package tests

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"ponglehub.co.uk/db-operator/internal/redis/k8s"
)

func TestRedisIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	namespace := os.Getenv("NAMESPACE")

	client, err := k8s.New(namespace)
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	mustPass(t, client.DBs().DeleteAll(context.Background()))
	mustPass(t, client.Clients().DeleteAll(context.Background()))

	mustPass(t, client.DBs().Create(context.Background(), k8s.RedisDB{
		RedisDBComparable: k8s.RedisDBComparable{
			Name:    "redis-db",
			Storage: "256Mi",
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), k8s.RedisClient{
		RedisClientComparable: k8s.RedisClientComparable{
			Name:       "my-secret",
			Deployment: "redis-db",
			Unit:       1,
			Secret:     "rdb-secret",
		},
	}))
}
