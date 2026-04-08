-- account session metadata by ownership anchor
SELECT
    account_id,
    revision,
    status,
    object_key,
    error_code,
    last_restored_at,
    created_at,
    updated_at
FROM account_sessions
WHERE account_id = $1;

-- session status snapshot for operational metrics
SELECT
    status,
    COUNT(*)::BIGINT AS total
FROM account_sessions
WHERE status IN ('valid', 'invalid', 'unavailable')
GROUP BY status;
