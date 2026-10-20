ALTER TABLE service_pool_reset_state ADD COLUMN IF NOT EXISTS lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE service_pool_reset_state ADD COLUMN IF NOT EXISTS next_check_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity';

CREATE TABLE IF NOT EXISTS service_pool_reset_gmail (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    credentials_encrypted TEXT NOT NULL DEFAULT '',
    refresh_token_encrypted TEXT NOT NULL DEFAULT '',
    model_key_encrypted TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    monitor_since TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    version BIGINT NOT NULL DEFAULT 1,
    oauth_state_hash TEXT NOT NULL DEFAULT '',
    oauth_verifier_encrypted TEXT NOT NULL DEFAULT '',
    oauth_user_id BIGINT NOT NULL DEFAULT 0,
    oauth_expires_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity'
);
INSERT INTO service_pool_reset_gmail (id) VALUES (1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS service_pool_reset_mail (
    message_key TEXT PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL,
    sender TEXT NOT NULL,
    subject TEXT NOT NULL,
    kind TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    evidence TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS service_pool_reset_mail_time_idx ON service_pool_reset_mail (created_at DESC);
