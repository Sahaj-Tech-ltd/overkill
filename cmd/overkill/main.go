package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	setupLogging()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := executeRootCommand(ctx); err != nil {
		os.Exit(1)
	}
}

func setupLogging() {
	writer := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "15:04:05",
	}
	log.Logger = log.Output(writer)
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
}
