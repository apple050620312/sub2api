CREATE TABLE IF NOT EXISTS dynamic_5h_meter_events (
    id BIGSERIAL PRIMARY KEY,
    source_key TEXT NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_id TEXT NOT NULL,
    amount DOUBLE PRECISION NOT NULL CHECK (amount > 0),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_dynamic_5h_meter_pending
    ON dynamic_5h_meter_events(id) WHERE delivered_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_dynamic_5h_meter_occurred
    ON dynamic_5h_meter_events(occurred_at);
