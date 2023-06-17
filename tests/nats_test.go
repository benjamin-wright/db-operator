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

	client, err := k8s.New()
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	mustPass(t, client.DBs().DeleteAll(context.Background(), namespace))
	mustPass(t, client.Clients().DeleteAll(context.Background(), namespace))

	mustPass(t, client.DBs().Create(context.Background(), k8s.NatsDB{
		NatsDBComparable: k8s.NatsDBComparable{
			Name:      "nats-db",
			Namespace: namespace,
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), k8s.NatsClient{
		NatsClientComparable: k8s.NatsClientComparable{
			Name:      "my-secret",
			Namespace: namespace,
			DBRef: k8s.DBRef{
				Name:      "nats-db",
				Namespace: namespace,
			},
			Secret: "ndb-secret",
		},
	}))
}
