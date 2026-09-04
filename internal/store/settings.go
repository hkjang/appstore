package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

func (r *Repository) GetSetting(ctx context.Context, key string, destination any) error {
	var value []byte
	err := r.pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, normalizeKey(key)).Scan(&value)
	if err != nil {
		return normalizeError("get setting", err)
	}
	if err := json.Unmarshal(value, destination); err != nil {
		return fmt.Errorf("decode setting %q: %w", key, err)
	}
	return nil
}

func (r *Repository) PutSetting(ctx context.Context, key string, value any, updatedBy *uuid.UUID) error {
	key = normalizeKey(key)
	if key == "" || key == "encryption_key_fingerprint" {
		return fmt.Errorf("setting key: %w", ErrInvalid)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode setting %q: %w", key, err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO system_settings(key, value, updated_by, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value,
			updated_by = EXCLUDED.updated_by, updated_at = now()`, key, encoded, updatedBy)
	return normalizeError("put setting", err)
}

func (r *Repository) GetSystemSettings(ctx context.Context) (model.SystemSettings, error) {
	var settings model.SystemSettings
	err := r.GetSetting(ctx, "system", &settings)
	return settings, err
}

// validateSystemSettings trims and range-checks the settings an administrator
// submits. It is pure so the rules can be tested without a database.
func validateSystemSettings(settings model.SystemSettings) (model.SystemSettings, error) {
	settings.SiteName = strings.TrimSpace(settings.SiteName)
	settings.SiteURL = strings.TrimSpace(settings.SiteURL)
	settings.Theme = normalizeKey(settings.Theme)
	settings.DefaultLanguage = strings.TrimSpace(settings.DefaultLanguage)
	if settings.SiteName == "" || settings.PageSize < 1 || settings.PageSize > 200 {
		return model.SystemSettings{}, fmt.Errorf("system settings: %w", ErrInvalid)
	}
	if settings.Theme != "system" && settings.Theme != "dark" && settings.Theme != "light" {
		return model.SystemSettings{}, fmt.Errorf("system theme: %w", ErrInvalid)
	}
	home, err := validateHomeCopy(settings.HomeCopy)
	if err != nil {
		return model.SystemSettings{}, err
	}
	settings.HomeCopy = home
	return settings, nil
}

// validateHomeCopy trims the banner wording and caps its length. The banner is
// a fixed slab of the landing page, so wording far past these lengths would
// break the layout rather than say more. An empty field is left empty on
// purpose: it means "show the shipped default".
func validateHomeCopy(home model.HomeCopy) (model.HomeCopy, error) {
	fields := []struct {
		name  string
		value *string
		max   int
	}{
		{"hero eyebrow", &home.HeroEyebrow, 120},
		{"hero title", &home.HeroTitle, 200},
		{"site description", &home.SiteDescription, 600},
		{"hero primary label", &home.HeroPrimaryLabel, 40},
		{"hero secondary label", &home.HeroSecondaryLabel, 40},
	}
	for _, field := range fields {
		*field.value = strings.TrimSpace(*field.value)
		if len([]rune(*field.value)) > field.max {
			return model.HomeCopy{}, fmt.Errorf("%s: %w", field.name, ErrInvalid)
		}
	}
	return home, nil
}

func (r *Repository) UpdateSystemSettings(ctx context.Context, settings model.SystemSettings, updatedBy *uuid.UUID) (model.SystemSettings, error) {
	settings, err := validateSystemSettings(settings)
	if err != nil {
		return model.SystemSettings{}, err
	}
	if err := r.PutSetting(ctx, "system", settings, updatedBy); err != nil {
		return model.SystemSettings{}, err
	}
	return r.GetSystemSettings(ctx)
}

func (r *Repository) GetAPISettings(ctx context.Context) (model.APISettings, error) {
	var settings model.APISettings
	err := r.GetSetting(ctx, "api", &settings)
	return settings, err
}

func (r *Repository) UpdateAPISettings(ctx context.Context, settings model.APISettings, updatedBy *uuid.UUID) (model.APISettings, error) {
	if settings.RateLimitPerMinute < 1 || settings.RateLimitPerMinute > 1_000_000 {
		return model.APISettings{}, fmt.Errorf("API settings: %w", ErrInvalid)
	}
	if err := r.PutSetting(ctx, "api", settings, updatedBy); err != nil {
		return model.APISettings{}, err
	}
	return r.GetAPISettings(ctx)
}

func (r *Repository) GetMCPSettings(ctx context.Context) (model.MCPSettings, error) {
	var settings model.MCPSettings
	err := r.GetSetting(ctx, "mcp", &settings)
	return settings, err
}

func (r *Repository) UpdateMCPSettings(ctx context.Context, settings model.MCPSettings, updatedBy *uuid.UUID) (model.MCPSettings, error) {
	settings.ProtocolVersion = strings.TrimSpace(settings.ProtocolVersion)
	if settings.RateLimitPerMinute < 1 || settings.RateLimitPerMinute > 1_000_000 || settings.ProtocolVersion == "" {
		return model.MCPSettings{}, fmt.Errorf("MCP settings: %w", ErrInvalid)
	}
	if err := r.PutSetting(ctx, "mcp", settings, updatedBy); err != nil {
		return model.MCPSettings{}, err
	}
	return r.GetMCPSettings(ctx)
}

// GetOIDCSettings returns the encrypted secret in ClientSecret for internal
// OIDC consumers. HTTP handlers must clear ClientSecret before serialization;
// ClientSecretSet is the only secret state intended for admin responses.
func (r *Repository) GetOIDCSettings(ctx context.Context) (model.OIDCSettings, error) {
	var settings model.OIDCSettings
	var roleJSON, groupJSON, scopesJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT enabled, issuer_url, client_id, client_secret_encrypted,
			role_claim_path, group_claim_path, role_mappings, group_mappings,
			scopes, updated_at FROM oidc_settings WHERE singleton`).Scan(
		&settings.Enabled, &settings.IssuerURL, &settings.ClientID, &settings.ClientSecret,
		&settings.RoleClaimPath, &settings.GroupClaimPath, &roleJSON, &groupJSON,
		&scopesJSON, &settings.UpdatedAt)
	if err != nil {
		return model.OIDCSettings{}, normalizeError("get OIDC settings", err)
	}
	settings.ClientSecretSet = settings.ClientSecret != ""
	if err := json.Unmarshal(roleJSON, &settings.RoleMappings); err != nil {
		return model.OIDCSettings{}, fmt.Errorf("decode OIDC role mappings: %w", err)
	}
	if err := json.Unmarshal(groupJSON, &settings.GroupMappings); err != nil {
		return model.OIDCSettings{}, fmt.Errorf("decode OIDC group mappings: %w", err)
	}
	if err := json.Unmarshal(scopesJSON, &settings.Scopes); err != nil {
		return model.OIDCSettings{}, fmt.Errorf("decode OIDC scopes: %w", err)
	}
	return settings, nil
}

func (r *Repository) UpdateOIDCSettings(ctx context.Context, settings model.OIDCSettings, encryptedSecret *string, updatedBy *uuid.UUID) (model.OIDCSettings, error) {
	settings.IssuerURL = strings.TrimRight(strings.TrimSpace(settings.IssuerURL), "/")
	settings.ClientID = strings.TrimSpace(settings.ClientID)
	settings.RoleClaimPath = strings.TrimSpace(settings.RoleClaimPath)
	settings.GroupClaimPath = strings.TrimSpace(settings.GroupClaimPath)
	settings.Scopes = uniqueStrings(settings.Scopes)
	if settings.Enabled && (settings.IssuerURL == "" || settings.ClientID == "") {
		return model.OIDCSettings{}, fmt.Errorf("enabled OIDC settings: %w", ErrInvalid)
	}
	if settings.RoleMappings == nil {
		settings.RoleMappings = map[string][]string{}
	}
	if settings.GroupMappings == nil {
		settings.GroupMappings = map[string][]string{}
	}
	if len(settings.Scopes) == 0 {
		settings.Scopes = []string{"openid", "profile", "email"}
	}

	secretExpression := `client_secret_encrypted`
	args := []any{settings.Enabled, settings.IssuerURL, settings.ClientID}
	if encryptedSecret != nil {
		args = append(args, *encryptedSecret)
		secretExpression = fmt.Sprintf("$%d", len(args))
	}
	args = append(args, settings.RoleClaimPath, settings.GroupClaimPath,
		jsonValue(settings.RoleMappings), jsonValue(settings.GroupMappings),
		jsonValue(settings.Scopes), updatedBy)
	base := len(args) - 5
	query := fmt.Sprintf(`
		UPDATE oidc_settings SET enabled = $1, issuer_url = $2, client_id = $3,
			client_secret_encrypted = %s, role_claim_path = $%d, group_claim_path = $%d,
			role_mappings = $%d, group_mappings = $%d, scopes = $%d,
			updated_by = $%d, updated_at = now() WHERE singleton`,
		secretExpression, base, base+1, base+2, base+3, base+4, base+5)
	if _, err := r.pool.Exec(ctx, query, args...); err != nil {
		return model.OIDCSettings{}, normalizeError("update OIDC settings", err)
	}
	return r.GetOIDCSettings(ctx)
}

func validateAIProvider(provider model.AIProvider) (model.AIProvider, error) {
	provider.Name = strings.TrimSpace(provider.Name)
	provider.Kind = normalizeKey(provider.Kind)
	provider.BaseURL = strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	provider.DefaultModel = strings.TrimSpace(provider.DefaultModel)
	if provider.Name == "" || provider.Kind == "" || provider.BaseURL == "" || provider.DefaultModel == "" ||
		provider.ContextWindow < 0 || provider.ContextWindow > 262144 ||
		provider.MaxInputTokens < 0 || provider.MaxInputTokens > 262144 ||
		provider.MaxOutputTokens < 0 || provider.MaxOutputTokens > 262144 ||
		provider.Temperature < 0 || provider.Temperature > 2 ||
		provider.TimeoutSeconds < 1 || provider.TimeoutSeconds > 3600 ||
		provider.Retries < 0 || provider.Retries > 10 {
		return model.AIProvider{}, fmt.Errorf("AI provider: %w", ErrInvalid)
	}
	if provider.ContextWindow > 0 && provider.MaxInputTokens+provider.MaxOutputTokens > provider.ContextWindow {
		return model.AIProvider{}, fmt.Errorf("AI provider token envelope: %w", ErrInvalid)
	}
	return provider, nil
}

const aiProviderColumns = `
	id, name, kind, base_url, api_key_encrypted, default_model, context_window,
	max_input_tokens, max_output_tokens, temperature, timeout_seconds, retries,
	streaming, enabled, created_at, updated_at`

func scanAIProvider(row rowScanner) (model.AIProvider, error) {
	var provider model.AIProvider
	err := row.Scan(&provider.ID, &provider.Name, &provider.Kind, &provider.BaseURL,
		&provider.APIKey, &provider.DefaultModel, &provider.ContextWindow,
		&provider.MaxInputTokens, &provider.MaxOutputTokens, &provider.Temperature,
		&provider.TimeoutSeconds, &provider.Retries, &provider.Streaming,
		&provider.Enabled, &provider.CreatedAt, &provider.UpdatedAt)
	provider.APIKeySet = provider.APIKey != ""
	return provider, err
}

func (r *Repository) ListAIProviders(ctx context.Context) ([]model.AIProvider, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+aiProviderColumns+` FROM ai_providers ORDER BY name, id`)
	if err != nil {
		return nil, normalizeError("list AI providers", err)
	}
	defer rows.Close()
	result := []model.AIProvider{}
	for rows.Next() {
		provider, err := scanAIProvider(rows)
		if err != nil {
			return nil, normalizeError("scan AI provider", err)
		}
		provider.APIKey = ""
		result = append(result, provider)
	}
	return result, normalizeError("iterate AI providers", rows.Err())
}

// GetAIProvider returns the encrypted API key for internal provider clients.
func (r *Repository) GetAIProvider(ctx context.Context, id uuid.UUID) (model.AIProvider, error) {
	provider, err := scanAIProvider(r.pool.QueryRow(ctx, `SELECT `+aiProviderColumns+` FROM ai_providers WHERE id = $1`, id))
	return provider, normalizeError("get AI provider", err)
}

func (r *Repository) CreateAIProvider(ctx context.Context, provider model.AIProvider, encryptedAPIKey string) (model.AIProvider, error) {
	provider, err := validateAIProvider(provider)
	if err != nil {
		return model.AIProvider{}, err
	}
	var id uuid.UUID
	err = r.pool.QueryRow(ctx, `
		INSERT INTO ai_providers(name, kind, base_url, api_key_encrypted, default_model,
			context_window, max_input_tokens, max_output_tokens, temperature,
			timeout_seconds, retries, streaming, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id`, provider.Name, provider.Kind, provider.BaseURL, encryptedAPIKey,
		provider.DefaultModel, provider.ContextWindow, provider.MaxInputTokens,
		provider.MaxOutputTokens, provider.Temperature, provider.TimeoutSeconds,
		provider.Retries, provider.Streaming, provider.Enabled).Scan(&id)
	if err != nil {
		return model.AIProvider{}, normalizeError("create AI provider", err)
	}
	return r.GetAIProvider(ctx, id)
}

func (r *Repository) UpdateAIProvider(ctx context.Context, provider model.AIProvider, encryptedAPIKey *string) (model.AIProvider, error) {
	provider, err := validateAIProvider(provider)
	if err != nil {
		return model.AIProvider{}, err
	}
	secretExpression := `api_key_encrypted`
	args := []any{provider.ID, provider.Name, provider.Kind, provider.BaseURL}
	if encryptedAPIKey != nil {
		args = append(args, *encryptedAPIKey)
		secretExpression = fmt.Sprintf("$%d", len(args))
	}
	args = append(args, provider.DefaultModel, provider.ContextWindow, provider.MaxInputTokens,
		provider.MaxOutputTokens, provider.Temperature, provider.TimeoutSeconds,
		provider.Retries, provider.Streaming, provider.Enabled)
	base := len(args) - 8
	query := fmt.Sprintf(`
		UPDATE ai_providers SET name = $2, kind = $3, base_url = $4,
			api_key_encrypted = %s, default_model = $%d, context_window = $%d,
			max_input_tokens = $%d, max_output_tokens = $%d, temperature = $%d,
			timeout_seconds = $%d, retries = $%d, streaming = $%d, enabled = $%d,
			updated_at = now() WHERE id = $1`, secretExpression, base, base+1, base+2,
		base+3, base+4, base+5, base+6, base+7, base+8)
	result, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return model.AIProvider{}, normalizeError("update AI provider", err)
	}
	if result.RowsAffected() == 0 {
		return model.AIProvider{}, fmt.Errorf("update AI provider: %w", ErrNotFound)
	}
	return r.GetAIProvider(ctx, provider.ID)
}

func (r *Repository) DeleteAIProvider(ctx context.Context, id uuid.UUID) error {
	result, err := r.pool.Exec(ctx, `DELETE FROM ai_providers WHERE id = $1`, id)
	if err != nil {
		return normalizeError("delete AI provider", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("delete AI provider: %w", ErrNotFound)
	}
	return nil
}

func (r *Repository) ListAIModels(ctx context.Context, providerID uuid.UUID) ([]model.AIModel, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, provider_id, name, context_window, max_input_tokens,
			max_output_tokens, enabled, created_at, updated_at
		FROM ai_models WHERE provider_id = $1 ORDER BY name, id`, providerID)
	if err != nil {
		return nil, normalizeError("list AI models", err)
	}
	defer rows.Close()
	result := []model.AIModel{}
	for rows.Next() {
		var item model.AIModel
		if err := rows.Scan(&item.ID, &item.ProviderID, &item.Name, &item.ContextWindow,
			&item.MaxInputTokens, &item.MaxOutputTokens, &item.Enabled,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, normalizeError("scan AI model", err)
		}
		result = append(result, item)
	}
	return result, normalizeError("iterate AI models", rows.Err())
}

func (r *Repository) UpsertAIModel(ctx context.Context, item model.AIModel) (model.AIModel, error) {
	item.Name = strings.TrimSpace(item.Name)
	if item.ProviderID == uuid.Nil || item.Name == "" || item.ContextWindow < 0 || item.ContextWindow > 262144 ||
		item.MaxInputTokens < 0 || item.MaxInputTokens > 262144 ||
		item.MaxOutputTokens < 0 || item.MaxOutputTokens > 262144 {
		return model.AIModel{}, fmt.Errorf("AI model: %w", ErrInvalid)
	}
	if item.ContextWindow > 0 && item.MaxInputTokens+item.MaxOutputTokens > item.ContextWindow {
		return model.AIModel{}, fmt.Errorf("AI model token envelope: %w", ErrInvalid)
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO ai_models(provider_id, name, context_window, max_input_tokens, max_output_tokens, enabled)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (provider_id, name) DO UPDATE SET context_window = EXCLUDED.context_window,
			max_input_tokens = EXCLUDED.max_input_tokens,
			max_output_tokens = EXCLUDED.max_output_tokens,
			enabled = EXCLUDED.enabled, updated_at = now()
		RETURNING id, created_at, updated_at`, item.ProviderID, item.Name, item.ContextWindow,
		item.MaxInputTokens, item.MaxOutputTokens, item.Enabled).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	return item, normalizeError("upsert AI model", err)
}

func (r *Repository) DeleteAIModel(ctx context.Context, id uuid.UUID) error {
	result, err := r.pool.Exec(ctx, `DELETE FROM ai_models WHERE id = $1`, id)
	if err != nil {
		return normalizeError("delete AI model", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("delete AI model: %w", ErrNotFound)
	}
	return nil
}
