package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/nikenesx/aura-server/internal/config"
	"github.com/nikenesx/aura-server/internal/health"
	"github.com/nikenesx/aura-server/internal/server"
)

func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := setupLogger(cfg.LogLevel); err != nil {
		return fmt.Errorf("setup logger: %w", err)
	}

	healthHandler := &health.Handler{}
	router := server.NewRouter(healthHandler)
	srv := server.New(cfg, router)

	if err := srv.Run(context.Background()); err != nil {
		return fmt.Errorf("run server: %w", err)
	}

	return nil
}

func setupLogger(level string) error {
	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(level)); err != nil {
		return fmt.Errorf("parse log level %q: %w", level, err)
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(handler))

	return nil
}
