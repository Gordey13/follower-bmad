CREATE TABLE IF NOT EXISTS tasks (
    id UUID PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    status TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    claimed_by TEXT NULL,
    claimed_at TIMESTAMPTZ NULL,
    started_at TIMESTAMPTZ NULL,
    finished_at TIMESTAMPTZ NULL,
    error_code TEXT NULL,
    result_reason TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_tasks_status CHECK (
        status IN ('queued', 'running', 'success', 'retry', 'fail')
    ),
    CONSTRAINT chk_tasks_reason_for_terminal_failure CHECK (
        status IN ('queued', 'running', 'success')
        OR COALESCE(error_code, '') <> ''
        OR COALESCE(result_reason, '') <> ''
    )
);

CREATE INDEX IF NOT EXISTS idx_tasks_status_claimed_at
    ON tasks (status, claimed_at);

CREATE INDEX IF NOT EXISTS idx_tasks_account_status
    ON tasks (account_id, status, created_at DESC);
