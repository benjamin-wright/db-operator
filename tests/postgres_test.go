package tests

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/secrets"
	postgres_helpers "github.com/benjamin-wright/db-operator/v2/internal/test_utils/postgres"
	"github.com/benjamin-wright/db-operator/v2/pkg/postgres/config"
	"github.com/benjamin-wright/db-operator/v2/pkg/postgres/migrations"
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

	seed := randomString(8)

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

	clusterName := "test-db-" + seed
	dbName := "db-" + seed

	mustPass(t, client.Clusters().Create(context.Background(), clusters.Resource{
		Comparable: clusters.Comparable{
			Name:      clusterName,
			Namespace: namespace,
			Storage:   "256Mi",
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), clients.Resource{
		Comparable: clients.Comparable{
			Cluster:    clients.Cluster{Name: clusterName, Namespace: namespace},
			Database:   dbName,
			Name:       "my-client-" + seed,
			Namespace:  namespace,
			Username:   "my_user",
			Secret:     "my-secret-" + seed,
			Permission: clients.PermissionAdmin,
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), clients.Resource{
		Comparable: clients.Comparable{
			Cluster:    clients.Cluster{Name: clusterName, Namespace: namespace},
			Database:   dbName,
			Name:       "other-client-" + seed,
			Namespace:  namespace,
			Username:   "other_user",
			Secret:     "other-secret-" + seed,
			Permission: clients.PermissionWrite,
		},
	}))

	owner := waitForResult(t, func() (secrets.Resource, error) {
		return client.Secrets().Get(context.Background(), "my-secret-"+seed, namespace)
	})

	mustPass(t, waitFor(func() error {
		cli, err := client.Clients().Get(context.Background(), "my-client-"+seed, namespace)
		if err != nil {
			return err
		}

		if !cli.Ready {
			return errors.New("client not ready")
		}

		return nil
	}))

	ownerPort, err := strconv.ParseInt(owner.GetPort(), 10, 0)
	mustPass(t, err)

	mig, err := migrations.New(config.Config{
		Host:     owner.GetHost(),
		Port:     int(ownerPort),
		Username: owner.User,
		Password: owner.Password,
		Database: owner.Database,
	})
	mustPass(t, err)

	mustPass(t, mig.Init())
	mustPass(t, mig.Run([]migrations.Migration{
		{
			Index: 1,
			Query: `
				CREATE TABLE test_table (
					id SERIAL PRIMARY KEY,
					name VARCHAR(255) NOT NULL
				)
			`,
		},
	}))

	user := waitForResult(t, func() (secrets.Resource, error) {
		return client.Secrets().Get(context.Background(), "other-secret-"+seed, namespace)
	})

	userPort, err := strconv.ParseInt(user.GetPort(), 10, 0)
	mustPass(t, err)

	pg, err := postgres_helpers.New(config.Config{
		Host:     user.GetHost(),
		Port:     int(userPort),
		Username: user.User,
		Password: user.Password,
		Database: user.Database,
	})
	mustPass(t, err)

	tables := pg.GetTableNames(t)

	expected := []string{"migrations", "test_table"}
	assert.Equal(t, expected, tables)
}
