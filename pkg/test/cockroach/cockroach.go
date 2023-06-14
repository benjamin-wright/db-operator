package cockroach

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/benjamin-wright/db-operator/pkg/postgres"
	"github.com/rs/zerolog/log"
)

func Run(name string, port int64) func() {
	os.Setenv("POSTGRES_HOST", "127.0.0.1")
	os.Setenv("POSTGRES_PORT", strconv.FormatInt(port, 10))
	os.Setenv("POSTGRES_USER", "root")
	os.Setenv("POSTGRES_NAME", "defaultdb")

	// Try to remove any existing containers
	exec.Command("docker", "stop", name).Run()

	image := "cockroachdb/cockroach:v22.2.8"
	args := "--logtostderr start-single-node --insecure --listen-addr 0.0.0.0:" + strconv.FormatInt(port, 10)

	cmdString := fmt.Sprintf(
		"run --rm -d -p %d:%d --name %s %s %s",
		port, port, name, image, args,
	)

	cmd := exec.Command("docker", strings.Split(cmdString, " ")...)
	if err := cmd.Run(); err != nil {
		log.Fatal().Err(err).Msg("Failed to start cockroach container")
	}

	return func() {
		cmd := exec.Command("docker", "stop", name)
		if err := cmd.Run(); err != nil {
			log.Fatal().Err(err).Msg("Failed to stop cockroach container")
		}
	}
}

func Migrate(path string) {
	cfg, err := postgres.ConfigFromEnv()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get connection details")
	}

	conn, err := postgres.Connect(cfg)
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
