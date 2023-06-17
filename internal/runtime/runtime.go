package runtime

import (
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/internal/dbs/cockroach/managers/database"
	"github.com/benjamin-wright/db-operator/internal/dbs/cockroach/managers/deployment"
	nats "github.com/benjamin-wright/db-operator/internal/dbs/nats/manager"
	redis "github.com/benjamin-wright/db-operator/internal/dbs/redis/manager"
)

func Run() (func(), error) {
	cockroachDeployManager, err := deployment.New(5 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create cockroach deployment manager: %+v", err)
	}
	cockroachDeployManager.Start()

	cockroachDBManager, err := database.New(5 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create cockroach database manager: %+v", err)
	}
	cockroachDBManager.Start()

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
		cockroachDeployManager.Stop()
		cockroachDBManager.Stop()
		redisDeployManager.Stop()
		natsDeployManager.Stop()
	}, nil
}
