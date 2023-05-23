package runtime

import (
	"fmt"
	"time"

	"ponglehub.co.uk/db-operator/internal/manager"
	"ponglehub.co.uk/db-operator/internal/services/k8s/crds"
	"ponglehub.co.uk/db-operator/internal/services/k8s/resources"
)

func Run(namespace string) (func(), error) {
	cdbClient, err := crds.NewCockroachDBClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for cockroach dbs: %+v", err)
	}

	ccClient, err := crds.NewCockroachClientClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for cockroach clients: %+v", err)
	}

	cmClient, err := crds.NewCockroachMigrationClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for cockroach migrations: %+v", err)
	}

	cssClient, err := resources.NewCockroachStatefulSetClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for cockroach stateful sets: %+v", err)
	}

	cpvcClient, err := resources.NewCockroachPVCClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for cockroach persistent volume claims: %+v", err)
	}

	csvcClient, err := resources.NewCockroachServiceClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for cockroach services: %+v", err)
	}

	csecretClient, err := resources.NewCockroachSecretClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for cockroach secrets: %+v", err)
	}

	rdbClient, err := crds.NewRedisDBClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for redis dbs: %+v", err)
	}

	rcClient, err := crds.NewRedisClientClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for redis clients: %+v", err)
	}

	rssClient, err := resources.NewRedisStatefulSetClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for redis stateful sets: %+v", err)
	}

	rpvcClient, err := resources.NewRedisPVCClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for redis persistent volume claims: %+v", err)
	}

	rsvcClient, err := resources.NewRedisServiceClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for redis services: %+v", err)
	}

	rsecretClient, err := resources.NewRedisSecretClient(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for redis secrets: %+v", err)
	}

	m, err := manager.New(namespace,
		cdbClient, ccClient, cmClient,
		cssClient, cpvcClient, csvcClient, csecretClient,
		rdbClient, rcClient,
		rssClient, rpvcClient, rsvcClient, rsecretClient,
		time.Second*5,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start the manager: %+v", err)
	}

	m.Start()

	return func() { m.Stop() }, nil
}
