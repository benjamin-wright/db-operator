package tests

import (
	"context"
	"os"
	"testing"

	"ponglehub.co.uk/db-operator/internal/services/k8s/crds"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
)

func makeRedisClients(t *testing.T, namespace string) (
	*k8s_generic.Client[crds.RedisDB, *crds.RedisDB],
	*k8s_generic.Client[crds.RedisClient, *crds.RedisClient],
) {
	rdbs, err := crds.NewRedisDBClient(namespace)
	if err != nil {
		t.Logf("failed to create rdb client: %+v", err)
		t.FailNow()
	}

	rcs, err := crds.NewRedisClientClient(namespace)
	if err != nil {
		t.Logf("failed to create rcs client: %+v", err)
		t.FailNow()
	}

	return rdbs, rcs
}

func TestRedisIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	namespace := os.Getenv("NAMESPACE")

	rdbs, rcs := makeRedisClients(t, namespace)

	mustPass(t, rdbs.DeleteAll(context.Background()))
	mustPass(t, rcs.DeleteAll(context.Background()))

	mustPass(t, rdbs.Create(context.Background(), crds.RedisDB{
		RedisDBComparable: crds.RedisDBComparable{
			Name:    "redis-db",
			Storage: "256Mi",
		},
	}))

	mustPass(t, rcs.Create(context.Background(), crds.RedisClient{
		RedisClientComparable: crds.RedisClientComparable{
			Name:       "my-secret",
			Deployment: "redis-db",
			Unit:       1,
			Secret:     "rdb-secret",
		},
	}))
}
