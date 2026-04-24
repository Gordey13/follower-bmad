package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"follower/internal/audit"
	"follower/internal/domain"
	"follower/internal/stackerr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ResultPostgresRepository struct {
	pool     *pgxpool.Pool
	auditLog *audit.Log
}

func NewResultRepository(pool *pgxpool.Pool, auditLog ...*audit.Log) *ResultPostgresRepository {
	var logger *audit.Log
	if len(auditLog) > 0 {
		logger = auditLog[0]
	}

	return &ResultPostgresRepository{
		pool:     pool,
		auditLog: logger,
	}
}

func (r *ResultPostgresRepository) Upsert(
	ctx context.Context,
	result domain.FollowResult,
) (domain.FollowResult, error) {
	if err := result.Validate(); err != nil {
		return domain.FollowResult{}, stackerr.WithStack(err)
	}

	row := r.pool.QueryRow(ctx, `
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
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, $10, $11, $12)
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
		RETURNING
			task_id,
			attempt,
			account_id,
			target_profile,
			outcome,
			verified,
			verification_signal,
			COALESCE(verification_reason, ''),
			COALESCE(error_code, ''),
			screenshot_object_key,
			artifact_object_keys,
			COALESCE(session_revision, 0),
			created_at,
			updated_at
	`,
		result.TaskID,
		result.Attempt,
		result.AccountID,
		result.TargetProfile,
		result.Outcome,
		result.Verified,
		result.VerificationSignal,
		result.VerificationReason,
		nullableErrorCode(result.ErrorCode),
		result.ScreenshotObjectKey,
		result.ArtifactObjectKeys,
		nullableInt64(result.SessionRevision),
	)

	stored, err := scanFollowResult(row)
	if err != nil {
		return domain.FollowResult{}, domain.NewDomainError(
			domain.ErrorCodeFollowResultPersistFailed,
			fmt.Sprintf("upsert follow result failed: %v", err),
		)
	}

	if r.auditLog != nil {
		_, auditErr := r.auditLog.Record(ctx, audit.Event{
			Action:        "follow.result.persisted",
			TargetType:    "follow_result",
			TargetID:      stored.TaskID.String() + ":" + strconv.Itoa(stored.Attempt),
			ChangeSummary: "follow result persisted",
			DiagnosticFields: map[string]string{
				"task_id":             stored.TaskID.String(),
				"account_id":          stored.AccountID.String(),
				"attempt":             strconv.Itoa(stored.Attempt),
				"outcome":             string(stored.Outcome),
				"verified":            strconv.FormatBool(stored.Verified),
				"verification_signal": string(stored.VerificationSignal),
				"error_code":          string(stored.ErrorCode),
			},
		})
		if auditErr != nil {
			// Domain write is already committed; audit is best-effort.
		}
	}

	return stored, nil
}

func (r *ResultPostgresRepository) GetByTaskAttempt(
	ctx context.Context,
	taskID uuid.UUID,
	attempt int,
) (domain.FollowResult, error) {
	if taskID == uuid.Nil {
		return domain.FollowResult{}, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskIdentifier,
			"task id must not be empty",
		)
	}
	if attempt <= 0 {
		return domain.FollowResult{}, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("attempt must be > 0, got %d", attempt),
		)
	}

	row := r.pool.QueryRow(ctx, `
		SELECT
			task_id,
			attempt,
			account_id,
			target_profile,
			outcome,
			verified,
			verification_signal,
			COALESCE(verification_reason, ''),
			COALESCE(error_code, ''),
			screenshot_object_key,
			artifact_object_keys,
			COALESCE(session_revision, 0),
			created_at,
			updated_at
		FROM follow_results
		WHERE task_id = $1
		  AND attempt = $2
	`, taskID, attempt)

	result, err := scanFollowResult(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FollowResult{}, domain.NewDomainError(
			domain.ErrorCodeFollowResultNotFound,
			fmt.Sprintf("follow result for task %s attempt %d not found", taskID.String(), attempt),
		)
	}
	if err != nil {
		return domain.FollowResult{}, domain.NewDomainError(
			domain.ErrorCodeFollowResultPersistFailed,
			fmt.Sprintf("get follow result failed: %v", err),
		)
	}

	return result, nil
}

func (r *ResultPostgresRepository) ListHistory(
	ctx context.Context,
	query domain.FollowResultsHistoryQuery,
) ([]domain.FollowResultHistoryEntry, error) {
	if err := query.Validate(); err != nil {
		return nil, stackerr.WithStack(err)
	}

	statement, args := buildFollowResultsHistoryStatement(query)
	rows, err := r.pool.Query(ctx, statement, args...)
	if err != nil {
		return nil, domain.NewDomainError(
			domain.ErrorCodeFollowResultPersistFailed,
			fmt.Sprintf("list follow result history failed: %v", err),
		)
	}
	defer rows.Close()

	history := make([]domain.FollowResultHistoryEntry, 0, query.Limit)
	for rows.Next() {
		entry, scanErr := scanFollowResultHistoryEntry(rows)
		if scanErr != nil {
			return nil, domain.NewDomainError(
				domain.ErrorCodeFollowResultPersistFailed,
				fmt.Sprintf("scan follow result history failed: %v", scanErr),
			)
		}
		history = append(history, entry)
	}
	if rows.Err() != nil {
		return nil, domain.NewDomainError(
			domain.ErrorCodeFollowResultPersistFailed,
			fmt.Sprintf("iterate follow result history failed: %v", rows.Err()),
		)
	}

	return history, nil
}

func scanFollowResult(row pgx.Row) (domain.FollowResult, error) {
	var result domain.FollowResult
	var targetProfile string
	var outcome string
	var signal string
	var verificationReason string
	var errorCode string
	var artifactObjectKeys []string
	var sessionRevision int64
	var createdAt time.Time
	var updatedAt time.Time

	err := row.Scan(
		&result.TaskID,
		&result.Attempt,
		&result.AccountID,
		&targetProfile,
		&outcome,
		&result.Verified,
		&signal,
		&verificationReason,
		&errorCode,
		&result.ScreenshotObjectKey,
		&artifactObjectKeys,
		&sessionRevision,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return domain.FollowResult{}, stackerr.WithStack(err)
	}

	result.TargetProfile = domain.TargetProfileDescriptor(targetProfile)
	result.Outcome = domain.FollowFlowOutcome(outcome)
	result.VerificationSignal = domain.FollowVerificationSignal(signal)
	result.VerificationReason = strings.TrimSpace(verificationReason)
	result.ErrorCode = domain.ErrorCode(errorCode)
	result.ArtifactObjectKeys = artifactObjectKeys
	result.SessionRevision = sessionRevision
	result.CreatedAt = createdAt
	result.UpdatedAt = updatedAt

	return result, nil
}

func scanFollowResultHistoryEntry(row pgx.Row) (domain.FollowResultHistoryEntry, error) {
	var entry domain.FollowResultHistoryEntry
	var targetProfile string
	var taskStatus string
	var followOutcome string
	var verificationSignal string
	var errorCode string
	var resultReason string

	err := row.Scan(
		&entry.TaskID,
		&entry.AccountID,
		&targetProfile,
		&entry.Attempt,
		&taskStatus,
		&followOutcome,
		&entry.Verified,
		&verificationSignal,
		&errorCode,
		&resultReason,
		&entry.CreatedAt,
		&entry.UpdatedAt,
	)
	if err != nil {
		return domain.FollowResultHistoryEntry{}, stackerr.WithStack(err)
	}

	entry.TargetProfile = domain.TargetProfileDescriptor(targetProfile)
	entry.TaskStatus = domain.TaskStatus(taskStatus)
	entry.FollowOutcome = domain.FollowFlowOutcome(followOutcome)
	entry.VerificationSignal = domain.FollowVerificationSignal(verificationSignal)
	entry.ErrorCode = domain.ErrorCode(errorCode)
	entry.ResultReason = strings.TrimSpace(resultReason)

	return entry, nil
}

func buildFollowResultsHistoryStatement(query domain.FollowResultsHistoryQuery) (string, []interface{}) {
	var builder strings.Builder
	args := make([]interface{}, 0, 8)
	nextArg := func(value interface{}) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	builder.WriteString(`
		SELECT
			fr.task_id,
			fr.account_id,
			fr.target_profile,
			fr.attempt,
			t.status,
			fr.outcome,
			fr.verified,
			fr.verification_signal,
			COALESCE(fr.error_code, ''),
			COALESCE(t.result_reason, ''),
			fr.created_at,
			fr.updated_at
		FROM follow_results fr
		INNER JOIN tasks t ON t.id = fr.task_id
		WHERE 1=1
	`)

	if query.AccountID != uuid.Nil {
		builder.WriteString(" AND fr.account_id = ")
		builder.WriteString(nextArg(query.AccountID))
	}
	if strings.TrimSpace(string(query.TargetProfile)) != "" {
		builder.WriteString(" AND fr.target_profile = ")
		builder.WriteString(nextArg(query.TargetProfile))
	}
	if query.Outcome != "" {
		builder.WriteString(" AND fr.outcome = ")
		builder.WriteString(nextArg(query.Outcome))
	}
	if query.TaskStatus != "" {
		builder.WriteString(" AND t.status = ")
		builder.WriteString(nextArg(query.TaskStatus))
	}
	if query.From != nil {
		builder.WriteString(" AND fr.created_at >= ")
		builder.WriteString(nextArg(*query.From))
	}
	if query.To != nil {
		builder.WriteString(" AND fr.created_at <= ")
		builder.WriteString(nextArg(*query.To))
	}

	builder.WriteString(" ORDER BY fr.created_at DESC, fr.task_id DESC, fr.attempt DESC")
	builder.WriteString(" LIMIT ")
	builder.WriteString(nextArg(query.Limit))
	builder.WriteString(" OFFSET ")
	builder.WriteString(nextArg(query.Offset))

	return builder.String(), args
}

func nullableInt64(value int64) interface{} {
	if value == 0 {
		return nil
	}
	return value
}
