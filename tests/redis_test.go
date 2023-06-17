package tests

import (
	"context"
	"os"
	"testing"

	"github.com/benjamin-wright/db-operator/internal/dbs/redis/k8s"
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

	mustPass(t, client.DBs().DeleteAll(context.Background(), namespace))
	mustPass(t, client.Clients().DeleteAll(context.Background(), namespace))

	mustPass(t, client.DBs().Create(context.Background(), k8s.RedisDB{
		RedisDBComparable: k8s.RedisDBComparable{
			Name:      "redis-db",
			Namespace: namespace,
			Storage:   "256Mi",
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), k8s.RedisClient{
		RedisClientComparable: k8s.RedisClientComparable{
			Name:      "my-secret",
			Namespace: namespace,
			DBRef: k8s.DBRef{
				Name:      "redis-db",
				Namespace: namespace,
			},
			Unit:   1,
			Secret: "rdb-secret",
		},
	}))
}
