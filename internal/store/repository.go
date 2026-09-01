package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("store: not found")
	ErrConflict  = errors.New("store: conflict")
	ErrForbidden = errors.New("store: forbidden")
	ErrInvalid   = errors.New("store: invalid input")
)

// Repository is the PostgreSQL persistence boundary shared by REST, MCP, and
// background maintenance paths. Authorization decisions remain in services;
// transactionally sensitive state transitions are kept here.
type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Pool() *pgxpool.Pool {
	return r.pool
}

type rowScanner interface {
	Scan(dest ...any) error
}

func normalizeError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23P01":
			return fmt.Errorf("%s: %w", operation, ErrConflict)
		case "23502", "23514", "22P02", "22001":
			return fmt.Errorf("%s: %w", operation, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func normalizePage(limit, offset, defaultLimit int) (int, int) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func normalizeKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
