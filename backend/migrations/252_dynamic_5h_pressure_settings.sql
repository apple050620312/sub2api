CREATE TABLE IF NOT EXISTS dynamic_5h_pressure_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    peak_threshold DOUBLE PRECISION NOT NULL CHECK (peak_threshold > 0 AND peak_threshold < 'Infinity'::DOUBLE PRECISION),
    normal_threshold DOUBLE PRECISION NOT NULL CHECK (normal_threshold > 0 AND normal_threshold <= peak_threshold),
    updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
