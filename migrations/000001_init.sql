CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject text NOT NULL UNIQUE,
    username text NOT NULL,
    email text NOT NULL DEFAULT '',
    display_name text NOT NULL DEFAULT '',
    team text NOT NULL DEFAULT '',
    auth_source text NOT NULL DEFAULT 'oidc',
    password_hash text NOT NULL DEFAULT '',
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_idx ON users (lower(username));

CREATE TABLE IF NOT EXISTS permissions (
    key text PRIMARY KEY,
    description text NOT NULL,
    category text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key text NOT NULL UNIQUE,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    system boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_key text NOT NULL REFERENCES permissions(key) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_key)
);

CREATE TABLE IF NOT EXISTS user_roles (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    source text NOT NULL DEFAULT 'manual',
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE IF NOT EXISTS sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    csrf_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    ip text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expires_at);

CREATE TABLE IF NOT EXISTS oidc_auth_requests (
    state_hash bytea PRIMARY KEY,
    nonce text NOT NULL,
    verifier text NOT NULL,
    return_to text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS categories (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    icon text NOT NULL DEFAULT '📦',
    description text NOT NULL DEFAULT '',
    position integer NOT NULL DEFAULT 0,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS apps (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    external_seed_id bigint UNIQUE,
    owner_id uuid REFERENCES users(id) ON DELETE SET NULL,
    category_id uuid NOT NULL REFERENCES categories(id),
    name text NOT NULL,
    slug text NOT NULL UNIQUE,
    summary text NOT NULL,
    description text NOT NULL,
    icon text NOT NULL DEFAULT '📦',
    gradient text NOT NULL DEFAULT '',
    service_url text NOT NULL DEFAULT '',
    tags jsonb NOT NULL DEFAULT '[]'::jsonb,
    screenshots jsonb NOT NULL DEFAULT '[]'::jsonb,
    language text NOT NULL DEFAULT '',
    framework text NOT NULL DEFAULT '',
    supports_mcp boolean NOT NULL DEFAULT false,
    supports_api boolean NOT NULL DEFAULT false,
    team text NOT NULL DEFAULT '',
    app_version text NOT NULL DEFAULT '',
    visibility text NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'pending_review', 'published', 'rejected', 'archived')),
    featured boolean NOT NULL DEFAULT false,
    trending_score integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX IF NOT EXISTS apps_public_idx ON apps (status, visibility, updated_at DESC);
CREATE INDEX IF NOT EXISTS apps_category_idx ON apps (category_id, status);
CREATE INDEX IF NOT EXISTS apps_owner_idx ON apps (owner_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS apps_search_idx ON apps USING gin (to_tsvector('simple', name || ' ' || summary || ' ' || description));

CREATE TABLE IF NOT EXISTS app_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    version text NOT NULL,
    notes text NOT NULL DEFAULT '',
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (app_id, version)
);

CREATE TABLE IF NOT EXISTS favorites (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, app_id)
);

CREATE TABLE IF NOT EXISTS reviews (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    submitter_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    reviewer_id uuid REFERENCES users(id) ON DELETE SET NULL,
    level integer NOT NULL DEFAULT 1 CHECK (level > 0),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz
);
CREATE INDEX IF NOT EXISTS reviews_queue_idx ON reviews (status, created_at);

CREATE TABLE IF NOT EXISTS workflow_config (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT false,
    levels integer NOT NULL DEFAULT 1 CHECK (levels BETWEEN 1 AND 10),
    reviewer_roles jsonb NOT NULL DEFAULT '["reviewer"]'::jsonb,
    team_leader_roles jsonb NOT NULL DEFAULT '["team_leader"]'::jsonb,
    auto_publish boolean NOT NULL DEFAULT true,
    reject_reason_required boolean NOT NULL DEFAULT true,
    reapproval_after_edit boolean NOT NULL DEFAULT true,
    prevent_self_approval boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO workflow_config(singleton) VALUES (true) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS system_settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO system_settings(key, value) VALUES
    ('system', '{"siteName":"Dev App Store","siteUrl":"","logoUrl":"","faviconUrl":"","theme":"system","defaultLanguage":"ko","pageSize":24,"publicMode":true}'::jsonb),
    ('api', '{"enabled":true,"anonymous":true,"rateLimitPerMinute":120}'::jsonb),
    ('mcp', '{"enabled":true,"anonymous":true,"rateLimitPerMinute":60,"protocolVersion":"2026-07-28"}'::jsonb)
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS oidc_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT false,
    issuer_url text NOT NULL DEFAULT '',
    client_id text NOT NULL DEFAULT '',
    client_secret_encrypted text NOT NULL DEFAULT '',
    role_claim_path text NOT NULL DEFAULT 'realm_access.roles',
    group_claim_path text NOT NULL DEFAULT 'groups',
    role_mappings jsonb NOT NULL DEFAULT '{"appstore-user":["user"],"appstore-contributor":["contributor"],"appstore-reviewer":["reviewer"],"appstore-manager":["team_leader"],"appstore-admin":["admin"],"appstore-super-admin":["super_admin"]}'::jsonb,
    group_mappings jsonb NOT NULL DEFAULT '{}'::jsonb,
    scopes jsonb NOT NULL DEFAULT '["openid","profile","email"]'::jsonb,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO oidc_settings(singleton) VALUES (true) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS key_permission_definitions (
    key text PRIMARY KEY,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS key_permission_templates (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    permissions jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS key_policy (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    max_keys integer NOT NULL DEFAULT 5 CHECK (max_keys BETWEEN 1 AND 100),
    default_expiry_days integer NOT NULL DEFAULT 90 CHECK (default_expiry_days BETWEEN 1 AND 3650),
    rotation_grace_days integer NOT NULL DEFAULT 7 CHECK (rotation_grace_days BETWEEN 0 AND 365),
    expire_unused boolean NOT NULL DEFAULT false,
    unused_expiry_days integer NOT NULL DEFAULT 90 CHECK (unused_expiry_days BETWEEN 1 AND 3650),
    force_rotation boolean NOT NULL DEFAULT false,
    force_rotation_days integer NOT NULL DEFAULT 90 CHECK (force_rotation_days BETWEEN 1 AND 3650),
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO key_policy(singleton) VALUES (true) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS api_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    key_prefix text NOT NULL,
    key_hash bytea NOT NULL UNIQUE,
    permissions jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    last_used_at timestamptz,
    revoked_at timestamptz,
    rotated_from uuid REFERENCES api_keys(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS api_keys_user_idx ON api_keys (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS api_keys_prefix_idx ON api_keys (key_prefix);

CREATE TABLE IF NOT EXISTS ai_providers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    kind text NOT NULL DEFAULT 'openai_compatible',
    base_url text NOT NULL,
    api_key_encrypted text NOT NULL DEFAULT '',
    default_model text NOT NULL,
    context_window bigint NOT NULL DEFAULT 32768 CHECK (context_window BETWEEN 0 AND 262144),
    max_input_tokens bigint NOT NULL DEFAULT 32768 CHECK (max_input_tokens BETWEEN 0 AND 262144),
    max_output_tokens bigint NOT NULL DEFAULT 4096 CHECK (max_output_tokens BETWEEN 0 AND 262144),
    temperature double precision NOT NULL DEFAULT 0.7 CHECK (temperature BETWEEN 0 AND 2),
    timeout_seconds integer NOT NULL DEFAULT 120 CHECK (timeout_seconds BETWEEN 1 AND 3600),
    retries integer NOT NULL DEFAULT 1 CHECK (retries BETWEEN 0 AND 10),
    streaming boolean NOT NULL DEFAULT true,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ai_models (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id uuid NOT NULL REFERENCES ai_providers(id) ON DELETE CASCADE,
    name text NOT NULL,
    context_window bigint NOT NULL CHECK (context_window BETWEEN 0 AND 262144),
    max_input_tokens bigint NOT NULL CHECK (max_input_tokens BETWEEN 0 AND 262144),
    max_output_tokens bigint NOT NULL CHECK (max_output_tokens BETWEEN 0 AND 262144),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider_id, name)
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id bigserial PRIMARY KEY,
    actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
    actor text NOT NULL DEFAULT 'system',
    action text NOT NULL,
    resource text NOT NULL,
    resource_id text NOT NULL DEFAULT '',
    before_value jsonb,
    after_value jsonb,
    ip text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT '',
    request_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_logs_created_idx ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS audit_logs_resource_idx ON audit_logs (resource, resource_id, created_at DESC);

CREATE OR REPLACE FUNCTION prevent_audit_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit logs are append-only';
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS audit_logs_immutable ON audit_logs;
CREATE TRIGGER audit_logs_immutable BEFORE UPDATE OR DELETE ON audit_logs
FOR EACH ROW EXECUTE FUNCTION prevent_audit_mutation();

INSERT INTO permissions(key, description, category) VALUES
    ('apps:read', 'Browse applications', 'apps'),
    ('apps:write', 'Create applications', 'apps'),
    ('apps:update', 'Update owned applications', 'apps'),
    ('apps:delete', 'Delete owned applications', 'apps'),
    ('apps:submit', 'Submit applications for publication', 'apps'),
    ('apps:manage', 'Manage all applications', 'apps'),
    ('favorites:read', 'Read favorites', 'favorites'),
    ('favorites:write', 'Manage favorites', 'favorites'),
    ('reviews:read', 'Read review queue', 'reviews'),
    ('reviews:decide', 'Approve or reject applications', 'reviews'),
    ('keys:manage', 'Manage personal keys', 'keys'),
    ('ai:use', 'Use configured AI providers', 'ai'),
    ('mcp:read', 'Use read-only MCP tools', 'mcp'),
    ('mcp:execute', 'Use mutating MCP tools', 'mcp'),
    ('users:manage', 'Manage users and assignments', 'admin'),
    ('roles:manage', 'Manage roles and permissions', 'admin'),
    ('settings:read', 'Read administrative settings', 'admin'),
    ('settings:write', 'Change administrative settings', 'admin'),
    ('audit:read', 'Read audit logs', 'admin')
ON CONFLICT (key) DO NOTHING;

INSERT INTO roles(key, name, description, system) VALUES
    ('user', 'User', 'Authenticated AppStore user', true),
    ('contributor', 'Contributor', 'Can register and maintain applications', true),
    ('reviewer', 'Reviewer', 'Can review application submissions', true),
    ('team_leader', 'Team Leader', 'Can review submissions for a team', true),
    ('admin', 'Admin', 'Can operate AppStore', true),
    ('super_admin', 'Super Admin', 'Unrestricted bootstrap and recovery administrator', true)
ON CONFLICT (key) DO NOTHING;

INSERT INTO role_permissions(role_id, permission_key)
SELECT r.id, p.key FROM roles r CROSS JOIN permissions p
WHERE (r.key = 'user' AND p.key IN ('apps:read','favorites:read','favorites:write','keys:manage','mcp:read'))
   OR (r.key = 'contributor' AND p.key IN ('apps:read','apps:write','apps:update','apps:delete','apps:submit','favorites:read','favorites:write','keys:manage','ai:use','mcp:read','mcp:execute'))
   OR (r.key IN ('reviewer','team_leader') AND p.key IN ('apps:read','reviews:read','reviews:decide','mcp:read'))
   OR (r.key = 'admin')
   OR (r.key = 'super_admin')
ON CONFLICT DO NOTHING;

INSERT INTO key_permission_definitions(key, name, description) VALUES
    ('apps:read', '앱 읽기', '공개 앱 카탈로그 조회'),
    ('apps:write', '앱 등록', '새 앱 등록'),
    ('apps:update', '앱 수정', '소유한 앱 수정'),
    ('apps:submit', '앱 제출', '앱 게시 또는 검토 제출'),
    ('favorites:read', '즐겨찾기 읽기', '즐겨찾기 조회'),
    ('favorites:write', '즐겨찾기 쓰기', '즐겨찾기 변경'),
    ('ai:use', 'AI 사용', 'AI Streaming API 사용'),
    ('mcp:read', 'MCP 읽기', '읽기 전용 MCP 도구 사용'),
    ('mcp:execute', 'MCP 실행', '변경 MCP 도구 사용')
ON CONFLICT (key) DO NOTHING;

INSERT INTO key_permission_templates(name, description, permissions) VALUES
    ('Read Only', '카탈로그와 MCP 읽기', '["apps:read","mcp:read"]'::jsonb),
    ('Developer', '앱 등록과 관리', '["apps:read","apps:write","apps:update","apps:submit"]'::jsonb),
    ('AI Client', 'AI Streaming 사용', '["apps:read","ai:use"]'::jsonb),
    ('MCP Client', 'MCP 읽기 및 실행', '["apps:read","mcp:read","mcp:execute"]'::jsonb),
    ('Full Access', '사용자에게 허용된 전체 클라이언트 권한', '["apps:read","apps:write","apps:update","apps:submit","favorites:read","favorites:write","ai:use","mcp:read","mcp:execute"]'::jsonb)
ON CONFLICT (name) DO NOTHING;
