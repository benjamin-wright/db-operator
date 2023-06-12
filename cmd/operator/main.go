package main

import (
	"log"
	"os"
	"os/signal"

	"github.com/benjamin-wright/db-operator/internal/runtime"
	"go.uber.org/zap"
)

func Exported() bool {
	return true
}

func main() {
	logger, _ := zap.NewDevelopment()
	zap.ReplaceGlobals(logger)

	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		zap.S().Fatalf("Must set NAMESPACE environment variable")
	}

	zap.S().Info("Starting operator...")

	stopper, err := runtime.Run(namespace)
	if err != nil {
		zap.S().Fatalf("Failed to start manager: %+v", err)
	}
	defer stopper()

	zap.S().Info("Running!")

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit

	log.Println("Shutting down server...")
}
