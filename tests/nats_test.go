package tests

import (
	"context"
	"os"
	"testing"

	"github.com/benjamin-wright/db-operator/internal/dbs/nats/k8s"
	"github.com/benjamin-wright/db-operator/internal/dbs/nats/k8s/clients"
	"github.com/benjamin-wright/db-operator/internal/dbs/nats/k8s/clusters"
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

	mustPass(t, client.Clusters().DeleteAll(context.Background(), namespace))
	mustPass(t, client.Clients().DeleteAll(context.Background(), namespace))

	mustPass(t, client.Clusters().Create(context.Background(), clusters.Resource{
		Comparable: clusters.Comparable{
			Name:      "nats-db",
			Namespace: namespace,
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), clients.Resource{
		Comparable: clients.Comparable{
			Name:      "my-secret",
			Namespace: namespace,
			Cluster: clients.Cluster{
				Name:      "nats-db",
				Namespace: namespace,
			},
			Secret: "ndb-secret",
		},
	}))
}
