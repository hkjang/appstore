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

	"github.com/hkjang/appstore/internal/buildinfo"
	"github.com/hkjang/appstore/internal/config"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/database"
	"github.com/hkjang/appstore/internal/httpapi"
	"github.com/hkjang/appstore/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("appstore stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	box, err := appcrypto.NewSecretBox(cfg.EncryptionKey)
	if err != nil {
		return err
	}

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 2*time.Minute)
	pool, err := database.Initialize(startupCtx, cfg)
	cancelStartup()
	if err != nil {
		return err
	}
	defer pool.Close()

	service, err := httpapi.New(store.New(pool), box, logger)
	if err != nil {
		return err
	}
	handler, err := service.Handler()
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("appstore listening", "address", cfg.ListenAddress, "version", buildinfo.Current().Version)
		serveErrors <- server.ListenAndServe()
	}()

	stopCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-stopCtx.Done():
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelShutdown()
	logger.Info("appstore shutting down")
	return server.Shutdown(shutdownCtx)
}
