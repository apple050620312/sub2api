CREATE TABLE IF NOT EXISTS service_pool_reset_state (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    state JSONB NOT NULL DEFAULT '{}'::jsonb,
    lease_until TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO service_pool_reset_state (id) VALUES (1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS service_pool_reset_events (
    id TEXT PRIMARY KEY,
    event_key TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL,
    observed_accounts INTEGER NOT NULL,
    total_accounts INTEGER NOT NULL,
    affected_subscriptions INTEGER NOT NULL DEFAULT 0,
    reset_at TIMESTAMPTZ NOT NULL,
    notification_channel_id TEXT NOT NULL DEFAULT '',
    notification_content TEXT NOT NULL DEFAULT '',
    notification_state TEXT NOT NULL DEFAULT 'pending',
    notification_message_id TEXT NOT NULL DEFAULT '',
    notification_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS service_pool_reset_events_time_idx ON service_pool_reset_events (reset_at DESC);

CREATE TABLE IF NOT EXISTS service_pool_reset_cache_targets (
    event_id TEXT NOT NULL REFERENCES service_pool_reset_events(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    PRIMARY KEY (event_id, user_id, group_id)
);
