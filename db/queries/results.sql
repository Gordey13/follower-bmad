-- follow_results queries (Story 2.4)

-- name: UpsertFollowResult :one
INSERT INTO follow_results (
    task_id,
    attempt,
    account_id,
    target_profile,
    outcome,
    verified,
    verification_signal,
    verification_reason,
    error_code,
    screenshot_object_key,
    artifact_object_keys,
    session_revision
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, $10, $11, $12
)
ON CONFLICT (task_id, attempt) DO UPDATE
SET account_id = EXCLUDED.account_id,
    target_profile = EXCLUDED.target_profile,
    outcome = EXCLUDED.outcome,
    verified = EXCLUDED.verified,
    verification_signal = EXCLUDED.verification_signal,
    verification_reason = EXCLUDED.verification_reason,
    error_code = EXCLUDED.error_code,
    screenshot_object_key = EXCLUDED.screenshot_object_key,
    artifact_object_keys = EXCLUDED.artifact_object_keys,
    session_revision = EXCLUDED.session_revision,
    updated_at = NOW()
RETURNING *;

-- name: GetFollowResultByTaskAttempt :one
SELECT *
FROM follow_results
WHERE task_id = $1
  AND attempt = $2;

-- name: ListFollowResultHistory :many
SELECT
    fr.task_id,
    fr.account_id,
    fr.target_profile,
    fr.attempt,
    t.status,
    fr.outcome,
    fr.verified,
    fr.verification_signal,
    COALESCE(fr.error_code, '') AS error_code,
    COALESCE(t.result_reason, '') AS result_reason,
    fr.created_at,
    fr.updated_at
FROM follow_results fr
INNER JOIN tasks t ON t.id = fr.task_id
WHERE 1 = 1
ORDER BY fr.created_at DESC, fr.task_id DESC, fr.attempt DESC;
