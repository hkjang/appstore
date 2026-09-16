-- SecCheck integration. Disabled by default: turning it on never retroactively
-- unpublishes an app, it only gates what happens from then on.
CREATE TABLE IF NOT EXISTS security_check_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT false,
    base_url text NOT NULL DEFAULT '',
    api_key_encrypted text NOT NULL DEFAULT '',
    timeout_seconds integer NOT NULL DEFAULT 10 CHECK (timeout_seconds BETWEEN 1 AND 60),
    -- Bumped whenever the connection changes, so an approval read from one
    -- SecCheck deployment is not honoured after the operator points AppStore
    -- at a different one.
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO security_check_settings(singleton) VALUES (true) ON CONFLICT DO NOTHING;

-- The challenge is what ties a remote review to this exact app content: the
-- owner copies it into the review, and editing the app rotates it so an old
-- approval cannot cover new content.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS security_challenge_nonce uuid NOT NULL DEFAULT gen_random_uuid();

-- Review identifiers survive their app: a remote review is bound to one app and
-- one challenge for good, and must never be recycled for another.
CREATE TABLE IF NOT EXISTS app_security_checks (
    review_id text PRIMARY KEY,
    app_id uuid REFERENCES apps(id) ON DELETE SET NULL,
    captured_nonce uuid NOT NULL,
    settings_revision bigint NOT NULL,
    review_number text NOT NULL DEFAULT '',
    remote_status text NOT NULL,
    final_result text NOT NULL DEFAULT '',
    approved_at timestamptz,
    checked_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS app_security_checks_app_idx ON app_security_checks(app_id);
ALTER TABLE apps ADD COLUMN IF NOT EXISTS security_review_id text REFERENCES app_security_checks(review_id) ON DELETE SET NULL;
