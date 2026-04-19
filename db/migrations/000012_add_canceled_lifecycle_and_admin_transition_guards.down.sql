ALTER TABLE tasks
    DROP CONSTRAINT IF EXISTS chk_tasks_lifecycle_consistency;

ALTER TABLE tasks
    ADD CONSTRAINT chk_tasks_lifecycle_consistency CHECK (
        (
            status = 'queued'
            AND attempt >= 0
            AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
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
            AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
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
            AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
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
            AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
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
    DROP CONSTRAINT IF EXISTS chk_tasks_status;

ALTER TABLE tasks
    ADD CONSTRAINT chk_tasks_status CHECK (
        status IN ('queued', 'running', 'success', 'retry', 'fail')
    );
