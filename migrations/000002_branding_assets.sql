-- Branding images live in PostgreSQL, not on the container filesystem, so an
-- uploaded logo or favicon survives replacing the service image on upgrade.
CREATE TABLE IF NOT EXISTS branding_assets (
    kind text PRIMARY KEY CHECK (kind IN ('logo', 'favicon')),
    content_type text NOT NULL,
    content bytea NOT NULL,
    checksum text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
