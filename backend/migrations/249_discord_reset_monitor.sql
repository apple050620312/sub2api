CREATE TABLE IF NOT EXISTS service_pool_reset_monitor (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    owner_user_id BIGINT NOT NULL DEFAULT 0,
    monitor_since TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    version BIGINT NOT NULL DEFAULT 1
);
INSERT INTO service_pool_reset_monitor (id) VALUES (1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS service_pool_reset_messages (
    message_key TEXT PRIMARY KEY,
    message_id TEXT NOT NULL,
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
CREATE INDEX IF NOT EXISTS service_pool_reset_messages_time_idx ON service_pool_reset_messages (created_at DESC);

UPDATE service_pool_reset_state
SET state=state - 'gmail' - 'status',next_check_at='-infinity'
WHERE id=1;
