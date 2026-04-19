package repository

import (
	"context"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type EnqueueValidatedRow struct {
	Row           int
	AccountID     uuid.UUID
	TargetProfile string
}

type EnqueueSkippedRow struct {
	Row     int    `json:"row"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type EnqueueValidatedBatchResult struct {
	RowsCreated int                 `json:"rows_created"`
	RowsSkipped int                 `json:"rows_skipped"`
	SkippedRows []EnqueueSkippedRow `json:"skipped_rows"`
}

type TaskRepository interface {
	Enqueue(ctx context.Context, task domain.Task) (domain.Task, error)
	EnqueueValidatedBatch(ctx context.Context, rows []EnqueueValidatedRow) (EnqueueValidatedBatchResult, error)
	GetByID(ctx context.Context, taskID uuid.UUID) (domain.Task, error)
	ListFailures(ctx context.Context, limit int, offset int) ([]domain.Task, error)
	ClaimNextQueued(ctx context.Context, claimedBy string) (domain.Task, bool, error)
	TaskQueueSnapshot(ctx context.Context) (map[domain.TaskStatus]int64, error)
	Complete(
		ctx context.Context,
		taskID uuid.UUID,
		claimedBy string,
		finalStatus domain.TaskStatus,
		errorCode domain.ErrorCode,
		resultReason string,
	) (domain.Task, error)
	ApplyAdminTransition(
		ctx context.Context,
		taskID uuid.UUID,
		action domain.TaskAdminAction,
	) (domain.Task, error)
	RetryFromTask(
		ctx context.Context,
		sourceTaskID uuid.UUID,
	) (domain.Task, error)
}
