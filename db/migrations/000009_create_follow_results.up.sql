CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_id_account
    ON tasks (id, account_id);

CREATE TABLE IF NOT EXISTS follow_results (
    task_id UUID NOT NULL,
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    target_profile TEXT NOT NULL,
    outcome TEXT NOT NULL,
    verified BOOLEAN NOT NULL,
    verification_signal TEXT NOT NULL,
    verification_reason TEXT NULL,
    error_code TEXT NULL,
    screenshot_object_key TEXT NOT NULL,
    artifact_object_keys TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    session_revision BIGINT NULL CHECK (session_revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT pk_follow_results PRIMARY KEY (task_id, attempt),
    CONSTRAINT fk_follow_results_task_account FOREIGN KEY (task_id, account_id)
        REFERENCES tasks(id, account_id) ON DELETE CASCADE,
    CONSTRAINT chk_follow_results_target_profile_nonempty CHECK (
        NULLIF(BTRIM(target_profile), '') IS NOT NULL
    ),
    CONSTRAINT chk_follow_results_outcome CHECK (
        outcome IN (
            'follow_completed',
            'follow_already_done',
            'follow_action_unavailable',
            'follow_target_unreachable',
            'follow_navigation_failed'
        )
    ),
    CONSTRAINT chk_follow_results_signal CHECK (
        verification_signal IN (
            'follow_confirmed',
            'follow_already_done',
            'follow_action_unavailable',
            'follow_target_unreachable',
            'follow_navigation_failed',
            'follow_verify_failed'
        )
    ),
    CONSTRAINT chk_follow_results_verified_vs_error CHECK (
        (verified = TRUE AND error_code IS NULL)
        OR (verified = FALSE AND NULLIF(BTRIM(COALESCE(error_code, '')), '') IS NOT NULL)
    ),
    CONSTRAINT chk_follow_results_screenshot_nonempty CHECK (
        NULLIF(BTRIM(screenshot_object_key), '') IS NOT NULL
    ),
    CONSTRAINT chk_follow_results_artifacts_nonempty CHECK (
        CARDINALITY(artifact_object_keys) > 0
    )
);

CREATE INDEX IF NOT EXISTS idx_follow_results_account_created
    ON follow_results (account_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_follow_results_outcome_created
    ON follow_results (outcome, created_at DESC);
