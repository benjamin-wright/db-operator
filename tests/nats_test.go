package tests

import (
	"context"
	"os"
	"testing"

	"github.com/benjamin-wright/db-operator/internal/dbs/nats/k8s"
	"github.com/stretchr/testify/assert"
)

func TestNatsIntegration(t *testing.T) {
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

	mustPass(t, client.DBs().Create(context.Background(), k8s.NatsDB{
		NatsDBComparable: k8s.NatsDBComparable{
			Name: "nats-db",
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), k8s.NatsClient{
		NatsClientComparable: k8s.NatsClientComparable{
			Name:       "my-secret",
			Deployment: "nats-db",
			Secret:     "ndb-secret",
		},
	}))
}
