package postgres_test

import (
	"context"
	"testing"
	"time"

	"follower/internal/audit"

	"github.com/jackc/pgx/v5/pgxpool"
)

func prepareAuditLogsSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS audit_logs (
			id UUID PRIMARY KEY,
			actor_type TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			action TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id TEXT NOT NULL,
			change_summary TEXT NOT NULL,
			diagnostic_fields JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at
			ON audit_logs (created_at DESC);
	`)
	if err != nil {
		t.Fatalf("prepare audit logs schema: %v", err)
	}
}

type failingAuditStore struct {
	appendErr error
}

func (s *failingAuditStore) Append(ctx context.Context, record audit.Record) (audit.Record, error) {
	return audit.Record{}, s.appendErr
}

func (s *failingAuditStore) ListRecent(ctx context.Context, limit int) ([]audit.Record, error) {
	return []audit.Record{}, nil
}

func newFailingAuditLog(err error) *audit.Log {
	return audit.NewLog(&failingAuditStore{appendErr: err}, nil)
}
