ALTER TABLE tasks
    DROP CONSTRAINT IF EXISTS chk_tasks_reason_for_terminal_failure;

ALTER TABLE tasks
    ADD CONSTRAINT chk_tasks_lifecycle_consistency CHECK (
        (
            status = 'queued'
            AND attempt >= 0
            AND claimed_by IS NULL
            AND claimed_at IS NULL
            AND started_at IS NULL
            AND finished_at IS NULL
            AND error_code IS NULL
            AND result_reason IS NULL
        )
        OR (
            status = 'running'
            AND attempt > 0
            AND claimed_by IS NOT NULL
            AND BTRIM(claimed_by) <> ''
            AND claimed_at IS NOT NULL
            AND started_at IS NOT NULL
            AND finished_at IS NULL
            AND error_code IS NULL
            AND result_reason IS NULL
        )
        OR (
            status = 'success'
            AND attempt > 0
            AND claimed_by IS NOT NULL
            AND BTRIM(claimed_by) <> ''
            AND claimed_at IS NOT NULL
            AND started_at IS NOT NULL
            AND finished_at IS NOT NULL
            AND error_code IS NULL
            AND result_reason IS NULL
        )
        OR (
            status IN ('retry', 'fail')
            AND attempt > 0
            AND claimed_by IS NOT NULL
            AND BTRIM(claimed_by) <> ''
            AND claimed_at IS NOT NULL
            AND started_at IS NOT NULL
            AND finished_at IS NOT NULL
            AND (
                NULLIF(BTRIM(error_code), '') IS NOT NULL
                OR NULLIF(BTRIM(result_reason), '') IS NOT NULL
            )
        )
    );

ALTER TABLE tasks
    ADD CONSTRAINT chk_tasks_lifecycle_temporal_order CHECK (
        (claimed_at IS NULL OR started_at IS NULL OR claimed_at <= started_at)
        AND (started_at IS NULL OR finished_at IS NULL OR started_at <= finished_at)
    );

DROP INDEX IF EXISTS idx_tasks_status_claimed_at;

CREATE INDEX IF NOT EXISTS idx_tasks_claim_next_queued_created_at_id
    ON tasks (created_at, id)
    WHERE status = 'queued';
