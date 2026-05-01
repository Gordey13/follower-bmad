package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"follower/internal/app"
	"follower/internal/config"
	"follower/internal/logger"
)

func main() {
	logger := slog.New(logger.NewCompactHandler(os.Stdout))

	cfgPath := os.Getenv("FOLLOWER_CONFIG_PATH")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Error("configuration validation failed", "error", err)
		os.Exit(1)
	}

	application, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("application bootstrap failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("application stopped with error", "error", err)
		os.Exit(1)
	}
}
