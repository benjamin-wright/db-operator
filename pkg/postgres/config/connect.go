package config

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

func getConnection(config *pgx.ConnConfig, retry bool) (*pgx.Conn, error) {
	attempts := 0
	limit := 1
	if retry {
		limit = 10
	}
	backoff := time.Duration(1)
	var connection *pgx.Conn
	var err error
	for attempts < limit {
		attempts += 1
		connection, err = pgx.ConnectConfig(context.Background(), config)
		if err != nil {
			log.Debug().Err(err).Msg("Failed to connect")
			time.Sleep(time.Second * backoff)
			backoff = backoff + time.Duration(1)
		} else {
			log.Debug().Msg("Connected")
			break
		}
	}

	if connection == nil {
		return nil, err
	}

	return connection, err
}

func Connect(config Config) (*pgx.Conn, error) {
	connectionString := config.ConnectionString()

	pgxConfig, err := pgx.ParseConfig(connectionString)
	if err != nil {
		return nil, err
	}

	if config.Timeout != 0 {
		pgxConfig.ConnectTimeout = config.Timeout
	} else {
		pgxConfig.ConnectTimeout = time.Second * 2
	}

	conn, err := getConnection(pgxConfig, config.Retry)
	if err != nil {
		return nil, err
	}

	if conn == nil {
		return nil, errors.New("failed to create connection without error")
	}

	return conn, nil
}
