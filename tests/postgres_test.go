package tests

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clusters"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/secrets"
	postgres_helpers "github.com/benjamin-wright/db-operator/internal/test_utils/postgres"
	"github.com/benjamin-wright/db-operator/pkg/postgres"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestPostgresIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	if testing.Verbose() {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.Disabled)
	}

	client, err := k8s.New()
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	namespace := os.Getenv("NAMESPACE")

	mustPass(t, client.Clusters().DeleteAll(context.Background(), namespace))
	mustPass(t, client.Clients().DeleteAll(context.Background(), namespace))
	mustPass(t, waitFor(func() error {
		sss, err := client.StatefulSets().GetAll(context.Background())
		if err != nil {
			return err
		}

		if len(sss) > 0 {
			return errors.New("stateful sets still exist")
		}

		return nil
	}))

	mustPass(t, client.Clusters().Create(context.Background(), clusters.Resource{
		Comparable: clusters.Comparable{
			Name:      "different-db",
			Namespace: namespace,
			Storage:   "256Mi",
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), clients.Resource{
		Comparable: clients.Comparable{
			Cluster:   clients.Cluster{Name: "different-db", Namespace: namespace},
			Database:  "new_db",
			Name:      "my-client",
			Namespace: namespace,
			Username:  "my_user",
			Secret:    "my-secret",
		},
	}))

	secret := waitForResult(t, func() (secrets.Resource, error) {
		return client.Secrets().Get(context.Background(), "my-secret", namespace)
	})

	port, err := strconv.ParseInt(secret.GetPort(), 10, 0)
	mustPass(t, err)

	pg, err := postgres_helpers.New(postgres.ConnectConfig{
		Host:     secret.GetHost(),
		Port:     int(port),
		Username: secret.User,
		Password: secret.Password,
		Database: secret.Database,
	})
	mustPass(t, err)

	tables := pg.GetTableNames(t)

	expected := []string{}
	assert.Equal(t, expected, tables)
}
