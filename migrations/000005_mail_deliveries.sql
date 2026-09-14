-- Mail notifications: every attempt to send one is recorded so an
-- administrator can see what left the building. The log keeps the subject
-- and the recipient, never the body. Relay settings live in system_settings
-- under mail.* and are not seeded: a row that was never written reads as
-- "mail off", so an upgrade changes nothing.
CREATE TABLE IF NOT EXISTS mail_deliveries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event text NOT NULL,
    recipient text NOT NULL,
    subject text NOT NULL,
    reference text NOT NULL DEFAULT '',
    actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
    status text NOT NULL DEFAULT 'queued',
    attempts integer NOT NULL DEFAULT 0,
    error_message text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS mail_deliveries_created_idx ON mail_deliveries (created_at DESC, id);
CREATE INDEX IF NOT EXISTS mail_deliveries_status_idx ON mail_deliveries (status, created_at DESC);
