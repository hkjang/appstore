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

	appstore "github.com/hkjang/appstore"
	"github.com/hkjang/appstore/internal/buildinfo"
	"github.com/hkjang/appstore/internal/config"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/database"
	"github.com/hkjang/appstore/internal/guides"
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

	repository := store.New(pool)
	attachBundledGuides(repository, logger)

	service, err := httpapi.New(repository, box, logger)
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

// attachBundledGuides runs before the listener opens, so /health/ready means
// the manuals of this release are already on their apps. A failure is logged
// rather than fatal: a catalog without one manual beats no catalog at all.
func attachBundledGuides(repository *store.Repository, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	summary, err := guides.Sync(ctx, repository, appstore.BundledGuides, logger)
	attrs := []any{
		"apps", summary.Apps, "unmatched", summary.Unmatched,
		"attached", summary.Attached, "replaced", summary.Replaced,
		"unchanged", summary.Unchanged, "failed", summary.Failed,
	}
	if err != nil {
		logger.Error("bundled guide sync stopped early", append(attrs, "error", err)...)
		return
	}
	logger.Info("bundled guides synced", attrs...)
}
