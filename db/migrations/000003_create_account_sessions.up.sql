CREATE TABLE IF NOT EXISTS account_sessions (
    account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL CHECK (revision > 0),
    status TEXT NOT NULL,
    object_key TEXT NOT NULL,
    error_code TEXT NULL,
    last_restored_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_account_sessions_status CHECK (
        status IN ('valid', 'invalid', 'unavailable')
    ),
    CONSTRAINT chk_account_sessions_valid_without_error CHECK (
        NOT (status = 'valid' AND error_code IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_account_sessions_status
    ON account_sessions (status);

CREATE INDEX IF NOT EXISTS idx_account_sessions_updated_at
    ON account_sessions (updated_at DESC);
