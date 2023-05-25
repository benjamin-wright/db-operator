package tests

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"ponglehub.co.uk/db-operator/internal/services/k8s/crds"
	"ponglehub.co.uk/db-operator/internal/services/k8s/resources"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
	"ponglehub.co.uk/db-operator/pkg/postgres"
	postgres_helpers "ponglehub.co.uk/db-operator/pkg/test_utils/postgres"
)

func makeCockroachClients(t *testing.T, namespace string) (
	*k8s_generic.Client[crds.CockroachDB, *crds.CockroachDB],
	*k8s_generic.Client[crds.CockroachClient, *crds.CockroachClient],
	*k8s_generic.Client[crds.CockroachMigration, *crds.CockroachMigration],
	*k8s_generic.Client[resources.CockroachSecret, *resources.CockroachSecret],
	*k8s_generic.Client[resources.CockroachStatefulSet, *resources.CockroachStatefulSet],
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

	css, err := resources.NewCockroachStatefulSetClient(namespace)
	if err != nil {
		t.Logf("failed to create c statefule set client: %+v", err)
		t.FailNow()
	}

	return cdbs, cclients, cms, csc, css
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

	cdbs, cclients, cms, csc, css := makeCockroachClients(t, namespace)

	mustPass(t, cdbs.DeleteAll(context.Background()))
	mustPass(t, cclients.DeleteAll(context.Background()))
	mustPass(t, cms.DeleteAll(context.Background()))
	mustPass(t, waitForFail(func() error {
		sss, err := css.GetAll(context.Background())
		if err != nil {
			assert.FailNow(t, "failed to get stateful sets", err)
		}

		if len(sss) > 0 {
			return nil
		}

		return errors.New("no stateful sets found")
	}))

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

	secret, err := waitFor(func() (resources.CockroachSecret, error) {
		return csc.Get(context.Background(), "my-secret")
	})
	mustPass(t, err)

	port, err := strconv.ParseInt(secret.GetPort(), 10, 0)
	mustPass(t, err)

	pg, err := postgres_helpers.New(postgres.ConnectConfig{
		Host:     secret.GetHost(namespace),
		Port:     int(port),
		Username: secret.User,
		Database: "new_db",
	})
	mustPass(t, err)

	tables := pg.GetTableNames(t)

	expected := []string{
		"migrations",
		"hithere",
	}
	assert.Equal(t, expected, tables)
}
