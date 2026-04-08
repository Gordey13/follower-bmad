package repository

import (
	"context"

	"follower/internal/audit"
)

type AuditRepository interface {
	Append(ctx context.Context, record audit.Record) (audit.Record, error)
	ListRecent(ctx context.Context, limit int) ([]audit.Record, error)
}
