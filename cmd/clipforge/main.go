package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/CyberLoord/clipforge/internal/ai"
	"github.com/CyberLoord/clipforge/internal/config"
	"github.com/CyberLoord/clipforge/internal/httpapi"
	"github.com/CyberLoord/clipforge/internal/media"
	"github.com/CyberLoord/clipforge/internal/storage"
	"github.com/CyberLoord/clipforge/internal/store"
	"github.com/CyberLoord/clipforge/internal/worker"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("application stopped", "error", err)
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	for _, bin := range []string{cfg.FFmpeg, cfg.FFprobe} {
		if _, err = exec.LookPath(bin); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return errors.New("invalid DATABASE_URL")
	}
	poolCfg.MaxConns = int32(cfg.Workers + 8)
	pool, err := pgxpool.NewWithConfig(startup, poolCfg)
	if err != nil {
		return errors.New("cannot create database pool")
	}
	defer pool.Close()
	if err = pool.Ping(startup); err != nil {
		return errors.New("cannot connect to database")
	}
	if err = store.Migrate(startup, pool, filepath.Join(cfg.Migrations, "001_jobs.sql")); err != nil {
		return err
	}
	files, err := storage.New(cfg.DataDir)
	if err != nil {
		return err
	}
	repo := store.New(pool)
	var transcriber ai.Transcriber
	var selector ai.Selector
	var montage ai.MontageSelector
	if cfg.APIKey != "" {
		provider := ai.NewOpenAI(cfg.APIKey, cfg.BaseURL, cfg.Model)
		transcriber = provider
		selector = provider
		montage = ai.NewOpenAI(cfg.APIKey, cfg.BaseURL, cfg.VisionModel)
	}
	jobs := worker.New(repo, media.New(cfg.FFmpeg, cfg.FFprobe), transcriber, selector, montage, worker.Config{Workers: cfg.Workers, LeaseDuration: cfg.LeaseDuration, PollInterval: time.Second, StageTimeout: cfg.StageTimeout, MaxAttempts: cfg.MaxAttempts, MaxDuration: cfg.MaxDuration, DataDir: cfg.DataDir}, logger)
	workersDone := make(chan struct{})
	go func() { defer close(workersDone); jobs.Run(ctx) }()
	server := &http.Server{Addr: cfg.Address, Handler: httpapi.New(repo, files, cfg, logger, pool.Ping), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	serverDone := make(chan error, 1)
	go func() {
		logger.Info("server started", "addr", cfg.Address, "mode", cfg.Mode, "workers", cfg.Workers)
		serverDone <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err = <-serverDone:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		stop()
	}
	shutdown, finish := context.WithTimeout(context.Background(), 15*time.Second)
	defer finish()
	if shutdownErr := server.Shutdown(shutdown); shutdownErr != nil {
		server.Close()
		logger.Warn("forced HTTP shutdown", "error", shutdownErr)
	}
	select {
	case <-workersDone:
	case <-shutdown.Done():
		return shutdown.Err()
	}
	return err
}
