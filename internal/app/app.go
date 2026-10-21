package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/nikenesx/aura-server/internal/appointments"
	"github.com/nikenesx/aura-server/internal/audio"
	"github.com/nikenesx/aura-server/internal/auth"
	"github.com/nikenesx/aura-server/internal/card"
	"github.com/nikenesx/aura-server/internal/config"
	"github.com/nikenesx/aura-server/internal/health"
	"github.com/nikenesx/aura-server/internal/jobs"
	"github.com/nikenesx/aura-server/internal/llm"
	"github.com/nikenesx/aura-server/internal/server"
	"github.com/nikenesx/aura-server/internal/store"
	"github.com/nikenesx/aura-server/internal/transcribe"
)

func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := setupLogger(cfg.LogLevel); err != nil {
		return fmt.Errorf("setup logger: %w", err)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	recordings, err := audio.NewDisk(cfg.AudioDir)
	if err != nil {
		return err
	}

	authService, err := auth.NewService(db, cfg.SessionTTL)
	if err != nil {
		return fmt.Errorf("setup auth: %w", err)
	}
	created, err := authService.Bootstrap(ctx, cfg.BootstrapUser, cfg.BootstrapPassword)
	if err != nil {
		return fmt.Errorf("create bootstrap account: %w", err)
	}
	if created {
		slog.Warn("created the bootstrap account; replace it before real doctors use the service",
			"username", cfg.BootstrapUser)
	}
	go sweepSessions(ctx, db)

	asr := transcribe.NewClient(cfg.ASRURL, cfg.ASRTimeout)
	worker := jobs.NewWorker(db, recordings, asr)
	healthHandler := &health.Handler{ASROK: asr.Healthy}
	if cfg.LLMURL != "" {
		model := llm.NewClient(cfg.LLMURL, cfg.LLMModel, cfg.LLMTimeout)
		worker.EnableCards(card.NewGenerator(model, card.VetAppointment))
		healthHandler.LLMOK = model.Healthy
	} else {
		slog.Warn("LLM_URL is empty, appointment cards are disabled")
	}
	if err := worker.Start(ctx); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}

	appointmentsHandler := appointments.NewHandler(db, recordings, worker)
	appointmentsHandler.MaxUpload = cfg.MaxUploadBytes
	appointmentsHandler.UploadTimeout = cfg.UploadTimeout

	router := server.NewRouter(healthHandler, authService, appointmentsHandler)
	srv := server.New(cfg, router)

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("run server: %w", err)
	}

	return nil
}

func sweepSessions(ctx context.Context, db *store.DB) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := db.DeleteExpiredSessions(ctx, now); err != nil {
				slog.Warn("sweep sessions", "error", err)
			}
		}
	}
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
