package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

const appColumns = `
	a.id, a.name, a.slug, a.summary, a.description, a.icon, a.gradient,
	a.service_url, a.category_id, a.tags, a.screenshots, a.language,
	a.framework, a.supports_mcp, a.supports_api, a.owner_id,
	COALESCE(NULLIF(u.display_name, ''), u.username, ''), a.team,
	a.app_version, a.visibility, a.status, a.featured, a.trending_score,
	a.created_at, a.updated_at, a.published_at,
	c.id, c.slug, c.name, c.icon, c.description, c.position, c.active`

const appFrom = `
	FROM apps a
	JOIN categories c ON c.id = a.category_id
	LEFT JOIN users u ON u.id = a.owner_id`

func scanApp(row rowScanner) (model.App, error) {
	var app model.App
	var category model.Category
	var tagsJSON, screenshotsJSON []byte
	err := row.Scan(
		&app.ID, &app.Name, &app.Slug, &app.Summary, &app.Description,
		&app.Icon, &app.Gradient, &app.ServiceURL, &app.CategoryID,
		&tagsJSON, &screenshotsJSON, &app.Language, &app.Framework,
		&app.SupportsMCP, &app.SupportsAPI, &app.OwnerID, &app.OwnerName,
		&app.Team, &app.Version, &app.Visibility, &app.Status, &app.Featured,
		&app.TrendingScore, &app.CreatedAt, &app.UpdatedAt, &app.PublishedAt,
		&category.ID, &category.Slug, &category.Name, &category.Icon,
		&category.Description, &category.Position, &category.Active,
	)
	if err != nil {
		return model.App{}, err
	}
	if err := json.Unmarshal(tagsJSON, &app.Tags); err != nil {
		return model.App{}, fmt.Errorf("decode app tags: %w", err)
	}
	if err := json.Unmarshal(screenshotsJSON, &app.Screenshots); err != nil {
		return model.App{}, fmt.Errorf("decode app screenshots: %w", err)
	}
	if app.Tags == nil {
		app.Tags = []string{}
	}
	if app.Screenshots == nil {
		app.Screenshots = []string{}
	}
	app.Category = &category
	return app, nil
}

func scanAppWithTotal(row rowScanner) (model.App, int, error) {
	var app model.App
	var category model.Category
	var tagsJSON, screenshotsJSON []byte
	var total int
	err := row.Scan(
		&total,
		&app.ID, &app.Name, &app.Slug, &app.Summary, &app.Description,
		&app.Icon, &app.Gradient, &app.ServiceURL, &app.CategoryID,
		&tagsJSON, &screenshotsJSON, &app.Language, &app.Framework,
		&app.SupportsMCP, &app.SupportsAPI, &app.OwnerID, &app.OwnerName,
		&app.Team, &app.Version, &app.Visibility, &app.Status, &app.Featured,
		&app.TrendingScore, &app.CreatedAt, &app.UpdatedAt, &app.PublishedAt,
		&category.ID, &category.Slug, &category.Name, &category.Icon,
		&category.Description, &category.Position, &category.Active,
	)
	if err != nil {
		return model.App{}, 0, err
	}
	if err := json.Unmarshal(tagsJSON, &app.Tags); err != nil {
		return model.App{}, 0, fmt.Errorf("decode app tags: %w", err)
	}
	if err := json.Unmarshal(screenshotsJSON, &app.Screenshots); err != nil {
		return model.App{}, 0, fmt.Errorf("decode app screenshots: %w", err)
	}
	if app.Tags == nil {
		app.Tags = []string{}
	}
	if app.Screenshots == nil {
		app.Screenshots = []string{}
	}
	app.Category = &category
	return app, total, nil
}

func (r *Repository) ListApps(ctx context.Context, options model.AppListOptions) (model.Page[model.App], error) {
	limit, offset := normalizePage(options.Limit, options.Offset, 24)
	where := []string{"true"}
	args := make([]any, 0, 10)
	add := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	if !options.IncludeAll {
		where = append(where, `a.status = 'published'`, `a.visibility = 'public'`, `c.active`)
	} else if status := strings.TrimSpace(options.Status); status != "" {
		where = append(where, `a.status = `+add(status))
	}
	if query := strings.TrimSpace(options.Query); query != "" {
		parameter := add(query)
		pattern := add(likePattern(query))
		where = append(where, `(to_tsvector('simple', a.name || ' ' || a.slug || ' ' || a.summary || ' ' || a.description || ' ' || a.tags::text) @@ plainto_tsquery('simple', `+parameter+`)
			OR a.name ILIKE `+pattern+` ESCAPE '\'
			OR a.slug ILIKE `+pattern+` ESCAPE '\'
			OR a.summary ILIKE `+pattern+` ESCAPE '\'
			OR a.description ILIKE `+pattern+` ESCAPE '\'
			OR a.tags::text ILIKE `+pattern+` ESCAPE '\')`)
	}
	if category := strings.TrimSpace(options.Category); category != "" {
		where = append(where, `c.slug = `+add(category))
	}
	if language := strings.TrimSpace(options.Language); language != "" {
		where = append(where, `lower(a.language) = lower(`+add(language)+`)`)
	}
	if options.OwnerID != nil {
		where = append(where, `a.owner_id = `+add(*options.OwnerID))
	}
	if options.MCPOnly {
		where = append(where, `a.supports_mcp`)
	}
	if options.Featured {
		where = append(where, `a.featured`)
	}

	order := `a.updated_at DESC, a.id`
	switch options.Sort {
	case "name":
		order = `lower(a.name), a.id`
	case "created":
		order = `a.created_at DESC, a.id`
	case "trending":
		order = `a.trending_score DESC, a.updated_at DESC, a.id`
	case "published":
		order = `a.published_at DESC NULLS LAST, a.id`
	}
	args = append(args, limit, offset)
	query := `SELECT count(*) OVER(), ` + appColumns + appFrom +
		` WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY %s LIMIT $%d OFFSET $%d`, order, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return model.Page[model.App]{}, normalizeError("list apps", err)
	}
	defer rows.Close()

	page := model.Page[model.App]{Items: []model.App{}, Limit: limit, Offset: offset}
	for rows.Next() {
		app, total, err := scanAppWithTotal(rows)
		if err != nil {
			return model.Page[model.App]{}, normalizeError("scan app list", err)
		}
		page.Total = total
		page.Items = append(page.Items, app)
	}
	if err := rows.Err(); err != nil {
		return model.Page[model.App]{}, normalizeError("iterate apps", err)
	}
	return page, nil
}

func (r *Repository) GetAppByID(ctx context.Context, id uuid.UUID) (model.App, error) {
	app, err := scanApp(r.pool.QueryRow(ctx, `SELECT `+appColumns+appFrom+` WHERE a.id = $1`, id))
	return app, normalizeError("get app by id", err)
}

func (r *Repository) GetAppBySlug(ctx context.Context, slug string, includeAll bool) (model.App, error) {
	where := ` WHERE a.slug = $1`
	if !includeAll {
		where += ` AND a.status = 'published' AND a.visibility = 'public' AND c.active`
	}
	app, err := scanApp(r.pool.QueryRow(ctx, `SELECT `+appColumns+appFrom+where, strings.TrimSpace(slug)))
	return app, normalizeError("get app by slug", err)
}

func validateAppInput(input model.AppInput) (model.AppInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = normalizeKey(input.Slug)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Description = strings.TrimSpace(input.Description)
	input.Icon = strings.TrimSpace(input.Icon)
	input.Gradient = strings.TrimSpace(input.Gradient)
	input.ServiceURL = strings.TrimSpace(input.ServiceURL)
	input.CategoryID = strings.TrimSpace(input.CategoryID)
	input.Tags = uniqueStrings(input.Tags)
	input.Screenshots = uniqueStrings(input.Screenshots)
	input.Language = strings.TrimSpace(input.Language)
	input.Framework = strings.TrimSpace(input.Framework)
	input.Team = strings.TrimSpace(input.Team)
	input.Version = strings.TrimSpace(input.Version)
	input.Visibility = normalizeKey(input.Visibility)
	if input.Visibility == "" {
		input.Visibility = "public"
	}
	if input.Name == "" || input.Slug == "" || input.Summary == "" || input.Description == "" || input.CategoryID == "" {
		return model.AppInput{}, fmt.Errorf("required app fields: %w", ErrInvalid)
	}
	if input.Visibility != "public" && input.Visibility != "private" {
		return model.AppInput{}, fmt.Errorf("app visibility: %w", ErrInvalid)
	}
	if input.Icon == "" {
		input.Icon = "📦"
	}
	return input, nil
}

func validAppStatus(status string) bool {
	switch status {
	case model.AppStatusDraft, model.AppStatusPending, model.AppStatusPublished, model.AppStatusRejected, "archived":
		return true
	default:
		return false
	}
}

func (r *Repository) CreateApp(ctx context.Context, ownerID *uuid.UUID, input model.AppInput, status string) (model.App, error) {
	input, err := validateAppInput(input)
	if err != nil {
		return model.App{}, err
	}
	categoryID, err := uuid.Parse(input.CategoryID)
	if err != nil || !validAppStatus(status) {
		return model.App{}, fmt.Errorf("create app category or status: %w", ErrInvalid)
	}
	var id uuid.UUID
	err = r.pool.QueryRow(ctx, `
		INSERT INTO apps(
			owner_id, category_id, name, slug, summary, description, icon, gradient,
			service_url, tags, screenshots, language, framework, supports_mcp,
			supports_api, team, app_version, visibility, status, published_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14,
			$15, $16, $17, $18, $19,
			CASE WHEN $19 = 'published' THEN now() ELSE NULL END
		) RETURNING id`,
		ownerID, categoryID, input.Name, input.Slug, input.Summary,
		input.Description, input.Icon, input.Gradient, input.ServiceURL,
		jsonValue(input.Tags), jsonValue(input.Screenshots), input.Language,
		input.Framework, input.SupportsMCP, input.SupportsAPI, input.Team,
		input.Version, input.Visibility, status,
	).Scan(&id)
	if err != nil {
		return model.App{}, normalizeError("create app", err)
	}
	return r.GetAppByID(ctx, id)
}

func (r *Repository) UpdateApp(ctx context.Context, id uuid.UUID, input model.AppInput) (model.App, error) {
	input, err := validateAppInput(input)
	if err != nil {
		return model.App{}, err
	}
	categoryID, err := uuid.Parse(input.CategoryID)
	if err != nil {
		return model.App{}, fmt.Errorf("update app category: %w", ErrInvalid)
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE apps SET category_id = $2, name = $3, slug = $4, summary = $5,
			description = $6, icon = $7, gradient = $8, service_url = $9,
			tags = $10, screenshots = $11, language = $12, framework = $13,
			supports_mcp = $14, supports_api = $15, team = $16,
			app_version = $17, visibility = $18, updated_at = now()
		WHERE id = $1`, id, categoryID, input.Name, input.Slug, input.Summary,
		input.Description, input.Icon, input.Gradient, input.ServiceURL,
		jsonValue(input.Tags), jsonValue(input.Screenshots), input.Language,
		input.Framework, input.SupportsMCP, input.SupportsAPI, input.Team,
		input.Version, input.Visibility)
	if err != nil {
		return model.App{}, normalizeError("update app", err)
	}
	if result.RowsAffected() == 0 {
		return model.App{}, fmt.Errorf("update app: %w", ErrNotFound)
	}
	return r.GetAppByID(ctx, id)
}

// AdminUpdateApp writes the whole catalog record in one statement, including
// the status and featured flags that owners cannot change themselves.
func (r *Repository) AdminUpdateApp(ctx context.Context, id uuid.UUID, input model.AppInput, status string, featured bool) (model.App, error) {
	input, err := validateAppInput(input)
	if err != nil {
		return model.App{}, err
	}
	categoryID, err := uuid.Parse(input.CategoryID)
	if err != nil {
		return model.App{}, fmt.Errorf("update app category: %w", ErrInvalid)
	}
	if !validAppStatus(status) {
		return model.App{}, fmt.Errorf("update app status: %w", ErrInvalid)
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE apps SET category_id = $2, name = $3, slug = $4, summary = $5,
			description = $6, icon = $7, gradient = $8, service_url = $9,
			tags = $10, screenshots = $11, language = $12, framework = $13,
			supports_mcp = $14, supports_api = $15, team = $16,
			app_version = $17, visibility = $18, status = $19, featured = $20,
			updated_at = now(),
			published_at = CASE WHEN $19 = 'published' THEN COALESCE(published_at, now()) ELSE published_at END
		WHERE id = $1`, id, categoryID, input.Name, input.Slug, input.Summary,
		input.Description, input.Icon, input.Gradient, input.ServiceURL,
		jsonValue(input.Tags), jsonValue(input.Screenshots), input.Language,
		input.Framework, input.SupportsMCP, input.SupportsAPI, input.Team,
		input.Version, input.Visibility, status, featured)
	if err != nil {
		return model.App{}, normalizeError("admin update app", err)
	}
	if result.RowsAffected() == 0 {
		return model.App{}, fmt.Errorf("admin update app: %w", ErrNotFound)
	}
	return r.GetAppByID(ctx, id)
}

func (r *Repository) DeleteApp(ctx context.Context, id uuid.UUID) error {
	result, err := r.pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, id)
	if err != nil {
		return normalizeError("delete app", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("delete app: %w", ErrNotFound)
	}
	return nil
}

func (r *Repository) SetAppStatus(ctx context.Context, id uuid.UUID, status string) (model.App, error) {
	if !validAppStatus(status) {
		return model.App{}, fmt.Errorf("set app status: %w", ErrInvalid)
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE apps SET status = $2, updated_at = now(),
			published_at = CASE WHEN $2 = 'published' THEN COALESCE(published_at, now()) ELSE published_at END
		WHERE id = $1`, id, status)
	if err != nil {
		return model.App{}, normalizeError("set app status", err)
	}
	if result.RowsAffected() == 0 {
		return model.App{}, fmt.Errorf("set app status: %w", ErrNotFound)
	}
	return r.GetAppByID(ctx, id)
}

type CategoryInput struct {
	Slug        string
	Name        string
	Icon        string
	Description string
	Position    int
	Active      bool
}

func (r *Repository) ListCategories(ctx context.Context, includeInactive bool) ([]model.Category, error) {
	where := ""
	if !includeInactive {
		where = ` WHERE c.active`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.slug, c.name, c.icon, c.description, c.position, c.active,
			count(a.id) FILTER (WHERE a.status = 'published' AND a.visibility = 'public')::int
		FROM categories c LEFT JOIN apps a ON a.category_id = c.id`+where+`
		GROUP BY c.id ORDER BY c.position, c.name, c.id`)
	if err != nil {
		return nil, normalizeError("list categories", err)
	}
	defer rows.Close()
	result := []model.Category{}
	for rows.Next() {
		var category model.Category
		if err := rows.Scan(&category.ID, &category.Slug, &category.Name, &category.Icon,
			&category.Description, &category.Position, &category.Active, &category.AppCount); err != nil {
			return nil, normalizeError("scan category", err)
		}
		result = append(result, category)
	}
	return result, normalizeError("iterate categories", rows.Err())
}

func (r *Repository) GetCategoryByID(ctx context.Context, id uuid.UUID) (model.Category, error) {
	var category model.Category
	err := r.pool.QueryRow(ctx, `
		SELECT c.id, c.slug, c.name, c.icon, c.description, c.position, c.active,
			count(a.id) FILTER (WHERE a.status = 'published' AND a.visibility = 'public')::int
		FROM categories c LEFT JOIN apps a ON a.category_id = c.id
		WHERE c.id = $1 GROUP BY c.id`, id,
	).Scan(&category.ID, &category.Slug, &category.Name, &category.Icon,
		&category.Description, &category.Position, &category.Active, &category.AppCount)
	return category, normalizeError("get category", err)
}

func validateCategory(input CategoryInput) (CategoryInput, error) {
	input.Slug = normalizeKey(input.Slug)
	input.Name = strings.TrimSpace(input.Name)
	input.Icon = strings.TrimSpace(input.Icon)
	input.Description = strings.TrimSpace(input.Description)
	if input.Slug == "" || input.Name == "" {
		return CategoryInput{}, fmt.Errorf("category slug or name: %w", ErrInvalid)
	}
	if input.Icon == "" {
		input.Icon = "📦"
	}
	return input, nil
}

func (r *Repository) CreateCategory(ctx context.Context, input CategoryInput) (model.Category, error) {
	input, err := validateCategory(input)
	if err != nil {
		return model.Category{}, err
	}
	var id uuid.UUID
	err = r.pool.QueryRow(ctx, `
		INSERT INTO categories(slug, name, icon, description, position, active)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, input.Slug,
		input.Name, input.Icon, input.Description, input.Position, input.Active).Scan(&id)
	if err != nil {
		return model.Category{}, normalizeError("create category", err)
	}
	return r.GetCategoryByID(ctx, id)
}

func (r *Repository) UpdateCategory(ctx context.Context, id uuid.UUID, input CategoryInput) (model.Category, error) {
	input, err := validateCategory(input)
	if err != nil {
		return model.Category{}, err
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE categories SET slug = $2, name = $3, icon = $4, description = $5,
			position = $6, active = $7, updated_at = now() WHERE id = $1`, id,
		input.Slug, input.Name, input.Icon, input.Description, input.Position, input.Active)
	if err != nil {
		return model.Category{}, normalizeError("update category", err)
	}
	if result.RowsAffected() == 0 {
		return model.Category{}, fmt.Errorf("update category: %w", ErrNotFound)
	}
	return r.GetCategoryByID(ctx, id)
}

func (r *Repository) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	result, err := r.pool.Exec(ctx, `DELETE FROM categories WHERE id = $1`, id)
	if err != nil {
		return normalizeError("delete category", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("delete category: %w", ErrNotFound)
	}
	return nil
}

func (r *Repository) ListFavorites(ctx context.Context, userID uuid.UUID, limit, offset int) (model.Page[model.App], error) {
	limit, offset = normalizePage(limit, offset, 24)
	rows, err := r.pool.Query(ctx, `
		SELECT count(*) OVER(), `+appColumns+appFrom+`
		JOIN favorites f ON f.app_id = a.id
		WHERE f.user_id = $1 AND a.status = 'published' AND a.visibility = 'public'
		ORDER BY f.created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return model.Page[model.App]{}, normalizeError("list favorites", err)
	}
	defer rows.Close()
	page := model.Page[model.App]{Items: []model.App{}, Limit: limit, Offset: offset}
	for rows.Next() {
		app, total, err := scanAppWithTotal(rows)
		if err != nil {
			return model.Page[model.App]{}, normalizeError("scan favorite", err)
		}
		page.Total = total
		page.Items = append(page.Items, app)
	}
	return page, normalizeError("iterate favorites", rows.Err())
}

func (r *Repository) AddFavorite(ctx context.Context, userID, appID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO favorites(user_id, app_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, userID, appID)
	return normalizeError("add favorite", err)
}

func (r *Repository) RemoveFavorite(ctx context.Context, userID, appID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM favorites WHERE user_id = $1 AND app_id = $2`, userID, appID)
	return normalizeError("remove favorite", err)
}

func jsonValue(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		return []byte("[]")
	}
	return encoded
}
