CREATE TABLE IF NOT EXISTS proxies (
    id UUID PRIMARY KEY,
    host TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port > 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS accounts (
    id UUID PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    proxy_id UUID NOT NULL REFERENCES proxies(id) ON DELETE RESTRICT,
    operational_state TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    is_ready BOOLEAN NOT NULL DEFAULT TRUE,
    is_quarantined BOOLEAN NOT NULL DEFAULT FALSE,
    is_restricted BOOLEAN NOT NULL DEFAULT FALSE,
    limit_reached BOOLEAN NOT NULL DEFAULT FALSE,
    active_execution_context_id TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_accounts_operational_state CHECK (
        operational_state IN (
            'active',
            'busy',
            'invalid_session',
            'quarantined',
            'restricted'
        )
    )
);

CREATE INDEX IF NOT EXISTS idx_accounts_proxy_id
    ON accounts (proxy_id);

CREATE INDEX IF NOT EXISTS idx_accounts_eligibility
    ON accounts (is_active, is_ready, is_quarantined, is_restricted, limit_reached);

CREATE UNIQUE INDEX IF NOT EXISTS ux_accounts_active_execution_context
    ON accounts (active_execution_context_id)
    WHERE active_execution_context_id IS NOT NULL;
