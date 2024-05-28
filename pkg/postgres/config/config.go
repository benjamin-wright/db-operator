package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	Database string
	Timeout  time.Duration
	Retry    bool
}

func FromEnv() (Config, error) {
	empty := Config{}

	host, ok := os.LookupEnv("POSTGRES_HOST")
	if !ok {
		return empty, errors.New("failed to lookup POSTGRES_HOST env var")
	}

	portString, ok := os.LookupEnv("POSTGRES_PORT")
	if !ok {
		return empty, errors.New("failed to lookup POSTGRES_PORT env var")
	}

	port, err := strconv.Atoi(portString)
	if err != nil {
		return empty, fmt.Errorf("failed to convert POSTGRES_PORT: %+v", err)
	}

	user, ok := os.LookupEnv("POSTGRES_USER")
	if !ok {
		return empty, errors.New("failed to lookup POSTGRES_USER env var")
	}

	password, _ := os.LookupEnv("POSTGRES_PASSWORD")

	database, ok := os.LookupEnv("POSTGRES_NAME")
	if !ok {
		database = "default"
	}

	return Config{
		Host:     host,
		Port:     port,
		Username: user,
		Password: password,
		Database: database,
		Retry:    true,
	}, nil
}

func AdminFromEnv() (Config, error) {
	empty := Config{}

	host, ok := os.LookupEnv("POSTGRES_HOST")
	if !ok {
		return empty, errors.New("failed to lookup POSTGRES_HOST env var")
	}

	portString, ok := os.LookupEnv("POSTGRES_PORT")
	if !ok {
		return empty, errors.New("failed to lookup POSTGRES_PORT env var")
	}

	port, err := strconv.Atoi(portString)
	if err != nil {
		return empty, fmt.Errorf("failed to convert POSTGRES_PORT: %+v", err)
	}

	user, ok := os.LookupEnv("POSTGRES_ADMIN_USER")
	if !ok {
		return empty, errors.New("failed to lookup POSTGRES_ADMIN_USER env var")
	}

	password, _ := os.LookupEnv("POSTGRES_ADMIN_PASS")

	return Config{
		Host:     host,
		Port:     port,
		Username: user,
		Password: password,
		Retry:    true,
	}, nil
}

func (c Config) ConnectionString() string {
	dbSuffix := ""
	if c.Database != "" {
		dbSuffix = "/" + c.Database
	}

	password := ""
	if c.Password != "" {
		password = ":" + url.QueryEscape(c.Password)
	}

	return fmt.Sprintf("postgresql://%s%s@%s:%d%s", c.Username, password, c.Host, c.Port, dbSuffix)
}

func (c Config) String() string {
	dbSuffix := ""
	if c.Database != "" {
		dbSuffix = "/" + c.Database
	}

	password := ""
	if c.Password != "" {
		password = ":**********"
	}

	return fmt.Sprintf("postgresql://%s%s@%s:%d%s", c.Username, password, c.Host, c.Port, dbSuffix)
}
