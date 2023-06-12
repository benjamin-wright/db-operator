package tests

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"ponglehub.co.uk/db-operator/internal/cockroach/k8s"
	"ponglehub.co.uk/db-operator/pkg/postgres"
	postgres_helpers "ponglehub.co.uk/db-operator/pkg/test_utils/postgres"
)

func TestCockroachIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	if testing.Verbose() {
		logger, _ := zap.NewDevelopment()
		zap.ReplaceGlobals(logger)
	}

	namespace := os.Getenv("NAMESPACE")

	client, err := k8s.New(namespace)
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	mustPass(t, client.DBs().DeleteAll(context.Background()))
	mustPass(t, client.Clients().DeleteAll(context.Background()))
	mustPass(t, client.Migrations().DeleteAll(context.Background()))
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

	mustPass(t, client.DBs().Create(context.Background(), k8s.CockroachDB{
		CockroachDBComparable: k8s.CockroachDBComparable{
			Name:    "different-db",
			Storage: "256Mi",
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), k8s.CockroachClient{
		CockroachClientComparable: k8s.CockroachClientComparable{
			Deployment: "different-db",
			Database:   "new_db",
			Name:       "my-client",
			Username:   "my_user",
			Secret:     "my-secret",
		},
	}))

	mustPass(t, client.Migrations().Create(context.Background(), k8s.CockroachMigration{
		CockroachMigrationComparable: k8s.CockroachMigrationComparable{
			Name:       "mig1",
			Deployment: "different-db",
			Database:   "new_db",
			Migration: `
				CREATE TABLE hithere (
					id INT PRIMARY KEY NOT NULL UNIQUE
				);
			`,
			Index: 1,
		},
	}))

	secret := waitForResult(t, func() (k8s.CockroachSecret, error) {
		return client.Secrets().Get(context.Background(), "my-secret")
	})

	port, err := strconv.ParseInt(secret.GetPort(), 10, 0)
	mustPass(t, err)

	pg, err := postgres_helpers.New(postgres.ConnectConfig{
		Host:     secret.GetHost(namespace),
		Port:     int(port),
		Username: secret.User,
		Database: secret.Database,
	})
	mustPass(t, err)

	tables := pg.GetTableNames(t)

	expected := []string{
		"migrations",
		"hithere",
	}
	assert.Equal(t, expected, tables)
}
