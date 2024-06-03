package runtime

import (
	"fmt"
	"time"

	nats "github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/manager"
	postgres "github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/manager"
	redis "github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/manager"
)

func Run() (func(), error) {
	postgresDeployManager, err := postgres.New(5 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres deployment manager: %+v", err)
	}
	postgresDeployManager.Start()

	redisDeployManager, err := redis.New(5 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis deployment manager: %+v", err)
	}
	redisDeployManager.Start()

	natsDeployManager, err := nats.New(5 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create nats deployment manager: %+v", err)
	}
	natsDeployManager.Start()

	return func() {
		postgresDeployManager.Stop()
		redisDeployManager.Stop()
		natsDeployManager.Stop()
	}, nil
}
