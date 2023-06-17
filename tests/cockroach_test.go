package tests

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/benjamin-wright/db-operator/internal/dbs/cockroach/k8s"
	postgres_helpers "github.com/benjamin-wright/db-operator/internal/test_utils/postgres"
	"github.com/benjamin-wright/db-operator/pkg/postgres"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestCockroachIntegration(t *testing.T) {
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

	mustPass(t, client.DBs().DeleteAll(context.Background(), namespace))
	mustPass(t, client.Clients().DeleteAll(context.Background(), namespace))
	mustPass(t, client.Migrations().DeleteAll(context.Background(), namespace))
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
			Name:      "different-db",
			Namespace: namespace,
			Storage:   "256Mi",
		},
	}))

	mustPass(t, client.Clients().Create(context.Background(), k8s.CockroachClient{
		CockroachClientComparable: k8s.CockroachClientComparable{
			DBRef:     k8s.DBRef{Name: "different-db", Namespace: namespace},
			Database:  "new_db",
			Name:      "my-client",
			Namespace: namespace,
			Username:  "my_user",
			Secret:    "my-secret",
		},
	}))

	mustPass(t, client.Migrations().Create(context.Background(), k8s.CockroachMigration{
		CockroachMigrationComparable: k8s.CockroachMigrationComparable{
			Name:      "mig1",
			Namespace: namespace,
			DBRef:     k8s.DBRef{Name: "different-db", Namespace: namespace},
			Database:  "new_db",
			Migration: `
				CREATE TABLE hithere (
					id INT PRIMARY KEY NOT NULL UNIQUE
				);
			`,
			Index: 1,
		},
	}))

	secret := waitForResult(t, func() (k8s.CockroachSecret, error) {
		return client.Secrets().Get(context.Background(), "my-secret", namespace)
	})

	port, err := strconv.ParseInt(secret.GetPort(), 10, 0)
	mustPass(t, err)

	pg, err := postgres_helpers.New(postgres.ConnectConfig{
		Host:     secret.GetHost(),
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
