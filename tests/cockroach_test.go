package tests

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"ponglehub.co.uk/db-operator/internal/services/k8s/crds"
	"ponglehub.co.uk/db-operator/internal/services/k8s/resources"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
	"ponglehub.co.uk/db-operator/pkg/postgres"
)

func makeCockroachClients(t *testing.T, namespace string) (
	*k8s_generic.Client[crds.CockroachDB, *crds.CockroachDB],
	*k8s_generic.Client[crds.CockroachClient, *crds.CockroachClient],
	*k8s_generic.Client[crds.CockroachMigration, *crds.CockroachMigration],
	*k8s_generic.Client[resources.CockroachSecret, *resources.CockroachSecret],
) {
	cdbs, err := crds.NewCockroachDBClient(namespace)
	if err != nil {
		t.Logf("failed to create cdb client: %+v", err)
		t.FailNow()
	}

	cclients, err := crds.NewCockroachClientClient(namespace)
	if err != nil {
		t.Logf("failed to create cclient client: %+v", err)
		t.FailNow()
	}

	cms, err := crds.NewCockroachMigrationClient(namespace)
	if err != nil {
		t.Logf("failed to create cmigrations client: %+v", err)
		t.FailNow()
	}

	csc, err := resources.NewCockroachSecretClient(namespace)
	if err != nil {
		t.Logf("failed to create cmigrations client: %+v", err)
		t.FailNow()
	}

	return cdbs, cclients, cms, csc
}

func TestCockroachIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	if testing.Verbose() {
		logger, _ := zap.NewDevelopment()
		zap.ReplaceGlobals(logger)
	}

	namespace := os.Getenv("NAMESPACE")

	cdbs, cclients, cms, csc := makeCockroachClients(t, namespace)

	mustPass(t, cdbs.DeleteAll(context.Background()))
	mustPass(t, cclients.DeleteAll(context.Background()))
	mustPass(t, cms.DeleteAll(context.Background()))

	mustPass(t, cdbs.Create(context.Background(), crds.CockroachDB{
		CockroachDBComparable: crds.CockroachDBComparable{
			Name:    "different-db",
			Storage: "256Mi",
		},
	}))

	mustPass(t, cclients.Create(context.Background(), crds.CockroachClient{
		CockroachClientComparable: crds.CockroachClientComparable{
			Deployment: "different-db",
			Database:   "new_db",
			Name:       "my-client",
			Username:   "my_user",
			Secret:     "my-secret",
		},
	}))

	mustPass(t, cms.Create(context.Background(), crds.CockroachMigration{
		CockroachMigrationComparable: crds.CockroachMigrationComparable{
			Name:       "mig1",
			Deployment: "random-db",
			Database:   "new_db",
			Migration: `
				CREATE TABLE hithere (
					id INT PRIMARY KEY NOT NULL UNIQUE
				);
			`,
			Index: 1,
		},
	}))

	secret, err := waitFor(func() (resources.CockroachSecret, error) {
		return csc.Get(context.Background(), "my-secret")
	})
	mustPass(t, err)

	port, err := strconv.ParseInt(secret.GetPort(), 10, 0)
	mustPass(t, err)

	conn, err := postgres.Connect(postgres.ConnectConfig{
		Host:     secret.GetHost(namespace),
		Port:     int(port),
		Username: secret.User,
	})
	mustPass(t, err)

	rows, err := conn.Query(context.Background(), "SHOW TABLES")
	mustPass(t, err)
	defer rows.Close()

	data, err := rows.Values()
	mustPass(t, err)

	assert.Equal(t, []interface{}{}, data)
}
