package main

import (
	"os"

	"github.com/benjamin-wright/db-operator/pkg/postgres/config"
	"github.com/benjamin-wright/db-operator/pkg/postgres/migrations"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	path, ok := os.LookupEnv("POSTGRES_MIGRATIONS_PATH")
	if !ok {
		path = "/migrations"
	}

	log.Info().Msgf("loading migrations from %s", path)
	m, err := migrations.LoadMigrations(path)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load migrations")
	}

	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config from env")
	}

	client, err := migrations.New(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create client")
	}

	log.Info().Msg("initializing client")
	err = client.Init()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to init client")
	}

	log.Info().Msg("running migrations")
	err = client.Run(m)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to run migrations")
	}

	log.Info().Msg("migrations complete")
}
