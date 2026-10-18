-- Persist the guided onboarding workflow without storing OAuth bearer tokens.
CREATE TABLE IF NOT EXISTS onboarding_config (
    id SMALLINT PRIMARY KEY DEFAULT 1,
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT onboarding_config_singleton CHECK (id = 1)
);

INSERT INTO onboarding_config (id, config)
VALUES (1, '{}'::jsonb)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS onboarding_discord_bindings (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    discord_user_id TEXT NOT NULL,
    discord_username TEXT NOT NULL DEFAULT '',
    guild_id TEXT NOT NULL DEFAULT '',
    role_id TEXT NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    last_checked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS onboarding_discord_bindings_subject_idx
    ON onboarding_discord_bindings (discord_user_id);

CREATE INDEX IF NOT EXISTS onboarding_discord_bindings_status_idx
    ON onboarding_discord_bindings (status);
