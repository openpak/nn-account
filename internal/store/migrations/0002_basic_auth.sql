-- Basic-auth cache: stores the SHA-256 of the base64 console Basic token
-- after a successful core verification, avoiding repeated password checks.
-- Short-lived by design; the core remains the credential authority.
CREATE TABLE basic_auth_cache (
    token_hash  TEXT PRIMARY KEY,
    pid         BIGINT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL
);
