package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/jackc/pgx/v5"
)

var roleKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

type RoleInput struct {
	Key         string
	Name        string
	Description string
	Permissions []string
}

func (r *Repository) ListPermissions(ctx context.Context, includeInactive bool) ([]model.Permission, error) {
	query := `SELECT key, description, category, active, created_at FROM permissions`
	if !includeInactive {
		query += ` WHERE active`
	}
	query += ` ORDER BY category, key`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, normalizeError("list permissions", err)
	}
	defer rows.Close()
	result := []model.Permission{}
	for rows.Next() {
		var permission model.Permission
		if err := rows.Scan(&permission.Key, &permission.Description, &permission.Category,
			&permission.Active, &permission.CreatedAt); err != nil {
			return nil, normalizeError("scan permission", err)
		}
		result = append(result, permission)
	}
	return result, normalizeError("iterate permissions", rows.Err())
}

func (r *Repository) UpsertPermission(ctx context.Context, permission model.Permission) (model.Permission, error) {
	permission.Key = normalizeKey(permission.Key)
	permission.Description = strings.TrimSpace(permission.Description)
	permission.Category = normalizeKey(permission.Category)
	if permission.Key == "" || permission.Description == "" || permission.Category == "" {
		return model.Permission{}, fmt.Errorf("permission: %w", ErrInvalid)
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO permissions(key, description, category, active)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE SET description = EXCLUDED.description,
			category = EXCLUDED.category, active = EXCLUDED.active
		RETURNING key, description, category, active, created_at`,
		permission.Key, permission.Description, permission.Category, permission.Active,
	).Scan(&permission.Key, &permission.Description, &permission.Category,
		&permission.Active, &permission.CreatedAt)
	return permission, normalizeError("upsert permission", err)
}

const roleSelect = `
	SELECT r.id, r.key, r.name, r.description, r.system,
		ARRAY(SELECT rp.permission_key FROM role_permissions rp
			WHERE rp.role_id = r.id ORDER BY rp.permission_key),
		(SELECT count(*)::int FROM user_roles ur WHERE ur.role_id = r.id),
		r.created_at, r.updated_at
	FROM roles r`

func scanRole(row rowScanner) (model.Role, error) {
	var role model.Role
	err := row.Scan(&role.ID, &role.Key, &role.Name, &role.Description, &role.System,
		&role.Permissions, &role.UserCount, &role.CreatedAt, &role.UpdatedAt)
	if role.Permissions == nil {
		role.Permissions = []string{}
	}
	return role, err
}

func (r *Repository) ListRoles(ctx context.Context) ([]model.Role, error) {
	rows, err := r.pool.Query(ctx, roleSelect+` ORDER BY r.system DESC, r.name, r.key`)
	if err != nil {
		return nil, normalizeError("list roles", err)
	}
	defer rows.Close()
	result := []model.Role{}
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, normalizeError("scan role", err)
		}
		result = append(result, role)
	}
	return result, normalizeError("iterate roles", rows.Err())
}

func (r *Repository) GetRole(ctx context.Context, id uuid.UUID) (model.Role, error) {
	role, err := scanRole(r.pool.QueryRow(ctx, roleSelect+` WHERE r.id = $1`, id))
	return role, normalizeError("get role", err)
}

func (r *Repository) GetRoleByKey(ctx context.Context, key string) (model.Role, error) {
	role, err := scanRole(r.pool.QueryRow(ctx, roleSelect+` WHERE r.key = $1`, normalizeKey(key)))
	return role, normalizeError("get role by key", err)
}

func validateRoleInput(input RoleInput) (RoleInput, error) {
	input.Key = normalizeKey(input.Key)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Permissions = uniqueStrings(input.Permissions)
	if !roleKeyPattern.MatchString(input.Key) || input.Name == "" {
		return RoleInput{}, fmt.Errorf("role key or name: %w", ErrInvalid)
	}
	return input, nil
}

func (r *Repository) CreateRole(ctx context.Context, input RoleInput) (model.Role, error) {
	input, err := validateRoleInput(input)
	if err != nil {
		return model.Role{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Role{}, normalizeError("begin create role", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO roles(key, name, description, system)
		VALUES ($1, $2, $3, false) RETURNING id`,
		input.Key, input.Name, input.Description).Scan(&id); err != nil {
		return model.Role{}, normalizeError("create role", err)
	}
	if err := replaceRolePermissions(ctx, tx, id, input.Permissions); err != nil {
		return model.Role{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Role{}, normalizeError("commit create role", err)
	}
	return r.GetRole(ctx, id)
}

func (r *Repository) UpdateRole(ctx context.Context, id uuid.UUID, input RoleInput) (model.Role, error) {
	input, err := validateRoleInput(input)
	if err != nil {
		return model.Role{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Role{}, normalizeError("begin update role", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentKey string
	var system bool
	if err := tx.QueryRow(ctx, `SELECT key, system FROM roles WHERE id = $1 FOR UPDATE`, id).Scan(&currentKey, &system); err != nil {
		return model.Role{}, normalizeError("lock role for update", err)
	}
	if system && input.Key != currentKey {
		return model.Role{}, fmt.Errorf("change system role key: %w", ErrForbidden)
	}
	result, err := tx.Exec(ctx, `
		UPDATE roles SET key = $2, name = $3, description = $4, updated_at = now()
		WHERE id = $1`, id, input.Key, input.Name, input.Description)
	if err != nil {
		return model.Role{}, normalizeError("update role", err)
	}
	if result.RowsAffected() == 0 {
		return model.Role{}, fmt.Errorf("update role: %w", ErrNotFound)
	}
	if err := replaceRolePermissions(ctx, tx, id, input.Permissions); err != nil {
		return model.Role{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Role{}, normalizeError("commit update role", err)
	}
	return r.GetRole(ctx, id)
}

func (r *Repository) DeleteRole(ctx context.Context, id uuid.UUID) error {
	result, err := r.pool.Exec(ctx, `DELETE FROM roles WHERE id = $1 AND NOT system`, id)
	if err != nil {
		return normalizeError("delete role", err)
	}
	if result.RowsAffected() == 0 {
		var system bool
		err := r.pool.QueryRow(ctx, `SELECT system FROM roles WHERE id = $1`, id).Scan(&system)
		if err != nil {
			return normalizeError("delete role", err)
		}
		return fmt.Errorf("delete system role: %w", ErrForbidden)
	}
	return nil
}

func (r *Repository) ReplaceRolePermissions(ctx context.Context, roleID uuid.UUID, permissions []string) (model.Role, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Role{}, normalizeError("begin replace role permissions", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := replaceRolePermissions(ctx, tx, roleID, uniqueStrings(permissions)); err != nil {
		return model.Role{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Role{}, normalizeError("commit replace role permissions", err)
	}
	return r.GetRole(ctx, roleID)
}

func replaceRolePermissions(ctx context.Context, tx pgx.Tx, roleID uuid.UUID, permissions []string) error {
	var roleExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1)`, roleID).Scan(&roleExists); err != nil {
		return normalizeError("check role", err)
	}
	if !roleExists {
		return fmt.Errorf("replace role permissions: %w", ErrNotFound)
	}
	if len(permissions) > 0 {
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT count(*)::int FROM permissions WHERE key = ANY($1) AND active`, permissions,
		).Scan(&count); err != nil {
			return normalizeError("validate role permissions", err)
		}
		if count != len(permissions) {
			return fmt.Errorf("replace role permissions: unknown or inactive permission: %w", ErrInvalid)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
		return normalizeError("clear role permissions", err)
	}
	if len(permissions) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions(role_id, permission_key)
			SELECT $1, unnest($2::text[])`, roleID, permissions); err != nil {
			return normalizeError("insert role permissions", err)
		}
	}
	return nil
}

func (r *Repository) ReplaceUserRoles(ctx context.Context, userID uuid.UUID, roleKeys []string) (model.User, error) {
	roleKeys = uniqueStrings(roleKeys)
	for i := range roleKeys {
		roleKeys[i] = normalizeKey(roleKeys[i])
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.User{}, normalizeError("begin replace user roles", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&userExists); err != nil {
		return model.User{}, normalizeError("check user roles target", err)
	}
	if !userExists {
		return model.User{}, fmt.Errorf("replace user roles: %w", ErrNotFound)
	}
	if len(roleKeys) > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM roles WHERE key = ANY($1)`, roleKeys).Scan(&count); err != nil {
			return model.User{}, normalizeError("validate user roles", err)
		}
		if count != len(roleKeys) {
			return model.User{}, fmt.Errorf("replace user roles: unknown role: %w", ErrInvalid)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1 AND source <> 'bootstrap'`, userID); err != nil {
		return model.User{}, normalizeError("clear user roles", err)
	}
	if len(roleKeys) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_roles(user_id, role_id, source)
			SELECT $1, id, 'manual' FROM roles WHERE key = ANY($2)
			ON CONFLICT (user_id, role_id) DO UPDATE SET source =
				CASE WHEN user_roles.source = 'bootstrap' THEN user_roles.source ELSE 'manual' END`,
			userID, roleKeys); err != nil {
			return model.User{}, normalizeError("insert user roles", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return model.User{}, normalizeError("commit replace user roles", err)
	}
	return r.GetUserByID(ctx, userID)
}
