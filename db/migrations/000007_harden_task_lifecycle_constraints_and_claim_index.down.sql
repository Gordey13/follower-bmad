DROP INDEX IF EXISTS idx_tasks_claim_next_queued_created_at_id;

CREATE INDEX IF NOT EXISTS idx_tasks_status_claimed_at
    ON tasks (status, claimed_at);

ALTER TABLE tasks
    DROP CONSTRAINT IF EXISTS chk_tasks_lifecycle_temporal_order;

ALTER TABLE tasks
    DROP CONSTRAINT IF EXISTS chk_tasks_lifecycle_consistency;

ALTER TABLE tasks
    ADD CONSTRAINT chk_tasks_reason_for_terminal_failure CHECK (
        status IN ('queued', 'running', 'success')
        OR COALESCE(error_code, '') <> ''
        OR COALESCE(result_reason, '') <> ''
    );
