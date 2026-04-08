package repository

import (
	"context"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type SessionRepository interface {
	GetByAccountID(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, error)
	StatusSnapshot(ctx context.Context) (map[domain.SessionStatus]int64, error)
	Upsert(ctx context.Context, metadata domain.SessionMetadata) (domain.SessionMetadata, error)
	UpdateStatus(
		ctx context.Context,
		accountID uuid.UUID,
		status domain.SessionStatus,
		errorCode domain.ErrorCode,
	) (domain.SessionMetadata, error)
	MarkRestored(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, error)
}
