-- task queue read model
SELECT
    id,
    source_task_id,
    account_id,
    status,
    attempt,
    claimed_by,
    claimed_at,
    started_at,
    finished_at,
    error_code,
    result_reason,
    created_at,
    updated_at
FROM tasks
WHERE id = $1;

-- queued backlog slice
SELECT
    id,
    source_task_id,
    account_id,
    status,
    attempt,
    claimed_by,
    claimed_at,
    started_at,
    finished_at,
    error_code,
    result_reason,
    created_at,
    updated_at
FROM tasks
WHERE status = 'queued'
ORDER BY created_at ASC
LIMIT $1;

-- task queue snapshot for operational metrics
SELECT
    status,
    COUNT(*)::BIGINT AS total
FROM tasks
WHERE status IN ('queued', 'running', 'success', 'retry', 'fail', 'canceled')
GROUP BY status;
