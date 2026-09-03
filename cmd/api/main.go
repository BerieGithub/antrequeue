// Command api runs the antrequeue job submission service.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BerieGithub/antrequeue/internal/api"
	"github.com/BerieGithub/antrequeue/internal/config"
	"github.com/BerieGithub/antrequeue/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := config.Load()
	if cfg.ServiceSecret == "" {
		log.Error("ANTREQUEUE_SERVICE_SECRET is required")
		os.Exit(1)
	}

	// v0 uses the in-memory store so the API is runnable immediately.
	// v1 swaps in Postgres behind the same store.Store interface.
	st := store.NewMemory()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(st, cfg.ServiceSecret, log).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("antrequeue api listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("stopped")
}
