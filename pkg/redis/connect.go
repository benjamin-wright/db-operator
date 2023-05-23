package redis

import (
	"fmt"

	"github.com/go-redis/redis/v8"
)

func Connect(config ConnectConfig) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", config.Host, config.Port),
		Password: "",
		DB:       config.Unit,
	})

	return rdb, nil
}
