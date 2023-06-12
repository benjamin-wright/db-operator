package runtime

import (
	"fmt"
	"time"

	"ponglehub.co.uk/db-operator/internal/cockroach/managers/database"
	"ponglehub.co.uk/db-operator/internal/cockroach/managers/deployment"
	redis "ponglehub.co.uk/db-operator/internal/redis/manager"
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
		return nil, fmt.Errorf("failed to create cockroach deployment manager: %w", err)
	}
	redisDeployManager.Start()

	return func() {
		cockroachDeployManager.Stop()
		cockroachDBManager.Stop()
		redisDeployManager.Stop()
	}, nil
}
