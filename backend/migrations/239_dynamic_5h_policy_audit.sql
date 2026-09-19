CREATE TABLE IF NOT EXISTS dynamic_5h_user_policies (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    multiplier DOUBLE PRECISION NOT NULL DEFAULT 1 CHECK (multiplier > 0),
    exempt BOOLEAN NOT NULL DEFAULT FALSE,
    updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dynamic_5h_audit_logs (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(32) NOT NULL,
    reason VARCHAR(500) NOT NULL DEFAULT '',
    before_usage DOUBLE PRECISION,
    after_usage DOUBLE PRECISION,
    old_policy JSONB,
    new_policy JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dynamic_5h_audit_user_created
    ON dynamic_5h_audit_logs(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_dynamic_5h_audit_created
    ON dynamic_5h_audit_logs(created_at DESC);
