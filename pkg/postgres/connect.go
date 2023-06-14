package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v4"
	"github.com/rs/zerolog/log"
)

func getConnection(config *pgx.ConnConfig) *pgx.Conn {
	finished := make(chan *pgx.Conn, 1)

	go func(finished chan<- *pgx.Conn) {
		attempts := 0
		limit := 10
		backoff := time.Duration(1)
		var connection *pgx.Conn
		var err error
		for attempts < limit {
			attempts += 1
			connection, err = pgx.ConnectConfig(context.Background(), config)
			if err != nil {
				time.Sleep(time.Second * backoff)
				backoff = backoff + time.Duration(1)
			} else {
				log.Info().Msg("Connected")
				break
			}
		}

		if connection == nil {
			log.Warn().Err(err).Msg("Failed to connect")
		}

		finished <- connection
	}(finished)

	return <-finished
}

func Connect(config ConnectConfig) (*pgx.Conn, error) {
	dbSuffix := ""
	if config.Database != "" {
		dbSuffix = "/" + config.Database
	}

	connectionString := fmt.Sprintf("postgresql://%s@%s:%d%s", config.Username, config.Host, config.Port, dbSuffix)

	log.Info().Msgf("Connecting to postgres with connection string: %s", connectionString)

	pgxConfig, err := pgx.ParseConfig(connectionString)
	if err != nil {
		return nil, err
	}

	pgxConfig.ConnectTimeout = time.Second * 2

	conn := getConnection(pgxConfig)
	if conn == nil {
		return nil, errors.New("failed to create connection, exiting")
	}

	return conn, nil
}
