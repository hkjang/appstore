package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hkjang/appstore/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationAdvisoryLockID is the signed 64-bit representation of "appstore".
// It is stable across releases so concurrently starting containers serialize
// schema changes even before schema_migrations exists.
const migrationAdvisoryLockID int64 = 0x61707073746f7265

var migrationNamePattern = regexp.MustCompile(`^([0-9]{6,})_([a-zA-Z0-9_-]+)\.sql$`)

type migrationFile struct {
	Version  string
	Name     string
	SQL      string
	Checksum string
}

// Migrate applies ordered embedded SQL migrations exactly once. A PostgreSQL
// advisory lock protects the complete migration run across application
// instances, while every individual migration is atomic.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := loadMigrations()
	if err != nil {
		return err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationAdvisoryLockID); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrationAdvisoryLockID)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version text PRIMARY KEY,
			name text NOT NULL,
			checksum text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, file := range files {
		var checksum string
		err := conn.QueryRow(ctx,
			`SELECT checksum FROM schema_migrations WHERE version = $1`, file.Version,
		).Scan(&checksum)
		switch {
		case err == nil:
			if checksum != file.Checksum {
				return fmt.Errorf("migration %s checksum changed after it was applied", file.Version)
			}
			continue
		case err != pgx.ErrNoRows:
			return fmt.Errorf("read migration %s state: %w", file.Version, err)
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", file.Version, err)
		}
		if _, err = tx.Exec(ctx, file.SQL, pgx.QueryExecModeSimpleProtocol); err == nil {
			_, err = tx.Exec(ctx,
				`INSERT INTO schema_migrations(version, name, checksum) VALUES ($1, $2, $3)`,
				file.Version, file.Name, file.Checksum,
			)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s (%s): %w", file.Version, file.Name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", file.Version, err)
		}
	}
	return nil
}

func loadMigrations() ([]migrationFile, error) {
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	result := make([]migrationFile, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		matches := migrationNamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		if _, exists := seen[matches[1]]; exists {
			return nil, fmt.Errorf("duplicate migration version %q", matches[1])
		}
		seen[matches[1]] = struct{}{}

		body, err := fs.ReadFile(migrations.Files, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return nil, fmt.Errorf("migration %s is empty", entry.Name())
		}
		digest := sha256.Sum256(body)
		result = append(result, migrationFile{
			Version:  matches[1],
			Name:     matches[2],
			SQL:      string(body),
			Checksum: hex.EncodeToString(digest[:]),
		})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	if len(result) == 0 {
		return nil, fmt.Errorf("no embedded migrations found")
	}
	return result, nil
}
