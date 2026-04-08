-- account-proxy ownership read model
SELECT
    a.id,
    a.username,
    a.proxy_id,
    a.operational_state,
    a.is_active,
    a.is_ready,
    a.is_quarantined,
    a.is_restricted,
    a.limit_reached,
    a.active_execution_context_id,
    a.created_at,
    a.updated_at,
    p.id,
    p.host,
    p.port,
    p.is_active,
    p.created_at,
    p.updated_at
FROM accounts a
JOIN proxies p ON p.id = a.proxy_id
WHERE a.id = $1;

-- account operational state snapshot for operational metrics
SELECT
    operational_state,
    COUNT(*)::BIGINT AS total
FROM accounts
WHERE operational_state IN ('active', 'busy', 'invalid_session', 'quarantined', 'restricted')
GROUP BY operational_state;
