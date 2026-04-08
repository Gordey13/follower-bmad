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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TaskPostgresRepository struct {
	pool     *pgxpool.Pool
	auditLog *audit.Log
}

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

	row := r.pool.QueryRow(ctx, `
		INSERT INTO tasks (
			id,
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
		) VALUES ($1, $2, $3, $4, $5, NULL, NULL, NULL, NULL, NULL, NULL)
		RETURNING
			id,
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
	`, task.ID, task.AccountID, task.TargetProfile, task.Status, task.Attempt)

	return scanTask(row)
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

func (r *TaskPostgresRepository) TaskQueueSnapshot(ctx context.Context) (map[domain.TaskStatus]int64, error) {
	snapshot := map[domain.TaskStatus]int64{
		domain.TaskStatusQueued:  0,
		domain.TaskStatusRunning: 0,
		domain.TaskStatusSuccess: 0,
		domain.TaskStatusRetry:   0,
		domain.TaskStatusFail:    0,
	}

	rows, err := r.pool.Query(ctx, `
		SELECT status, COUNT(*)::BIGINT
		FROM tasks
		WHERE status IN ('queued', 'running', 'success', 'retry', 'fail')
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
	var targetProfile string
	var status string
	var claimedAt *time.Time
	var startedAt *time.Time
	var finishedAt *time.Time
	var errorCode string
	var resultReason string

	err := row.Scan(
		&task.ID,
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

	task.TargetProfile = domain.TargetProfileDescriptor(targetProfile)
	task.Status = domain.TaskStatus(status)
	task.ClaimedAt = claimedAt
	task.StartedAt = startedAt
	task.FinishedAt = finishedAt
	task.ErrorCode = domain.ErrorCode(errorCode)
	task.ResultReason = resultReason

	return task, nil
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
