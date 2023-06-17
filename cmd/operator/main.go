package main

import (
	"os"
	"os/signal"

	"github.com/benjamin-wright/db-operator/internal/runtime"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Exported() bool {
	return true
}

func main() {
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	level, err := zerolog.ParseLevel(logLevel)
	if err != nil {
		panic(err)
	}

	zerolog.SetGlobalLevel(level)

	log.Info().Msg("Starting operator...")

	stopper, err := runtime.Run()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to start manager")
	}
	defer stopper()

	log.Info().Msg("Started operator")

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit

	log.Info().Msg("Received interrupt signal")
}
