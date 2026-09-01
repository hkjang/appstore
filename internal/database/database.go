package database

import (
	"context"
	"errors"
	"fmt"

	appstore "github.com/hkjang/appstore"
	"github.com/hkjang/appstore/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates and verifies a PostgreSQL connection pool. Schema changes and
// seed data are intentionally handled separately by Migrate, SeedApps, and
// EnsureBootstrapAdmin so tests and administrative commands can run each step
// independently.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL DSN")
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errors.New("open PostgreSQL pool failed")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("PostgreSQL ping failed")
	}
	return pool, nil
}

// Initialize opens PostgreSQL, applies all embedded migrations under a global
// advisory lock, seeds the curated catalog idempotently, and guarantees the
// configured break-glass bootstrap administrator exists.
func Initialize(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	pool, err := Open(ctx, cfg.PostgresDSN)
	if err != nil {
		return nil, err
	}

	fail := func(step string, stepErr error) (*pgxpool.Pool, error) {
		pool.Close()
		return nil, fmt.Errorf("initialize database (%s): %w", step, stepErr)
	}

	if err := Migrate(ctx, pool); err != nil {
		return fail("migrate", err)
	}
	if err := VerifyEncryptionKey(ctx, pool, cfg.EncryptionKey); err != nil {
		return fail("encryption key", err)
	}
	if err := SeedApps(ctx, pool, appstore.DefaultAppsJSON); err != nil {
		return fail("seed apps", err)
	}
	if _, err := EnsureBootstrapAdmin(ctx, pool, cfg.BootstrapAdmin, cfg.BootstrapAdminPassword); err != nil {
		return fail("bootstrap admin", err)
	}
	return pool, nil
}
