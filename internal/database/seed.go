package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const bootstrapSubject = "bootstrap:admin"
const bootstrapAdvisoryLockID int64 = 0x61707073626f6f74

type seedApp struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	Subtitle     string   `json:"subtitle"`
	Description  string   `json:"description"`
	Category     string   `json:"category"`
	CategoryName string   `json:"category_name"`
	CategoryIcon string   `json:"category_icon"`
	Language     string   `json:"language"`
	CreatedAt    string   `json:"created_at"`
	PushedAt     string   `json:"pushed_at"`
	Stars        int      `json:"stars"`
	Featured     bool     `json:"featured"`
	TechStack    []string `json:"tech_stack"`
	Icon         string   `json:"icon"`
	Gradient     string   `json:"gradient"`
	HasMCP       bool     `json:"has_mcp"`
	// GitHub URLs are deliberately not represented here. Unknown JSON fields
	// are ignored, making it impossible for the seed path to persist them.
}

type seedCategory struct {
	Slug        string
	Name        string
	Icon        string
	Description string
	Position    int
}

// SeedApps imports the bundled curated catalog. It is safe on every startup:
// both category and app inserts preserve any existing runtime-managed row.
func SeedApps(ctx context.Context, pool *pgxpool.Pool, data []byte) error {
	apps, categories, err := decodeSeedApps(data)
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin app seed: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	categoryIDs := make(map[string]uuid.UUID, len(categories))
	for _, category := range categories {
		if _, err := tx.Exec(ctx, `
			INSERT INTO categories(slug, name, icon, description, position)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (slug) DO NOTHING`,
			category.Slug, category.Name, category.Icon, category.Description, category.Position,
		); err != nil {
			return fmt.Errorf("seed category %q: %w", category.Slug, err)
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM categories WHERE slug = $1`, category.Slug).Scan(&id); err != nil {
			return fmt.Errorf("read seeded category %q: %w", category.Slug, err)
		}
		categoryIDs[category.Slug] = id
	}

	now := time.Now().UTC()
	for _, app := range apps {
		categoryID, ok := categoryIDs[app.Category]
		if !ok {
			return fmt.Errorf("seed app %q references missing category %q", app.Name, app.Category)
		}
		createdAt := parseSeedDate(app.CreatedAt, now)
		updatedAt := parseSeedDate(app.PushedAt, createdAt)
		name := firstNonEmpty(app.Title, app.Name)
		summary := firstNonEmpty(app.Subtitle, app.Description, name)
		description := firstNonEmpty(app.Description, summary)
		icon := firstNonEmpty(app.Icon, "📦")
		framework := detectFramework(app.TechStack)

		if _, err := tx.Exec(ctx, `
			INSERT INTO apps(
				external_seed_id, category_id, name, slug, summary, description,
				icon, gradient, service_url, tags, screenshots, language,
				framework, supports_mcp, supports_api, visibility, status,
				featured, trending_score, created_at, updated_at, published_at
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, '', $9, '[]'::jsonb, $10,
				$11, $12, $13, 'public', 'published',
				$14, $15, $16, $17, $16
			)
			ON CONFLICT DO NOTHING`,
			app.ID, categoryID, name, slugify(app.Name, app.ID), summary, description,
			icon, app.Gradient, jsonBytes(app.TechStack), app.Language,
			framework, app.HasMCP, supportsAPI(app.TechStack), app.Featured,
			app.Stars, createdAt, updatedAt,
		); err != nil {
			return fmt.Errorf("seed app %q: %w", app.Name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit app seed: %w", err)
	}
	return nil
}

// EnsureBootstrapAdmin consumes the environment-provided credential only on
// an empty installation. Once created, later restarts never reset the stored
// username or password from environment values.
func EnsureBootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, username, password string) (uuid.UUID, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin bootstrap admin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	userID, err := ensureBootstrapAdmin(ctx, tx, username, password, auth.HashPassword)
	if err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit bootstrap admin: %w", err)
	}
	return userID, nil
}

type bootstrapTransaction interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ensureBootstrapAdmin(
	ctx context.Context,
	tx bootstrapTransaction,
	username string,
	password string,
	hashPassword func(string) (string, error),
) (uuid.UUID, error) {

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, bootstrapAdvisoryLockID); err != nil {
		return uuid.Nil, fmt.Errorf("lock bootstrap admin initialization: %w", err)
	}

	var userID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM users WHERE subject = $1`, bootstrapSubject).Scan(&userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("read bootstrap admin: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		username = strings.TrimSpace(username)
		if username == "" {
			return uuid.Nil, errors.New("bootstrap username is required on first initialization")
		}
		if len(password) < 12 {
			return uuid.Nil, errors.New("bootstrap password must be at least 12 characters on first initialization")
		}
		hash, hashErr := hashPassword(password)
		if hashErr != nil {
			return uuid.Nil, fmt.Errorf("hash bootstrap password: %w", hashErr)
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO users(subject, username, display_name, auth_source, password_hash, active)
			VALUES ($1, $2, $2, 'bootstrap', $3, true)
			RETURNING id`, bootstrapSubject, username, hash).Scan(&userID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("create bootstrap admin: %w", err)
		}
	}

	result, err := tx.Exec(ctx, `
		INSERT INTO user_roles(user_id, role_id, source)
		SELECT $1, id, 'bootstrap' FROM roles WHERE key = 'super_admin'
		ON CONFLICT (user_id, role_id) DO UPDATE SET source = 'bootstrap'`, userID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("grant bootstrap super_admin: %w", err)
	}
	if result.RowsAffected() != 1 {
		return uuid.Nil, errors.New("super_admin role is missing after migration")
	}

	return userID, nil
}

func decodeSeedApps(data []byte) ([]seedApp, []seedCategory, error) {
	var apps []seedApp
	if err := json.Unmarshal(data, &apps); err != nil {
		return nil, nil, fmt.Errorf("decode app seed JSON: %w", err)
	}
	if len(apps) == 0 {
		return nil, nil, errors.New("app seed is empty")
	}

	categories := make([]seedCategory, 0)
	seenCategories := make(map[string]struct{})
	seenSlugs := make(map[string]string, len(apps))
	seenIDs := make(map[int64]struct{}, len(apps))
	for i := range apps {
		app := &apps[i]
		app.Name = strings.TrimSpace(app.Name)
		app.Category = slugify(app.Category, 0)
		if app.ID == 0 || app.Name == "" || app.Category == "" {
			return nil, nil, fmt.Errorf("seed app at index %d is missing id, name, or category", i)
		}
		if _, exists := seenIDs[app.ID]; exists {
			return nil, nil, fmt.Errorf("duplicate seed app id %d", app.ID)
		}
		seenIDs[app.ID] = struct{}{}
		slug := slugify(app.Name, app.ID)
		if previous, exists := seenSlugs[slug]; exists {
			return nil, nil, fmt.Errorf("seed slug %q is shared by %q and %q", slug, previous, app.Name)
		}
		seenSlugs[slug] = app.Name

		if _, exists := seenCategories[app.Category]; !exists {
			seenCategories[app.Category] = struct{}{}
			categories = append(categories, seedCategory{
				Slug:        app.Category,
				Name:        firstNonEmpty(app.CategoryName, app.Category),
				Icon:        firstNonEmpty(app.CategoryIcon, "📦"),
				Description: "",
				Position:    len(categories),
			})
		}
	}
	return apps, categories, nil
}

func slugify(value string, fallbackID int64) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(value) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
		case b.Len() > 0 && !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" && fallbackID != 0 {
		return fmt.Sprintf("app-%d", fallbackID)
	}
	return result
}

func parseSeedDate(value string, fallback time.Time) time.Time {
	if parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value)); err == nil {
		return parsed.UTC()
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func jsonBytes(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		return []byte("[]")
	}
	return encoded
}

func supportsAPI(stack []string) bool {
	for _, item := range stack {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized == "api" || strings.Contains(normalized, "rest api") || strings.Contains(normalized, "openapi") {
			return true
		}
	}
	return false
}

func detectFramework(stack []string) string {
	known := []string{"React", "Vue", "Angular", "Svelte", "Next.js", "Vite", "TailwindCSS"}
	for _, candidate := range known {
		for _, item := range stack {
			if strings.EqualFold(strings.TrimSpace(item), candidate) {
				return candidate
			}
		}
	}
	return ""
}
