package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

const userSelect = `
	SELECT u.id, u.subject, u.username, u.email, u.display_name, u.team,
		u.auth_source, u.active,
		ARRAY(SELECT r.key FROM user_roles ur JOIN roles r ON r.id = ur.role_id
			WHERE ur.user_id = u.id ORDER BY r.key),
		ARRAY(SELECT DISTINCT rp.permission_key
			FROM user_roles ur
			JOIN role_permissions rp ON rp.role_id = ur.role_id
			JOIN permissions p ON p.key = rp.permission_key AND p.active
			WHERE ur.user_id = u.id ORDER BY rp.permission_key),
		u.created_at, u.updated_at
	FROM users u`

const (
	// DefaultUserRole is the baseline for a signed-in person: browse, favourite
	// and hold personal keys.
	DefaultUserRole = "user"
	// ContributorRole additionally allows submitting and maintaining apps.
	ContributorRole = "contributor"
)

// BaselineRole picks the role every signed-in person starts with. With the
// approval workflow on, submissions are reviewed before they reach the
// catalogue, so contributing is safe to grant by default; with it off a
// submission publishes straight away and the baseline stays read-only.
func BaselineRole(workflowEnabled bool) string {
	if workflowEnabled {
		return ContributorRole
	}
	return DefaultUserRole
}

type OIDCUserInput struct {
	Subject     string
	Username    string
	Email       string
	DisplayName string
	Team        string
	// DefaultRole is the baseline role granted on first sign-in. Empty means
	// the plain authenticated user role.
	DefaultRole string
}

type UserUpdate struct {
	Username    string
	Email       string
	DisplayName string
	Team        string
	Active      bool
}

type UserListOptions struct {
	Query      string
	Role       string
	AuthSource string
	Active     *bool
	Limit      int
	Offset     int
}

type BootstrapCredential struct {
	User         model.User
	PasswordHash string
}

func scanUser(row rowScanner) (model.User, error) {
	var user model.User
	err := row.Scan(
		&user.ID, &user.Subject, &user.Username, &user.Email, &user.DisplayName,
		&user.Team, &user.AuthSource, &user.Active, &user.Roles,
		&user.Permissions, &user.CreatedAt, &user.UpdatedAt,
	)
	if user.Roles == nil {
		user.Roles = []string{}
	}
	if user.Permissions == nil {
		user.Permissions = []string{}
	}
	return user, err
}

func (r *Repository) GetUserByID(ctx context.Context, id uuid.UUID) (model.User, error) {
	user, err := scanUser(r.pool.QueryRow(ctx, userSelect+` WHERE u.id = $1`, id))
	return user, normalizeError("get user by id", err)
}

func (r *Repository) GetUserBySubject(ctx context.Context, subject string) (model.User, error) {
	user, err := scanUser(r.pool.QueryRow(ctx, userSelect+` WHERE u.subject = $1`, strings.TrimSpace(subject)))
	return user, normalizeError("get user by subject", err)
}

func (r *Repository) GetUserByUsername(ctx context.Context, username string) (model.User, error) {
	user, err := scanUser(r.pool.QueryRow(ctx, userSelect+` WHERE lower(u.username) = lower($1)`, strings.TrimSpace(username)))
	return user, normalizeError("get user by username", err)
}

// HasBootstrapCredential reports whether a local admin account can still sign
// in. It stays true once SSO is configured: the local account is the documented
// recovery path for when the identity provider is unreachable.
func (r *Repository) HasBootstrapCredential(ctx context.Context) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM users
			WHERE auth_source = 'bootstrap' AND active AND password_hash <> ''
		)`).Scan(&exists)
	if err != nil {
		return false, normalizeError("check bootstrap credential", err)
	}
	return exists, nil
}

func (r *Repository) GetBootstrapCredential(ctx context.Context, username string) (BootstrapCredential, error) {
	var result BootstrapCredential
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT id, password_hash FROM users
		WHERE lower(username) = lower($1) AND auth_source = 'bootstrap' AND active`,
		strings.TrimSpace(username),
	).Scan(&id, &result.PasswordHash)
	if err != nil {
		return result, normalizeError("get bootstrap credential", err)
	}
	result.User, err = r.GetUserByID(ctx, id)
	return result, err
}

func (r *Repository) UpsertOIDCUser(ctx context.Context, input OIDCUserInput) (model.User, error) {
	input.Subject = strings.TrimSpace(input.Subject)
	input.Username = strings.TrimSpace(input.Username)
	if input.Subject == "" || input.Username == "" {
		return model.User{}, fmt.Errorf("upsert OIDC user: %w", ErrInvalid)
	}
	if strings.TrimSpace(input.DisplayName) == "" {
		input.DisplayName = input.Username
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.User{}, normalizeError("begin OIDC user upsert", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO users(subject, username, email, display_name, team, auth_source, active)
		VALUES ($1, $2, $3, $4, $5, 'oidc', true)
		ON CONFLICT (subject) DO UPDATE SET
			username = EXCLUDED.username,
			email = EXCLUDED.email,
			display_name = EXCLUDED.display_name,
			team = EXCLUDED.team,
			updated_at = now()
		RETURNING id`,
		input.Subject, input.Username, strings.TrimSpace(input.Email),
		strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.Team),
	).Scan(&id)
	if err != nil {
		return model.User{}, normalizeError("upsert OIDC user", err)
	}
	defaultRole := strings.TrimSpace(input.DefaultRole)
	if defaultRole == "" {
		defaultRole = DefaultUserRole
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_roles(user_id, role_id, source)
		SELECT $1, id, 'default' FROM roles WHERE key = $2
		ON CONFLICT DO NOTHING`, id, defaultRole); err != nil {
		return model.User{}, normalizeError("grant default user role", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.User{}, normalizeError("commit OIDC user upsert", err)
	}
	return r.GetUserByID(ctx, id)
}

func (r *Repository) ListUsers(ctx context.Context, options UserListOptions) (model.Page[model.User], error) {
	limit, offset := normalizePage(options.Limit, options.Offset, 50)
	where := []string{"true"}
	args := make([]any, 0, 6)
	add := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if query := strings.TrimSpace(options.Query); query != "" {
		parameter := add(likePattern(query))
		where = append(where, `(u.username ILIKE `+parameter+` ESCAPE '\' OR u.email ILIKE `+parameter+` ESCAPE '\' OR u.display_name ILIKE `+parameter+` ESCAPE '\')`)
	}
	if role := normalizeKey(options.Role); role != "" {
		parameter := add(role)
		where = append(where, `EXISTS (SELECT 1 FROM user_roles fur JOIN roles fr ON fr.id = fur.role_id WHERE fur.user_id = u.id AND fr.key = `+parameter+`)`)
	}
	if source := normalizeKey(options.AuthSource); source != "" {
		where = append(where, `u.auth_source = `+add(source))
	}
	if options.Active != nil {
		where = append(where, `u.active = `+add(*options.Active))
	}

	args = append(args, limit, offset)
	query := userSelect[:strings.LastIndex(userSelect, "FROM users u")] + `,
		COUNT(*) OVER() AS total
	FROM users u WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY lower(u.username), u.id LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return model.Page[model.User]{}, normalizeError("list users", err)
	}
	defer rows.Close()

	page := model.Page[model.User]{Items: []model.User{}, Limit: limit, Offset: offset}
	for rows.Next() {
		var user model.User
		var total int
		err := rows.Scan(
			&user.ID, &user.Subject, &user.Username, &user.Email, &user.DisplayName,
			&user.Team, &user.AuthSource, &user.Active, &user.Roles,
			&user.Permissions, &user.CreatedAt, &user.UpdatedAt, &total,
		)
		if err != nil {
			return model.Page[model.User]{}, normalizeError("scan user list", err)
		}
		page.Total = total
		page.Items = append(page.Items, user)
	}
	if err := rows.Err(); err != nil {
		return model.Page[model.User]{}, normalizeError("iterate user list", err)
	}
	return page, nil
}

func (r *Repository) UpdateUser(ctx context.Context, id uuid.UUID, input UserUpdate) (model.User, error) {
	if strings.TrimSpace(input.Username) == "" {
		return model.User{}, fmt.Errorf("update user: %w", ErrInvalid)
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE users SET username = $2, email = $3, display_name = $4,
			team = $5, active = $6, updated_at = now()
		WHERE id = $1`, id, strings.TrimSpace(input.Username), strings.TrimSpace(input.Email),
		strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.Team), input.Active)
	if err != nil {
		return model.User{}, normalizeError("update user", err)
	}
	if result.RowsAffected() == 0 {
		return model.User{}, fmt.Errorf("update user: %w", ErrNotFound)
	}
	return r.GetUserByID(ctx, id)
}

func (r *Repository) SetUserActive(ctx context.Context, id uuid.UUID, active bool) error {
	result, err := r.pool.Exec(ctx, `UPDATE users SET active = $2, updated_at = now() WHERE id = $1`, id, active)
	if err != nil {
		return normalizeError("set user active", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("set user active: %w", ErrNotFound)
	}
	if !active {
		_, _ = r.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
	}
	return nil
}

func (r *Repository) DeleteUser(ctx context.Context, id uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return normalizeError("begin remove user access", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Users are retained as inactive records so immutable audit rows, app
	// ownership, and review history never need to be rewritten or orphaned.
	result, err := tx.Exec(ctx, `
		UPDATE users SET active = false, email = '', team = '', updated_at = now()
		WHERE id = $1 AND auth_source <> 'bootstrap'`, id)
	if err != nil {
		return normalizeError("remove user access", err)
	}
	if result.RowsAffected() == 0 {
		var source string
		if err := tx.QueryRow(ctx, `SELECT auth_source FROM users WHERE id = $1`, id).Scan(&source); err != nil {
			return normalizeError("remove user access", err)
		}
		return fmt.Errorf("remove bootstrap user access: %w", ErrForbidden)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id); err != nil {
		return normalizeError("delete removed user sessions", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return normalizeError("commit remove user access", err)
	}
	return nil
}

type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash []byte
	CSRFHash  []byte
	ExpiresAt time.Time
	LastSeen  time.Time
	IP        string
	UserAgent string
	CreatedAt time.Time
}

type CreateSessionParams struct {
	UserID    uuid.UUID
	TokenHash []byte
	CSRFHash  []byte
	ExpiresAt time.Time
	IP        string
	UserAgent string
}

func (r *Repository) CreateSession(ctx context.Context, params CreateSessionParams) (Session, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Session{}, normalizeError("begin create session", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the user row while the session is created so an administrator cannot
	// deactivate the account concurrently and leave a fresh usable session.
	var active bool
	if err := tx.QueryRow(ctx, `SELECT active FROM users WHERE id = $1 FOR SHARE`, params.UserID).Scan(&active); err != nil {
		return Session{}, normalizeError("get session user", err)
	}
	if !active {
		return Session{}, fmt.Errorf("create session for inactive user: %w", ErrForbidden)
	}

	var session Session
	err = tx.QueryRow(ctx, `
		INSERT INTO sessions(user_id, token_hash, csrf_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, token_hash, csrf_hash, expires_at, last_seen_at, ip, user_agent, created_at`,
		params.UserID, params.TokenHash, params.CSRFHash, params.ExpiresAt,
		params.IP, params.UserAgent,
	).Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CSRFHash,
		&session.ExpiresAt, &session.LastSeen, &session.IP, &session.UserAgent, &session.CreatedAt)
	if err != nil {
		return Session{}, normalizeError("create session", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, normalizeError("commit create session", err)
	}
	return session, nil
}

func (r *Repository) GetSessionByTokenHash(ctx context.Context, tokenHash []byte) (Session, model.User, error) {
	var session Session
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, token_hash, csrf_hash, expires_at, last_seen_at, ip, user_agent, created_at
		FROM sessions WHERE token_hash = $1 AND expires_at > now()`, tokenHash,
	).Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CSRFHash,
		&session.ExpiresAt, &session.LastSeen, &session.IP, &session.UserAgent, &session.CreatedAt)
	if err != nil {
		return Session{}, model.User{}, normalizeError("get session", err)
	}
	user, err := r.GetUserByID(ctx, session.UserID)
	if err != nil {
		return Session{}, model.User{}, err
	}
	if !user.Active {
		return Session{}, model.User{}, fmt.Errorf("get session: %w", ErrForbidden)
	}
	return session, user, nil
}

func (r *Repository) TouchSession(ctx context.Context, tokenHash []byte, at time.Time) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE sessions SET last_seen_at = $2
		WHERE token_hash = $1 AND expires_at > $2`, tokenHash, at)
	if err != nil {
		return normalizeError("touch session", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("touch session: %w", ErrNotFound)
	}
	return nil
}

func (r *Repository) DeleteSessionByTokenHash(ctx context.Context, tokenHash []byte) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return normalizeError("delete session", err)
}

func (r *Repository) DeleteUserSessions(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return normalizeError("delete user sessions", err)
}

func (r *Repository) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	result, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, normalizeError("delete expired sessions", err)
	}
	return result.RowsAffected(), nil
}

type OIDCAuthRequest struct {
	StateHash []byte
	Nonce     string
	Verifier  string
	ReturnTo  string
	ExpiresAt time.Time
	CreatedAt time.Time
}

func (r *Repository) CreateOIDCAuthRequest(ctx context.Context, request OIDCAuthRequest) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO oidc_auth_requests(state_hash, nonce, verifier, return_to, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, request.StateHash, request.Nonce,
		request.Verifier, request.ReturnTo, request.ExpiresAt)
	return normalizeError("create OIDC auth request", err)
}

func (r *Repository) ConsumeOIDCAuthRequest(ctx context.Context, stateHash []byte) (OIDCAuthRequest, error) {
	var request OIDCAuthRequest
	err := r.pool.QueryRow(ctx, `
		DELETE FROM oidc_auth_requests
		WHERE state_hash = $1 AND expires_at > now()
		RETURNING state_hash, nonce, verifier, return_to, expires_at, created_at`, stateHash,
	).Scan(&request.StateHash, &request.Nonce, &request.Verifier, &request.ReturnTo,
		&request.ExpiresAt, &request.CreatedAt)
	return request, normalizeError("consume OIDC auth request", err)
}

func (r *Repository) DeleteExpiredOIDCAuthRequests(ctx context.Context) (int64, error) {
	result, err := r.pool.Exec(ctx, `DELETE FROM oidc_auth_requests WHERE expires_at <= now()`)
	if err != nil {
		return 0, normalizeError("delete expired OIDC auth requests", err)
	}
	return result.RowsAffected(), nil
}
