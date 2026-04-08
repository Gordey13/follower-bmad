package repository

import (
	"context"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type ResultRepository interface {
	Upsert(ctx context.Context, result domain.FollowResult) (domain.FollowResult, error)
	GetByTaskAttempt(ctx context.Context, taskID uuid.UUID, attempt int) (domain.FollowResult, error)
	ListHistory(ctx context.Context, query domain.FollowResultsHistoryQuery) ([]domain.FollowResultHistoryEntry, error)
}
