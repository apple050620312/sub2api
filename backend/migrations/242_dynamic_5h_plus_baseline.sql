CREATE TABLE IF NOT EXISTS dynamic_5h_plus_samples (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    reset_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    start_percent DOUBLE PRECISION NOT NULL,
    ended_at TIMESTAMPTZ,
    end_percent DOUBLE PRECISION,
    capacity DOUBLE PRECISION,
    PRIMARY KEY (account_id, reset_at)
);

CREATE TABLE IF NOT EXISTS dynamic_5h_plus_baseline (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    capacity DOUBLE PRECISION NOT NULL CHECK (capacity > 0),
    sample_count INTEGER NOT NULL,
    calibrated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
