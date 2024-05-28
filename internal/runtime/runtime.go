package runtime

import (
	"fmt"
	"time"

	nats "github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/manager"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/managers/database"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/managers/deployment"
	redis "github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/manager"
)

func Run() (func(), error) {
	postgresDeployManager, err := deployment.New(5 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres deployment manager: %+v", err)
	}
	postgresDeployManager.Start()

	postgresDBManager, err := database.New(5 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres database manager: %+v", err)
	}
	postgresDBManager.Start()

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
		postgresDBManager.Stop()
		redisDeployManager.Stop()
		natsDeployManager.Stop()
	}, nil
}
