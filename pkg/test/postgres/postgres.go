package postgres

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/benjamin-wright/db-operator/v2/pkg/postgres/config"
	"github.com/rs/zerolog/log"
)

func Run(name string, port int64) func() {
	os.Setenv("POSTGRES_HOST", "127.0.0.1")
	os.Setenv("POSTGRES_PORT", strconv.FormatInt(port, 10))
	os.Setenv("POSTGRES_USER", "postgres")
	os.Setenv("POSTGRES_NAME", "defaultdb")

	// Try to remove any existing containers
	exec.Command("docker", "stop", name).Run()

	image := "postgres:16.3"

	cmdString := fmt.Sprintf(
		"run --rm -d -p %d:5432 --name %s %s",
		port, name, image,
	)

	cmd := exec.Command("docker", strings.Split(cmdString, " ")...)
	if err := cmd.Run(); err != nil {
		log.Fatal().Err(err).Msg("Failed to start postgres container")
	}

	return func() {
		cmd := exec.Command("docker", "stop", name)
		if err := cmd.Run(); err != nil {
			log.Fatal().Err(err).Msg("Failed to stop postgres container")
		}
	}
}

func Migrate(path string) {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get connection details")
	}

	conn, err := config.Connect(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to postgres")
	}
	defer conn.Close(context.TODO())

	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to read migration file")
	}

	_, err = conn.Exec(context.TODO(), string(data))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to run migration")
	}

	log.Info().Msgf("Ran migration: %s", path)
}
