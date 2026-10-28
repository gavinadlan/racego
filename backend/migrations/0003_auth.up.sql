-- Sesi login (cookie racego_session). Yang disimpan hanya SHA-256 token, bukan token-nya.
CREATE TABLE sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    bytea NOT NULL UNIQUE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    user_agent    text,
    ip            text
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
