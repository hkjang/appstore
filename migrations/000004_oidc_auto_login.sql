-- Silent SSO: an administrator opts in to prompt=none sign-in, and each
-- in-flight authorization request remembers whether it was silent so the
-- callback can tell a refused silent attempt from a failed interactive one.
-- Both default to off, so existing installations behave exactly as before.
ALTER TABLE oidc_settings ADD COLUMN IF NOT EXISTS auto_login boolean NOT NULL DEFAULT false;
ALTER TABLE oidc_auth_requests ADD COLUMN IF NOT EXISTS silent boolean NOT NULL DEFAULT false;
