package runtime

import (
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/internal/dbs/cockroach/managers/database"
	"github.com/benjamin-wright/db-operator/internal/dbs/cockroach/managers/deployment"
	nats "github.com/benjamin-wright/db-operator/internal/dbs/nats/manager"
	redis "github.com/benjamin-wright/db-operator/internal/dbs/redis/manager"
)

func Run(namespace string) (func(), error) {
	cockroachDeployManager, err := deployment.New(namespace, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create cockroach deployment manager: %w", err)
	}
	cockroachDeployManager.Start()

	cockroachDBManager, err := database.New(namespace, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create cockroach database manager: %w", err)
	}
	cockroachDBManager.Start()

	redisDeployManager, err := redis.New(namespace, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis deployment manager: %w", err)
	}
	redisDeployManager.Start()

	natsDeployManager, err := nats.New(namespace, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create nats deployment manager: %w", err)
	}
	natsDeployManager.Start()

	return func() {
		cockroachDeployManager.Stop()
		cockroachDBManager.Stop()
		redisDeployManager.Stop()
		natsDeployManager.Stop()
	}, nil
}
