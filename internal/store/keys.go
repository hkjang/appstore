package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/jackc/pgx/v5"
)

type CreateAPIKeyParams struct {
	UserID      uuid.UUID
	Name        string
	Prefix      string
	Hash        []byte
	Permissions []string
	ExpiresAt   *time.Time
	RotatedFrom *uuid.UUID
}

type APIKeyCredential struct {
	Key         model.APIKey
	UserID      uuid.UUID
	UserActive  bool
	Hash        []byte
	Permissions []string
}

type APIKeyRotationResult struct {
	OldKey          model.APIKey `json:"oldKey"`
	NewKey          model.APIKey `json:"newKey"`
	GracePeriodEnds time.Time    `json:"gracePeriodEnds"`
}

func scanAPIKey(row rowScanner) (model.APIKey, error) {
	var key model.APIKey
	var permissionsJSON []byte
	err := row.Scan(&key.ID, &key.Name, &key.Prefix, &permissionsJSON, &key.CreatedAt,
		&key.ExpiresAt, &key.LastUsedAt, &key.RevokedAt, &key.RotatedFrom)
	if err != nil {
		return model.APIKey{}, err
	}
	if err := json.Unmarshal(permissionsJSON, &key.Permissions); err != nil {
		return model.APIKey{}, fmt.Errorf("decode API key permissions: %w", err)
	}
	if key.Permissions == nil {
		key.Permissions = []string{}
	}
	return key, nil
}

const apiKeyColumns = `id, name, key_prefix, permissions, created_at, expires_at, last_used_at, revoked_at, rotated_from`

func (r *Repository) ListAPIKeys(ctx context.Context, userID uuid.UUID, includeRevoked bool) ([]model.APIKey, error) {
	query := `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE user_id = $1`
	if !includeRevoked {
		query += ` AND revoked_at IS NULL`
	}
	query += ` ORDER BY created_at DESC, id`
	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, normalizeError("list API keys", err)
	}
	defer rows.Close()
	result := []model.APIKey{}
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, normalizeError("scan API key", err)
		}
		result = append(result, key)
	}
	return result, normalizeError("iterate API keys", rows.Err())
}

func (r *Repository) GetAPIKey(ctx context.Context, userID, keyID uuid.UUID) (model.APIKey, error) {
	key, err := scanAPIKey(r.pool.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE id = $1 AND user_id = $2`, keyID, userID))
	return key, normalizeError("get API key", err)
}

func (r *Repository) GetAPIKeyCredentialByHash(ctx context.Context, hash []byte) (APIKeyCredential, error) {
	var credential APIKeyCredential
	var permissionsJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT k.id, k.name, k.key_prefix,
			COALESCE((
				SELECT jsonb_agg(value ORDER BY value)
				FROM jsonb_array_elements_text(k.permissions) permission(value)
				JOIN key_permission_definitions definition
					ON definition.key = permission.value AND definition.active
			), '[]'::jsonb),
			k.created_at, k.expires_at,
			k.last_used_at, k.revoked_at, k.rotated_from, k.user_id, u.active, k.key_hash
		FROM api_keys k JOIN users u ON u.id = k.user_id
		CROSS JOIN key_policy policy
		WHERE k.key_hash = $1 AND k.revoked_at IS NULL
			AND (k.expires_at IS NULL OR k.expires_at > now())
			AND (NOT policy.expire_unused OR COALESCE(k.last_used_at, k.created_at) > now() - policy.unused_expiry_days * interval '1 day')
			AND (NOT policy.force_rotation OR k.created_at > now() - policy.force_rotation_days * interval '1 day')`, hash,
	).Scan(&credential.Key.ID, &credential.Key.Name, &credential.Key.Prefix,
		&permissionsJSON, &credential.Key.CreatedAt, &credential.Key.ExpiresAt,
		&credential.Key.LastUsedAt, &credential.Key.RevokedAt, &credential.Key.RotatedFrom,
		&credential.UserID, &credential.UserActive, &credential.Hash)
	if err != nil {
		return APIKeyCredential{}, normalizeError("get API key credential", err)
	}
	if err := json.Unmarshal(permissionsJSON, &credential.Permissions); err != nil {
		return APIKeyCredential{}, fmt.Errorf("decode API key credential permissions: %w", err)
	}
	credential.Key.Permissions = append([]string(nil), credential.Permissions...)
	if !credential.UserActive {
		return APIKeyCredential{}, fmt.Errorf("API key user is inactive: %w", ErrForbidden)
	}
	return credential, nil
}

func getKeyPolicy(ctx context.Context, db queryRower) (model.KeyPolicy, error) {
	var policy model.KeyPolicy
	err := db.QueryRow(ctx, `
		SELECT max_keys, default_expiry_days, rotation_grace_days, expire_unused,
			unused_expiry_days, force_rotation, force_rotation_days
		FROM key_policy WHERE singleton`).Scan(&policy.MaxKeys, &policy.DefaultExpiryDays,
		&policy.RotationGraceDays, &policy.ExpireUnused, &policy.UnusedExpiryDays,
		&policy.ForceRotation, &policy.ForceRotationDays)
	return policy, normalizeError("get key policy", err)
}

func (r *Repository) GetKeyPolicy(ctx context.Context) (model.KeyPolicy, error) {
	return getKeyPolicy(ctx, r.pool)
}

func (r *Repository) UpdateKeyPolicy(ctx context.Context, policy model.KeyPolicy) (model.KeyPolicy, error) {
	if policy.MaxKeys < 1 || policy.MaxKeys > 100 || policy.DefaultExpiryDays < 1 ||
		policy.DefaultExpiryDays > 3650 || policy.RotationGraceDays < 0 ||
		policy.RotationGraceDays > 365 || policy.UnusedExpiryDays < 1 ||
		policy.UnusedExpiryDays > 3650 || policy.ForceRotationDays < 1 || policy.ForceRotationDays > 3650 {
		return model.KeyPolicy{}, fmt.Errorf("key policy: %w", ErrInvalid)
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE key_policy SET max_keys = $1, default_expiry_days = $2,
			rotation_grace_days = $3, expire_unused = $4, unused_expiry_days = $5,
			force_rotation = $6, force_rotation_days = $7, updated_at = now()
		WHERE singleton`, policy.MaxKeys, policy.DefaultExpiryDays, policy.RotationGraceDays,
		policy.ExpireUnused, policy.UnusedExpiryDays, policy.ForceRotation, policy.ForceRotationDays)
	if err != nil {
		return model.KeyPolicy{}, normalizeError("update key policy", err)
	}
	return r.GetKeyPolicy(ctx)
}

func validateAPIKeyParams(params CreateAPIKeyParams) (CreateAPIKeyParams, error) {
	params.Name = strings.TrimSpace(params.Name)
	params.Prefix = strings.TrimSpace(params.Prefix)
	params.Permissions = uniqueStrings(params.Permissions)
	if params.UserID == uuid.Nil || params.Name == "" || params.Prefix == "" || len(params.Hash) < 16 || len(params.Permissions) == 0 {
		return CreateAPIKeyParams{}, fmt.Errorf("API key fields: %w", ErrInvalid)
	}
	return params, nil
}

func validateKeyPermissions(ctx context.Context, tx pgx.Tx, permissions []string) error {
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::int FROM key_permission_definitions
		WHERE key = ANY($1) AND active`, permissions).Scan(&count); err != nil {
		return normalizeError("validate API key permissions", err)
	}
	if count != len(permissions) {
		return fmt.Errorf("unknown or inactive API key permission: %w", ErrInvalid)
	}
	return nil
}

func createAPIKeyTx(ctx context.Context, tx pgx.Tx, params CreateAPIKeyParams, defaultExpiryDays int) (model.APIKey, error) {
	if params.ExpiresAt == nil {
		expires := time.Now().UTC().AddDate(0, 0, defaultExpiryDays)
		params.ExpiresAt = &expires
	}
	var key model.APIKey
	var permissionsJSON []byte
	err := tx.QueryRow(ctx, `
		INSERT INTO api_keys(user_id, name, key_prefix, key_hash, permissions, expires_at, rotated_from)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+apiKeyColumns,
		params.UserID, params.Name, params.Prefix, params.Hash, jsonValue(params.Permissions),
		params.ExpiresAt, params.RotatedFrom,
	).Scan(&key.ID, &key.Name, &key.Prefix, &permissionsJSON, &key.CreatedAt,
		&key.ExpiresAt, &key.LastUsedAt, &key.RevokedAt, &key.RotatedFrom)
	if err != nil {
		return model.APIKey{}, normalizeError("create API key", err)
	}
	key.Permissions = append([]string(nil), params.Permissions...)
	return key, nil
}

func (r *Repository) CreateAPIKey(ctx context.Context, params CreateAPIKeyParams) (model.APIKey, error) {
	params, err := validateAPIKeyParams(params)
	if err != nil {
		return model.APIKey{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.APIKey{}, normalizeError("begin create API key", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userActive bool
	if err := tx.QueryRow(ctx, `SELECT active FROM users WHERE id = $1 FOR UPDATE`, params.UserID).Scan(&userActive); err != nil {
		return model.APIKey{}, normalizeError("lock API key user", err)
	}
	if !userActive {
		return model.APIKey{}, fmt.Errorf("API key user is inactive: %w", ErrForbidden)
	}
	policy, err := getKeyPolicy(ctx, tx)
	if err != nil {
		return model.APIKey{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::int FROM api_keys WHERE user_id = $1 AND revoked_at IS NULL
			AND (expires_at IS NULL OR expires_at > now())`, params.UserID).Scan(&count); err != nil {
		return model.APIKey{}, normalizeError("count active API keys", err)
	}
	if count >= policy.MaxKeys {
		return model.APIKey{}, fmt.Errorf("maximum active API keys reached: %w", ErrConflict)
	}
	if err := validateKeyPermissions(ctx, tx, params.Permissions); err != nil {
		return model.APIKey{}, err
	}
	key, err := createAPIKeyTx(ctx, tx, params, policy.DefaultExpiryDays)
	if err != nil {
		return model.APIKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.APIKey{}, normalizeError("commit create API key", err)
	}
	return key, nil
}

func (r *Repository) RotateAPIKey(ctx context.Context, oldKeyID uuid.UUID, params CreateAPIKeyParams) (APIKeyRotationResult, error) {
	params, err := validateAPIKeyParams(params)
	if err != nil {
		return APIKeyRotationResult{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return APIKeyRotationResult{}, normalizeError("begin rotate API key", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var old model.APIKey
	var oldPermissions []byte
	err = tx.QueryRow(ctx, `
		SELECT `+apiKeyColumns+` FROM api_keys
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL FOR UPDATE`,
		oldKeyID, params.UserID,
	).Scan(&old.ID, &old.Name, &old.Prefix, &oldPermissions, &old.CreatedAt,
		&old.ExpiresAt, &old.LastUsedAt, &old.RevokedAt, &old.RotatedFrom)
	if err != nil {
		return APIKeyRotationResult{}, normalizeError("lock rotated API key", err)
	}
	_ = json.Unmarshal(oldPermissions, &old.Permissions)
	policy, err := getKeyPolicy(ctx, tx)
	if err != nil {
		return APIKeyRotationResult{}, err
	}
	if err := validateKeyPermissions(ctx, tx, params.Permissions); err != nil {
		return APIKeyRotationResult{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::int FROM api_keys WHERE user_id = $1 AND id <> $2
			AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())`,
		params.UserID, oldKeyID).Scan(&count); err != nil {
		return APIKeyRotationResult{}, normalizeError("count API keys for rotation", err)
	}
	if count >= policy.MaxKeys {
		return APIKeyRotationResult{}, fmt.Errorf("maximum active API keys reached: %w", ErrConflict)
	}
	graceEnd := time.Now().UTC().AddDate(0, 0, policy.RotationGraceDays)
	if policy.RotationGraceDays == 0 {
		if _, err := tx.Exec(ctx, `UPDATE api_keys SET revoked_at = now(), expires_at = now() WHERE id = $1`, oldKeyID); err != nil {
			return APIKeyRotationResult{}, normalizeError("revoke rotated API key", err)
		}
		now := time.Now().UTC()
		old.RevokedAt = &now
		old.ExpiresAt = &now
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE api_keys SET expires_at = CASE
				WHEN expires_at IS NULL OR expires_at > $2 THEN $2 ELSE expires_at END
			WHERE id = $1`, oldKeyID, graceEnd); err != nil {
			return APIKeyRotationResult{}, normalizeError("set API key rotation grace", err)
		}
		if old.ExpiresAt == nil || old.ExpiresAt.After(graceEnd) {
			old.ExpiresAt = &graceEnd
		}
	}
	params.RotatedFrom = &oldKeyID
	newKey, err := createAPIKeyTx(ctx, tx, params, policy.DefaultExpiryDays)
	if err != nil {
		return APIKeyRotationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIKeyRotationResult{}, normalizeError("commit API key rotation", err)
	}
	return APIKeyRotationResult{OldKey: old, NewKey: newKey, GracePeriodEnds: graceEnd}, nil
}

func (r *Repository) RevokeAPIKey(ctx context.Context, userID, keyID uuid.UUID) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE api_keys SET revoked_at = COALESCE(revoked_at, now())
		WHERE id = $1 AND user_id = $2`, keyID, userID)
	if err != nil {
		return normalizeError("revoke API key", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("revoke API key: %w", ErrNotFound)
	}
	return nil
}

func (r *Repository) TouchAPIKey(ctx context.Context, keyID uuid.UUID, usedAt time.Time) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE api_keys SET last_used_at = $2
		WHERE id = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > $2)`, keyID, usedAt)
	if err != nil {
		return normalizeError("touch API key", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("touch API key: %w", ErrNotFound)
	}
	return nil
}

func (r *Repository) ListKeyPermissionDefinitions(ctx context.Context, includeInactive bool) ([]model.KeyPermissionDefinition, error) {
	query := `SELECT key, name, description, active, created_at, updated_at FROM key_permission_definitions`
	if !includeInactive {
		query += ` WHERE active`
	}
	query += ` ORDER BY key`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, normalizeError("list key permission definitions", err)
	}
	defer rows.Close()
	result := []model.KeyPermissionDefinition{}
	for rows.Next() {
		var definition model.KeyPermissionDefinition
		if err := rows.Scan(&definition.Key, &definition.Name, &definition.Description,
			&definition.Active, &definition.CreatedAt, &definition.UpdatedAt); err != nil {
			return nil, normalizeError("scan key permission definition", err)
		}
		result = append(result, definition)
	}
	return result, normalizeError("iterate key permission definitions", rows.Err())
}

func (r *Repository) UpsertKeyPermissionDefinition(ctx context.Context, definition model.KeyPermissionDefinition) (model.KeyPermissionDefinition, error) {
	definition.Key = normalizeKey(definition.Key)
	definition.Name = strings.TrimSpace(definition.Name)
	definition.Description = strings.TrimSpace(definition.Description)
	if definition.Key == "" || definition.Name == "" {
		return model.KeyPermissionDefinition{}, fmt.Errorf("key permission definition: %w", ErrInvalid)
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO key_permission_definitions(key, name, description, active)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE SET name = EXCLUDED.name,
			description = EXCLUDED.description, active = EXCLUDED.active, updated_at = now()
		RETURNING key, name, description, active, created_at, updated_at`,
		definition.Key, definition.Name, definition.Description, definition.Active,
	).Scan(&definition.Key, &definition.Name, &definition.Description,
		&definition.Active, &definition.CreatedAt, &definition.UpdatedAt)
	return definition, normalizeError("upsert key permission definition", err)
}

func (r *Repository) ListKeyPermissionTemplates(ctx context.Context) ([]model.KeyPermissionTemplate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, description, permissions, created_at, updated_at
		FROM key_permission_templates ORDER BY name, id`)
	if err != nil {
		return nil, normalizeError("list key permission templates", err)
	}
	defer rows.Close()
	result := []model.KeyPermissionTemplate{}
	for rows.Next() {
		var template model.KeyPermissionTemplate
		var permissions []byte
		if err := rows.Scan(&template.ID, &template.Name, &template.Description,
			&permissions, &template.CreatedAt, &template.UpdatedAt); err != nil {
			return nil, normalizeError("scan key permission template", err)
		}
		if err := json.Unmarshal(permissions, &template.Permissions); err != nil {
			return nil, fmt.Errorf("decode key permission template: %w", err)
		}
		result = append(result, template)
	}
	return result, normalizeError("iterate key permission templates", rows.Err())
}

func (r *Repository) CreateKeyPermissionTemplate(ctx context.Context, template model.KeyPermissionTemplate) (model.KeyPermissionTemplate, error) {
	template.Name = strings.TrimSpace(template.Name)
	template.Description = strings.TrimSpace(template.Description)
	template.Permissions = uniqueStrings(template.Permissions)
	if template.Name == "" || len(template.Permissions) == 0 {
		return model.KeyPermissionTemplate{}, fmt.Errorf("key permission template: %w", ErrInvalid)
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO key_permission_templates(name, description, permissions)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at`, template.Name, template.Description,
		jsonValue(template.Permissions)).Scan(&template.ID, &template.CreatedAt, &template.UpdatedAt)
	return template, normalizeError("create key permission template", err)
}

func (r *Repository) UpdateKeyPermissionTemplate(ctx context.Context, template model.KeyPermissionTemplate) (model.KeyPermissionTemplate, error) {
	template.Name = strings.TrimSpace(template.Name)
	template.Description = strings.TrimSpace(template.Description)
	template.Permissions = uniqueStrings(template.Permissions)
	if template.ID == uuid.Nil || template.Name == "" || len(template.Permissions) == 0 {
		return model.KeyPermissionTemplate{}, fmt.Errorf("key permission template: %w", ErrInvalid)
	}
	err := r.pool.QueryRow(ctx, `
		UPDATE key_permission_templates SET name = $2, description = $3,
			permissions = $4, updated_at = now() WHERE id = $1
		RETURNING created_at, updated_at`, template.ID, template.Name,
		template.Description, jsonValue(template.Permissions)).Scan(&template.CreatedAt, &template.UpdatedAt)
	return template, normalizeError("update key permission template", err)
}

func (r *Repository) DeleteKeyPermissionTemplate(ctx context.Context, id uuid.UUID) error {
	result, err := r.pool.Exec(ctx, `DELETE FROM key_permission_templates WHERE id = $1`, id)
	if err != nil {
		return normalizeError("delete key permission template", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("delete key permission template: %w", ErrNotFound)
	}
	return nil
}
