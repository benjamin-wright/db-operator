package redis

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

type ConnectConfig struct {
	Host string
	Port int
	Unit int
}

func ConfigFromEnv() (ConnectConfig, error) {
	empty := ConnectConfig{}

	host, ok := os.LookupEnv("REDIS_HOST")
	if !ok {
		return empty, errors.New("failed to lookup REDIS_HOST env var")
	}

	portString, ok := os.LookupEnv("REDIS_PORT")
	if !ok {
		return empty, errors.New("failed to lookup REDIS_PORT env var")
	}

	port, err := strconv.Atoi(portString)
	if err != nil {
		return empty, fmt.Errorf("failed to convert REDIS_PORT: %+v", err)
	}

	unitString, ok := os.LookupEnv("REDIS_UNIT")
	if !ok {
		return empty, errors.New("failed to lookup REDIS_UNIT env var")
	}

	unit, err := strconv.Atoi(unitString)
	if err != nil {
		return empty, fmt.Errorf("failed to convert REDIS_UNIT: %+v", err)
	}

	return ConnectConfig{
		Host: host,
		Port: port,
		Unit: unit,
	}, nil
}
