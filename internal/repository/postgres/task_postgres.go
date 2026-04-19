package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"follower/internal/audit"
	"follower/internal/domain"
	"follower/internal/observability"
	"follower/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TaskPostgresRepository struct {
	pool     *pgxpool.Pool
	auditLog *audit.Log
}

const defaultAdminCancelReason = "task canceled by admin operator"

func NewTaskRepository(pool *pgxpool.Pool, auditLog ...*audit.Log) *TaskPostgresRepository {
	var logger *audit.Log
	if len(auditLog) > 0 {
		logger = auditLog[0]
	}

	return &TaskPostgresRepository{
		pool:     pool,
		auditLog: logger,
	}
}

func (r *TaskPostgresRepository) Enqueue(ctx context.Context, task domain.Task) (domain.Task, error) {
	if task.ID == uuid.Nil {
		task.ID = uuid.New()
	}
	if task.Status == "" {
		task.Status = domain.TaskStatusQueued
	}
	if task.Status != domain.TaskStatusQueued {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("enqueue supports only queued status, got %s", task.Status),
		)
	}
	if err := task.Validate(); err != nil {
		return domain.Task{}, err
	}
	sourceTaskID := uuid.Nil
	if task.SourceTaskID != nil {
		sourceTaskID = *task.SourceTaskID
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO tasks (
			id,
			source_task_id,
			account_id,
			target_profile,
			status,
			attempt,
			claimed_by,
			claimed_at,
			started_at,
			finished_at,
			error_code,
			result_reason
		) VALUES ($1, $2, $3, $4, $5, $6, NULL, NULL, NULL, NULL, NULL, NULL)
		RETURNING
			id,
			source_task_id,
			account_id,
			target_profile,
			status,
			attempt,
			COALESCE(claimed_by, ''),
			claimed_at,
			started_at,
			finished_at,
			COALESCE(error_code, ''),
			COALESCE(result_reason, ''),
			created_at,
			updated_at
	`, task.ID, nullableUUID(sourceTaskID), task.AccountID, task.TargetProfile, task.Status, task.Attempt)

	return scanTask(row)
}

func (r *TaskPostgresRepository) EnqueueValidatedBatch(
	ctx context.Context,
	rows []repository.EnqueueValidatedRow,
) (repository.EnqueueValidatedBatchResult, error) {
	result := repository.EnqueueValidatedBatchResult{
		SkippedRows: make([]repository.EnqueueSkippedRow, 0),
	}
	if len(rows) == 0 {
		return result, nil
	}

	rowIndexes := make([]int32, 0, len(rows))
	taskIDs := make([]uuid.UUID, 0, len(rows))
	accountIDs := make([]uuid.UUID, 0, len(rows))
	targetProfiles := make([]string, 0, len(rows))

	for _, inputRow := range rows {
		if inputRow.Row <= 0 {
			return repository.EnqueueValidatedBatchResult{}, domain.NewDomainError(
				domain.ErrorCodeInvalidTaskTransition,
				"batch enqueue row index must be greater than zero",
			)
		}
		if inputRow.AccountID == uuid.Nil {
			return repository.EnqueueValidatedBatchResult{}, domain.NewDomainError(
				domain.ErrorCodeInvalidAccountIdentifier,
				"batch enqueue account_id must not be empty",
			)
		}

		rowIndexes = append(rowIndexes, int32(inputRow.Row))
		taskIDs = append(taskIDs, uuid.New())
		accountIDs = append(accountIDs, inputRow.AccountID)
		targetProfiles = append(targetProfiles, strings.TrimSpace(inputRow.TargetProfile))
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return repository.EnqueueValidatedBatchResult{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("begin batch enqueue transaction failed: %v", err),
		)
	}
	defer tx.Rollback(ctx)

	rowsCursor, err := tx.Query(ctx, `
		WITH input_rows AS (
			SELECT
				row_index::INT AS row_index,
				task_id::UUID AS task_id,
				account_id::UUID AS account_id,
				BTRIM(target_profile)::TEXT AS target_profile
			FROM UNNEST($1::INT[], $2::UUID[], $3::UUID[], $4::TEXT[]) AS t(row_index, task_id, account_id, target_profile)
		),
		payload_ranked AS (
			SELECT
				ir.row_index,
				ir.task_id,
				ir.account_id,
				ir.target_profile,
				ROW_NUMBER() OVER (
					PARTITION BY ir.account_id, ir.target_profile
					ORDER BY ir.row_index, ir.task_id
				) AS payload_rank
			FROM input_rows ir
		),
		payload_duplicates AS (
			SELECT
				pr.row_index,
				'duplicate_active_task'::TEXT AS reason_code
			FROM payload_ranked pr
			WHERE pr.payload_rank > 1
		),
		payload_unique AS (
			SELECT
				pr.row_index,
				pr.task_id,
				pr.account_id,
				pr.target_profile
			FROM payload_ranked pr
			WHERE pr.payload_rank = 1
		),
		missing_accounts AS (
			SELECT
				pu.row_index,
				'account_not_found'::TEXT AS reason_code
			FROM payload_unique pu
			LEFT JOIN accounts a ON a.id = pu.account_id
			WHERE a.id IS NULL
		),
		active_duplicates AS (
			SELECT
				pu.row_index,
				'duplicate_active_task'::TEXT AS reason_code
			FROM payload_unique pu
			INNER JOIN tasks t
				ON t.account_id = pu.account_id
				AND t.target_profile = pu.target_profile
				AND t.status IN ('queued', 'running')
		),
		insertable AS (
			SELECT
				pu.row_index,
				pu.task_id,
				pu.account_id,
				pu.target_profile
			FROM payload_unique pu
			LEFT JOIN missing_accounts ma ON ma.row_index = pu.row_index
			LEFT JOIN active_duplicates ad ON ad.row_index = pu.row_index
			WHERE ma.row_index IS NULL
				AND ad.row_index IS NULL
		),
		inserted AS (
			INSERT INTO tasks (
				id,
				source_task_id,
				account_id,
				target_profile,
				status,
				attempt,
				claimed_by,
				claimed_at,
				started_at,
				finished_at,
				error_code,
				result_reason
			)
			SELECT
				i.task_id,
				NULL,
				i.account_id,
				i.target_profile,
				'queued',
				0,
				NULL,
				NULL,
				NULL,
				NULL,
				NULL,
				NULL
			FROM insertable i
			RETURNING id
		)
		SELECT
			'created'::TEXT AS kind,
			i.row_index,
			''::TEXT AS reason_code
		FROM insertable i
		INNER JOIN inserted ins ON ins.id = i.task_id

		UNION ALL

		SELECT
			'skipped'::TEXT AS kind,
			pd.row_index,
			pd.reason_code
		FROM payload_duplicates pd

		UNION ALL

		SELECT
			'skipped'::TEXT AS kind,
			ma.row_index,
			ma.reason_code
		FROM missing_accounts ma

		UNION ALL

		SELECT
			'skipped'::TEXT AS kind,
			ad.row_index,
			ad.reason_code
		FROM active_duplicates ad

		ORDER BY row_index, kind
	`, rowIndexes, taskIDs, accountIDs, targetProfiles)
	if err != nil {
		return repository.EnqueueValidatedBatchResult{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("batch enqueue execution failed: %v", err),
		)
	}
	defer rowsCursor.Close()

	for rowsCursor.Next() {
		var kind string
		var rowIndex int
		var reasonCode string
		if err := rowsCursor.Scan(&kind, &rowIndex, &reasonCode); err != nil {
			return repository.EnqueueValidatedBatchResult{}, domain.NewDomainError(
				domain.ErrorCodeTaskQueueWriteFailed,
				fmt.Sprintf("batch enqueue result scan failed: %v", err),
			)
		}

		switch kind {
		case "created":
			result.RowsCreated++
		case "skipped":
			result.SkippedRows = append(result.SkippedRows, repository.EnqueueSkippedRow{
				Row:     rowIndex,
				Code:    reasonCode,
				Message: enqueueSkipReasonMessage(reasonCode),
			})
		}
	}
	if err := rowsCursor.Err(); err != nil {
		return repository.EnqueueValidatedBatchResult{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("batch enqueue rows iteration failed: %v", err),
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return repository.EnqueueValidatedBatchResult{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("commit batch enqueue transaction failed: %v", err),
		)
	}

	result.RowsSkipped = len(result.SkippedRows)
	return result, nil
}

func (r *TaskPostgresRepository) GetByID(ctx context.Context, taskID uuid.UUID) (domain.Task, error) {
	if taskID == uuid.Nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskIdentifier,
			"task id must not be empty",
		)
	}

	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			source_task_id,
			account_id,
			target_profile,
			status,
			attempt,
			COALESCE(claimed_by, ''),
			claimed_at,
			started_at,
			finished_at,
			COALESCE(error_code, ''),
			COALESCE(result_reason, ''),
			created_at,
			updated_at
		FROM tasks
		WHERE id = $1
	`, taskID)

	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskNotFound,
			fmt.Sprintf("task %s not found", taskID.String()),
		)
	}

	return task, err
}

func (r *TaskPostgresRepository) ListFailures(
	ctx context.Context,
	limit int,
	offset int,
) ([]domain.Task, error) {
	if limit <= 0 {
		return nil, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("failures limit must be > 0, got %d", limit),
		)
	}
	if offset < 0 {
		return nil, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("failures offset must be >= 0, got %d", offset),
		)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			source_task_id,
			account_id,
			target_profile,
			status,
			attempt,
			COALESCE(claimed_by, ''),
			claimed_at,
			started_at,
			finished_at,
			COALESCE(error_code, ''),
			COALESCE(result_reason, ''),
			created_at,
			updated_at
		FROM tasks
		WHERE status IN ('fail', 'retry')
		ORDER BY updated_at DESC, id DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := make([]domain.Task, 0, limit)
	for rows.Next() {
		var task domain.Task
		var targetProfile string
		var status string
		var claimedAt *time.Time
		var startedAt *time.Time
		var finishedAt *time.Time
		var errorCode string
		var resultReason string

		if scanErr := rows.Scan(
			&task.ID,
			&task.SourceTaskID,
			&task.AccountID,
			&targetProfile,
			&status,
			&task.Attempt,
			&task.ClaimedBy,
			&claimedAt,
			&startedAt,
			&finishedAt,
			&errorCode,
			&resultReason,
			&task.CreatedAt,
			&task.UpdatedAt,
		); scanErr != nil {
			return nil, scanErr
		}

		task.TargetProfile = domain.TargetProfileDescriptor(targetProfile)
		task.Status = domain.TaskStatus(status)
		task.ClaimedAt = claimedAt
		task.StartedAt = startedAt
		task.FinishedAt = finishedAt
		task.ErrorCode = domain.ErrorCode(errorCode)
		task.ResultReason = resultReason

		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tasks, nil
}

func (r *TaskPostgresRepository) ClaimNextQueued(
	ctx context.Context,
	claimedBy string,
) (domain.Task, bool, error) {
	if strings.TrimSpace(claimedBy) == "" {
		return domain.Task{}, false, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskClaimedBy,
			"claimed_by must not be empty",
		)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Task{}, false, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		WITH next_task AS (
			SELECT id
			FROM tasks
			WHERE status = 'queued'
			ORDER BY created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE tasks t
		SET status = 'running',
			claimed_by = $1,
			claimed_at = NOW(),
			started_at = NOW(),
			attempt = t.attempt + 1,
			updated_at = NOW()
		FROM next_task
		WHERE t.id = next_task.id
		RETURNING
			t.id,
			t.source_task_id,
			t.account_id,
			t.target_profile,
			t.status,
			t.attempt,
			COALESCE(t.claimed_by, ''),
			t.claimed_at,
			t.started_at,
			t.finished_at,
			COALESCE(t.error_code, ''),
			COALESCE(t.result_reason, ''),
			t.created_at,
			t.updated_at
	`, claimedBy)

	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return domain.Task{}, false, commitErr
		}
		return domain.Task{}, false, nil
	}
	if err != nil {
		return domain.Task{}, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Task{}, false, err
	}

	r.recordClaimedAudit(ctx, task)
	return task, true, nil
}

func (r *TaskPostgresRepository) Complete(
	ctx context.Context,
	taskID uuid.UUID,
	claimedBy string,
	finalStatus domain.TaskStatus,
	errorCode domain.ErrorCode,
	resultReason string,
) (domain.Task, error) {
	if taskID == uuid.Nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskIdentifier,
			"task id must not be empty",
		)
	}
	if strings.TrimSpace(claimedBy) == "" {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskClaimedBy,
			"claimed_by must not be empty",
		)
	}

	currentTask, err := r.GetByID(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	if currentTask.ClaimedBy != claimedBy {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskClaimOwnerMismatch,
			fmt.Sprintf(
				"task %s is claimed by %s, not %s",
				taskID.String(),
				currentTask.ClaimedBy,
				claimedBy,
			),
		)
	}
	if err := domain.ValidateTaskCompletion(currentTask.Status, finalStatus, errorCode, resultReason); err != nil {
		return domain.Task{}, err
	}

	if finalStatus == domain.TaskStatusSuccess {
		errorCode = ""
		resultReason = ""
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE tasks
		SET status = $3,
			finished_at = NOW(),
			error_code = $4,
			result_reason = $5,
			updated_at = NOW()
		WHERE id = $1
		AND claimed_by = $2
		AND status = 'running'
		RETURNING
			id,
			source_task_id,
			account_id,
			target_profile,
			status,
			attempt,
			COALESCE(claimed_by, ''),
			claimed_at,
			started_at,
			finished_at,
			COALESCE(error_code, ''),
			COALESCE(result_reason, ''),
			created_at,
			updated_at
	`, taskID, claimedBy, finalStatus, nullableErrorCode(errorCode), nullableString(resultReason))

	completedTask, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		latestTask, latestErr := r.GetByID(ctx, taskID)
		if latestErr != nil {
			return domain.Task{}, latestErr
		}
		if latestTask.ClaimedBy != claimedBy {
			return domain.Task{}, domain.NewDomainError(
				domain.ErrorCodeTaskClaimOwnerMismatch,
				fmt.Sprintf(
					"task %s is claimed by %s, not %s",
					taskID.String(),
					latestTask.ClaimedBy,
					claimedBy,
				),
			)
		}
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskNotRunning,
			fmt.Sprintf("task %s must be running to complete, got %s", taskID.String(), latestTask.Status),
		)
	}
	if err != nil {
		return domain.Task{}, err
	}

	r.recordCompletedAudit(ctx, completedTask)
	return completedTask, nil
}

func (r *TaskPostgresRepository) ApplyAdminTransition(
	ctx context.Context,
	taskID uuid.UUID,
	action domain.TaskAdminAction,
) (domain.Task, error) {
	return r.applyAdminTransition(ctx, taskID, action, "")
}

func (r *TaskPostgresRepository) CancelTask(
	ctx context.Context,
	taskID uuid.UUID,
	reason string,
) (domain.Task, error) {
	return r.applyAdminTransition(ctx, taskID, domain.TaskAdminActionCancel, reason)
}

func (r *TaskPostgresRepository) applyAdminTransition(
	ctx context.Context,
	taskID uuid.UUID,
	action domain.TaskAdminAction,
	cancelReason string,
) (domain.Task, error) {
	if taskID == uuid.Nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskIdentifier,
			"task id must not be empty",
		)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("begin admin transition transaction failed: %v", err),
		)
	}
	defer tx.Rollback(ctx)

	lockedTask, err := scanTask(tx.QueryRow(ctx, `
		SELECT
			id,
			source_task_id,
			account_id,
			target_profile,
			status,
			attempt,
			COALESCE(claimed_by, ''),
			claimed_at,
			started_at,
			finished_at,
			COALESCE(error_code, ''),
			COALESCE(result_reason, ''),
			created_at,
			updated_at
		FROM tasks
		WHERE id = $1
		FOR UPDATE
	`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskNotFound,
			fmt.Sprintf("task %s not found", taskID.String()),
		)
	}
	if err != nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("lock task for admin transition failed: %v", err),
		)
	}

	transition, err := domain.EvaluateTaskAdminTransition(lockedTask.Status, action)
	if err != nil {
		return domain.Task{}, err
	}
	normalizedCancelReason := ""
	if transition.Action == domain.TaskAdminActionCancel {
		normalizedCancelReason = normalizeAdminCancelReason(cancelReason)
	}

	updatedTask, err := scanTask(tx.QueryRow(ctx, `
		UPDATE tasks
		SET
			status = $2,
			claimed_by = NULL,
			claimed_at = NULL,
			started_at = NULL,
			finished_at = CASE
				WHEN $2 = 'canceled' THEN NOW()
				ELSE NULL
			END,
			error_code = NULL,
			result_reason = CASE
				WHEN $2 = 'canceled' THEN $3
				ELSE NULL
			END,
			updated_at = NOW()
		WHERE id = $1
		RETURNING
			id,
			source_task_id,
			account_id,
			target_profile,
			status,
			attempt,
			COALESCE(claimed_by, ''),
			claimed_at,
			started_at,
			finished_at,
			COALESCE(error_code, ''),
			COALESCE(result_reason, ''),
			created_at,
			updated_at
	`, taskID, transition.ToStatus, nullableString(normalizedCancelReason)))
	if err != nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("persist admin transition failed: %v", err),
		)
	}

	if err := appendAdminTransitionAuditLog(ctx, tx, lockedTask, updatedTask, transition); err != nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("append admin transition audit failed: %v", err),
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("commit admin transition transaction failed: %v", err),
		)
	}

	return updatedTask, nil
}

func (r *TaskPostgresRepository) RetryFromTask(
	ctx context.Context,
	sourceTaskID uuid.UUID,
) (domain.Task, error) {
	if sourceTaskID == uuid.Nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeInvalidTaskIdentifier,
			"source task id must not be empty",
		)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("begin retry transaction failed: %v", err),
		)
	}
	defer tx.Rollback(ctx)

	sourceTask, err := scanTask(tx.QueryRow(ctx, `
		SELECT
			id,
			source_task_id,
			account_id,
			target_profile,
			status,
			attempt,
			COALESCE(claimed_by, ''),
			claimed_at,
			started_at,
			finished_at,
			COALESCE(error_code, ''),
			COALESCE(result_reason, ''),
			created_at,
			updated_at
		FROM tasks
		WHERE id = $1
		FOR UPDATE
	`, sourceTaskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskNotFound,
			fmt.Sprintf("task %s not found", sourceTaskID.String()),
		)
	}
	if err != nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("lock source task for retry failed: %v", err),
		)
	}

	if _, err := domain.EvaluateTaskAdminTransition(sourceTask.Status, domain.TaskAdminActionRetry); err != nil {
		return domain.Task{}, err
	}

	newTaskID := uuid.New()
	retriedTask, err := scanTask(tx.QueryRow(ctx, `
		INSERT INTO tasks (
			id,
			source_task_id,
			account_id,
			target_profile,
			status,
			attempt,
			claimed_by,
			claimed_at,
			started_at,
			finished_at,
			error_code,
			result_reason
		) VALUES (
			$1,
			$2,
			$3,
			$4,
			'queued',
			0,
			NULL,
			NULL,
			NULL,
			NULL,
			NULL,
			NULL
		)
		RETURNING
			id,
			source_task_id,
			account_id,
			target_profile,
			status,
			attempt,
			COALESCE(claimed_by, ''),
			claimed_at,
			started_at,
			finished_at,
			COALESCE(error_code, ''),
			COALESCE(result_reason, ''),
			created_at,
			updated_at
	`, newTaskID, sourceTask.ID, sourceTask.AccountID, sourceTask.TargetProfile))
	if err != nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("insert retry task failed: %v", err),
		)
	}

	if err := appendTaskRetryAuditLog(ctx, tx, sourceTask, retriedTask); err != nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("append retry audit failed: %v", err),
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Task{}, domain.NewDomainError(
			domain.ErrorCodeTaskQueueWriteFailed,
			fmt.Sprintf("commit retry transaction failed: %v", err),
		)
	}

	return retriedTask, nil
}

func (r *TaskPostgresRepository) TaskQueueSnapshot(ctx context.Context) (map[domain.TaskStatus]int64, error) {
	snapshot := map[domain.TaskStatus]int64{
		domain.TaskStatusQueued:   0,
		domain.TaskStatusRunning:  0,
		domain.TaskStatusSuccess:  0,
		domain.TaskStatusRetry:    0,
		domain.TaskStatusFail:     0,
		domain.TaskStatusCanceled: 0,
	}

	rows, err := r.pool.Query(ctx, `
		SELECT status, COUNT(*)::BIGINT
		FROM tasks
		WHERE status IN ('queued', 'running', 'success', 'retry', 'fail', 'canceled')
		GROUP BY status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}

		normalizedStatus := domain.TaskStatus(status)
		if !normalizedStatus.IsValid() {
			continue
		}
		snapshot[normalizedStatus] = count
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return snapshot, nil
}

func scanTask(row pgx.Row) (domain.Task, error) {
	var task domain.Task
	var sourceTaskID *uuid.UUID
	var targetProfile string
	var status string
	var claimedAt *time.Time
	var startedAt *time.Time
	var finishedAt *time.Time
	var errorCode string
	var resultReason string

	err := row.Scan(
		&task.ID,
		&sourceTaskID,
		&task.AccountID,
		&targetProfile,
		&status,
		&task.Attempt,
		&task.ClaimedBy,
		&claimedAt,
		&startedAt,
		&finishedAt,
		&errorCode,
		&resultReason,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
	if err != nil {
		return domain.Task{}, err
	}

	task.SourceTaskID = sourceTaskID
	task.TargetProfile = domain.TargetProfileDescriptor(targetProfile)
	task.Status = domain.TaskStatus(status)
	task.ClaimedAt = claimedAt
	task.StartedAt = startedAt
	task.FinishedAt = finishedAt
	task.ErrorCode = domain.ErrorCode(errorCode)
	task.ResultReason = resultReason

	return task, nil
}

func enqueueSkipReasonMessage(reasonCode string) string {
	switch strings.TrimSpace(reasonCode) {
	case "duplicate_active_task":
		return "active queued/running task already exists for account_id+target_profile"
	case "account_not_found":
		return "account_id does not exist"
	default:
		return "row skipped by queue import policy"
	}
}

func normalizeAdminCancelReason(reason string) string {
	normalized := strings.TrimSpace(reason)
	if normalized == "" {
		return defaultAdminCancelReason
	}
	return normalized
}

func (r *TaskPostgresRepository) recordClaimedAudit(ctx context.Context, task domain.Task) {
	if r.auditLog == nil {
		return
	}

	_, _ = r.auditLog.Record(ctx, audit.Event{
		Action:        "task.claimed",
		TargetType:    "task",
		TargetID:      task.ID.String(),
		ChangeSummary: "task claimed and moved to running",
		DiagnosticFields: map[string]string{
			"account_id": task.AccountID.String(),
			"attempt":    strconv.Itoa(task.Attempt),
			"claimed_by": task.ClaimedBy,
		},
	})
}

func (r *TaskPostgresRepository) recordCompletedAudit(ctx context.Context, task domain.Task) {
	if r.auditLog == nil {
		return
	}

	action := "task.failed"
	changeSummary := fmt.Sprintf("task completed with %s", task.Status)
	if task.Status == domain.TaskStatusSuccess {
		action = "task.succeeded"
		changeSummary = "task completed with success"
	}

	_, _ = r.auditLog.Record(ctx, audit.Event{
		Action:        action,
		TargetType:    "task",
		TargetID:      task.ID.String(),
		ChangeSummary: changeSummary,
		DiagnosticFields: map[string]string{
			"account_id":    task.AccountID.String(),
			"attempt":       strconv.Itoa(task.Attempt),
			"claimed_by":    task.ClaimedBy,
			"final_status":  string(task.Status),
			"error_code":    string(task.ErrorCode),
			"result_reason": task.ResultReason,
		},
	})
}

func appendAdminTransitionAuditLog(
	ctx context.Context,
	tx pgx.Tx,
	before domain.Task,
	after domain.Task,
	transition domain.TaskAdminTransition,
) error {
	actor := audit.ActorFromContext(ctx)
	diagnosticFields := adminMutationDiagnosticFields(
		ctx,
		adminActionFromTransition(transition.Action),
		after.ID.String(),
		map[string]string{
			"account_id":  after.AccountID.String(),
			"from_status": string(transition.FromStatus),
			"to_status":   string(transition.ToStatus),
			"attempt":     strconv.Itoa(after.Attempt),
		},
	)
	diagnosticJSON, err := json.Marshal(diagnosticFields)
	if err != nil {
		return fmt.Errorf("marshal admin transition diagnostic fields: %w", err)
	}

	action := "task.admin_transitioned"
	switch transition.Action {
	case domain.TaskAdminActionRetry:
		action = "task.retried"
	case domain.TaskAdminActionCancel:
		action = "task.canceled"
	}

	changeSummary := fmt.Sprintf(
		"admin transition applied: %s -> %s (%s)",
		before.Status,
		after.Status,
		transition.Action,
	)

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id,
			actor_type,
			actor_id,
			action,
			target_type,
			target_id,
			change_summary,
			diagnostic_fields,
			created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`,
		uuid.New(),
		string(actor.Type),
		actor.ID,
		action,
		"task",
		after.ID.String(),
		changeSummary,
		diagnosticJSON,
		time.Now().UTC(),
	); err != nil {
		return err
	}

	return nil
}

func appendTaskRetryAuditLog(
	ctx context.Context,
	tx pgx.Tx,
	sourceTask domain.Task,
	newTask domain.Task,
) error {
	actor := audit.ActorFromContext(ctx)
	diagnosticFields := adminMutationDiagnosticFields(
		ctx,
		observability.EventAdminRetryTask,
		newTask.ID.String(),
		map[string]string{
			"account_id":     newTask.AccountID.String(),
			"source_task_id": sourceTask.ID.String(),
			"new_task_id":    newTask.ID.String(),
			"source_status":  string(sourceTask.Status),
			"new_status":     string(newTask.Status),
		},
	)
	diagnosticJSON, err := json.Marshal(diagnosticFields)
	if err != nil {
		return fmt.Errorf("marshal retry diagnostic fields: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id,
			actor_type,
			actor_id,
			action,
			target_type,
			target_id,
			change_summary,
			diagnostic_fields,
			created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`,
		uuid.New(),
		string(actor.Type),
		actor.ID,
		"task.retried",
		"task",
		newTask.ID.String(),
		fmt.Sprintf("retry task created from source task %s", sourceTask.ID.String()),
		diagnosticJSON,
		time.Now().UTC(),
	); err != nil {
		return err
	}

	return nil
}

func adminMutationDiagnosticFields(
	ctx context.Context,
	adminAction string,
	taskID string,
	extra map[string]string,
) map[string]string {
	requestContext := observability.AdminRequestContextFrom(ctx)
	fields := map[string]string{
		"correlation_id":   requestContext.CorrelationID,
		"admin_action":     strings.TrimSpace(adminAction),
		"operation_result": "success",
		"error_code":       "none",
		"task_id":          strings.TrimSpace(taskID),
	}

	for key, value := range extra {
		fields[key] = value
	}

	return audit.SanitizeDiagnosticFields(fields)
}

func adminActionFromTransition(action domain.TaskAdminAction) string {
	switch action {
	case domain.TaskAdminActionRetry:
		return observability.EventAdminRetryTask
	case domain.TaskAdminActionCancel:
		return observability.EventAdminCancelTask
	default:
		return "admin.unknown"
	}
}
