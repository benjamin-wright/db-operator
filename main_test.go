//go:build !unit
// +build !unit

package main

import (
	"context"
	"os"
	"testing"

	"ponglehub.co.uk/db-operator/internal/services/k8s/crds"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
)

func makeCockroachClients(t *testing.T, namespace string) (
	*k8s_generic.Client[crds.CockroachDB, *crds.CockroachDB],
	*k8s_generic.Client[crds.CockroachClient, *crds.CockroachClient],
	*k8s_generic.Client[crds.CockroachMigration, *crds.CockroachMigration],
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

	return cdbs, cclients, cms
}

func TestCockroachIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	namespace := os.Getenv("NAMESPACE")

	cdbs, cclients, cms := makeCockroachClients(t, namespace)

	err := cdbs.DeleteAll(context.Background())
	if err != nil {
		t.Logf("failed to clear existing dbs: %+v", err)
		t.FailNow()
	}

	err = cclients.DeleteAll(context.Background())
	if err != nil {
		t.Logf("failed to clear existing clients: %+v", err)
		t.FailNow()
	}

	err = cms.DeleteAll(context.Background())
	if err != nil {
		t.Logf("failed to clear existing migrations: %+v", err)
		t.FailNow()
	}

	err = cdbs.Create(context.Background(), crds.CockroachDB{
		CockroachDBComparable: crds.CockroachDBComparable{
			Name:    "different-db",
			Storage: "256Mi",
		},
	})
	if err != nil {
		t.Logf("failed to create test db: %+v", err)
		t.FailNow()
	}

	err = cclients.Create(context.Background(), crds.CockroachClient{
		CockroachClientComparable: crds.CockroachClientComparable{
			Deployment: "different-db",
			Database:   "new_db",
			Name:       "my-client",
			Username:   "my_user",
			Secret:     "my-secret",
		},
	})
	if err != nil {
		t.Logf("failed to create test client: %+v", err)
		t.FailNow()
	}

	err = cms.Create(context.Background(), crds.CockroachMigration{
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
	})
	if err != nil {
		t.Logf("failed to create test migration: %+v", err)
		t.FailNow()
	}
}

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

	err := rdbs.DeleteAll(context.Background())
	if err != nil {
		t.Logf("failed to clear existing dbs: %+v", err)
		t.FailNow()
	}

	err = rcs.DeleteAll(context.Background())
	if err != nil {
		t.Logf("failed to clear existing clients: %+v", err)
		t.FailNow()
	}

	err = rdbs.Create(context.Background(), crds.RedisDB{
		RedisDBComparable: crds.RedisDBComparable{
			Name:    "redis-db",
			Storage: "256Mi",
		},
	})
	if err != nil {
		t.Logf("failed to create test db: %+v", err)
		t.FailNow()
	}

	err = rcs.Create(context.Background(), crds.RedisClient{
		RedisClientComparable: crds.RedisClientComparable{
			Name:       "my-secret",
			Deployment: "redis-db",
			Unit:       1,
			Secret:     "rdb-secret",
		},
	})
	if err != nil {
		t.Logf("failed to create test client: %+v", err)
		t.FailNow()
	}
}
