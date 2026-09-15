-- Guide documents live in PostgreSQL next to the app they describe, so an
-- uploaded manual survives replacing the service image the same way branding
-- does. Version 000005 is reserved by the mail delivery branch.
CREATE TABLE IF NOT EXISTS app_documents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    title text NOT NULL DEFAULT '',
    file_name text NOT NULL,
    content_type text NOT NULL,
    content bytea NOT NULL,
    size integer NOT NULL,
    checksum text NOT NULL,
    uploaded_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS app_documents_app_idx ON app_documents (app_id, created_at);
-- One name per app keeps a double-submitted upload from quietly landing twice.
CREATE UNIQUE INDEX IF NOT EXISTS app_documents_app_file_idx
    ON app_documents (app_id, lower(file_name));
