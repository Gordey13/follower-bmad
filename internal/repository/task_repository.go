package repository

import (
	"context"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type TaskRepository interface {
	Enqueue(ctx context.Context, task domain.Task) (domain.Task, error)
	GetByID(ctx context.Context, taskID uuid.UUID) (domain.Task, error)
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
}
