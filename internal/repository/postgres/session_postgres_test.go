package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"follower/internal/audit"
	"follower/internal/domain"
	postgresrepo "follower/internal/repository/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSessionRepositoryUpsertAndGetByAccountID(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareSessionMetadataSchema(t, pool)

	accountID := createTestAccount(t, pool, "session-account-01")
	repository := postgresrepo.NewSessionRepository(pool)

	metadata := domain.SessionMetadata{
		AccountID: accountID,
		Revision:  1,
		Status:    domain.SessionStatusValid,
		ObjectKey: "accounts/" + accountID.String() + "/sessions/1.json",
	}

	saved, err := repository.Upsert(context.Background(), metadata)
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	got, err := repository.GetByAccountID(context.Background(), accountID)
	if err != nil {
		t.Fatalf("GetByAccountID() error = %v", err)
	}

	if got.AccountID != accountID {
		t.Fatalf("expected account id %s, got %s", accountID.String(), got.AccountID.String())
	}
	if got.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", got.Revision)
	}
	if got.Status != domain.SessionStatusValid {
		t.Fatalf("expected status %s, got %s", domain.SessionStatusValid, got.Status)
	}
	if got.ObjectKey != metadata.ObjectKey {
		t.Fatalf("expected object key %q, got %q", metadata.ObjectKey, got.ObjectKey)
	}
	if saved.UpdatedAt.Before(saved.CreatedAt) {
		t.Fatalf("expected updated_at >= created_at")
	}
}

func TestSessionRepositoryUpsertRejectsValidStatusWithErrorCode(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareSessionMetadataSchema(t, pool)

	accountID := createTestAccount(t, pool, "session-account-valid-error-01")
	repository := postgresrepo.NewSessionRepository(pool)

	_, err := repository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountID,
		Revision:  1,
		Status:    domain.SessionStatusValid,
		ObjectKey: "accounts/" + accountID.String() + "/sessions/1.json",
		ErrorCode: domain.ErrorCodeSessionPayloadCorrupted,
	})
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidSessionStatus) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeInvalidSessionStatus, err)
	}
}

func TestSessionRepositoryUpdateStatusIsolatedByAccount(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareSessionMetadataSchema(t, pool)

	accountIDOne := createTestAccount(t, pool, "session-account-02")
	accountIDTwo := createTestAccount(t, pool, "session-account-03")
	repository := postgresrepo.NewSessionRepository(pool)

	_, err := repository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountIDOne,
		Revision:  1,
		Status:    domain.SessionStatusValid,
		ObjectKey: "accounts/" + accountIDOne.String() + "/sessions/1.json",
	})
	if err != nil {
		t.Fatalf("Upsert(accountIDOne) error = %v", err)
	}

	_, err = repository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountIDTwo,
		Revision:  4,
		Status:    domain.SessionStatusValid,
		ObjectKey: "accounts/" + accountIDTwo.String() + "/sessions/4.json",
	})
	if err != nil {
		t.Fatalf("Upsert(accountIDTwo) error = %v", err)
	}

	updated, err := repository.UpdateStatus(
		context.Background(),
		accountIDOne,
		domain.SessionStatusUnavailable,
		domain.ErrorCodeSessionPayloadMissing,
	)
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}

	if updated.Status != domain.SessionStatusUnavailable {
		t.Fatalf("expected status %s, got %s", domain.SessionStatusUnavailable, updated.Status)
	}
	if updated.ErrorCode != domain.ErrorCodeSessionPayloadMissing {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeSessionPayloadMissing, updated.ErrorCode)
	}

	other, err := repository.GetByAccountID(context.Background(), accountIDTwo)
	if err != nil {
		t.Fatalf("GetByAccountID(accountIDTwo) error = %v", err)
	}
	if other.Status != domain.SessionStatusValid {
		t.Fatalf("expected second account to stay valid, got %s", other.Status)
	}
	if other.Revision != 4 {
		t.Fatalf("expected second account revision 4, got %d", other.Revision)
	}
}

func TestSessionRepositoryWritesAuditOnStatusChange(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareSessionMetadataSchema(t, pool)
	prepareAuditLogsSchema(t, pool)

	accountID := createTestAccount(t, pool, "session-audit-account-01")
	auditRepository := postgresrepo.NewAuditRepository(pool)
	auditLog := audit.NewLog(auditRepository, nil)
	repository := postgresrepo.NewSessionRepository(pool, auditLog)

	_, err := repository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountID,
		Revision:  1,
		Status:    domain.SessionStatusValid,
		ObjectKey: "accounts/" + accountID.String() + "/sessions/1.json",
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	_, err = repository.UpdateStatus(
		context.Background(),
		accountID,
		domain.SessionStatusInvalid,
		domain.ErrorCodeSessionPayloadCorrupted,
	)
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}

	records, err := auditRepository.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected session status change audit record")
	}
	if records[0].Action != "session.status_changed" {
		t.Fatalf("expected action %q, got %q", "session.status_changed", records[0].Action)
	}
}

func TestSessionRepositoryUpsertSucceedsWhenAuditFails(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareSessionMetadataSchema(t, pool)

	accountID := createTestAccount(t, pool, "session-audit-fail-open-01")
	repository := postgresrepo.NewSessionRepository(
		pool,
		newFailingAuditLog(errors.New("audit store unavailable")),
	)

	metadata := domain.SessionMetadata{
		AccountID: accountID,
		Revision:  1,
		Status:    domain.SessionStatusValid,
		ObjectKey: "accounts/" + accountID.String() + "/sessions/1.json",
	}

	saved, err := repository.Upsert(context.Background(), metadata)
	if err != nil {
		t.Fatalf("Upsert() must succeed even when audit fails, got error = %v", err)
	}
	if saved.AccountID != accountID {
		t.Fatalf("expected account id %s, got %s", accountID.String(), saved.AccountID.String())
	}

	got, err := repository.GetByAccountID(context.Background(), accountID)
	if err != nil {
		t.Fatalf("GetByAccountID() error = %v", err)
	}
	if got.Revision != metadata.Revision {
		t.Fatalf("expected revision %d, got %d", metadata.Revision, got.Revision)
	}
	if got.ObjectKey != metadata.ObjectKey {
		t.Fatalf("expected object key %q, got %q", metadata.ObjectKey, got.ObjectKey)
	}
}

func TestSessionRepositoryStatusSnapshotReturnsCounts(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareSessionMetadataSchema(t, pool)

	repository := postgresrepo.NewSessionRepository(pool)

	type entry struct {
		username string
		status   domain.SessionStatus
	}
	items := []entry{
		{username: "session-snapshot-valid", status: domain.SessionStatusValid},
		{username: "session-snapshot-invalid", status: domain.SessionStatusInvalid},
		{username: "session-snapshot-unavailable", status: domain.SessionStatusUnavailable},
	}

	for index, item := range items {
		accountID := createTestAccount(t, pool, item.username)
		errorCode := domain.ErrorCode("")
		if item.status != domain.SessionStatusValid {
			errorCode = domain.ErrorCodeSessionPayloadCorrupted
		}
		if _, err := repository.Upsert(context.Background(), domain.SessionMetadata{
			AccountID: accountID,
			Revision:  int64(index + 1),
			Status:    item.status,
			ObjectKey: "accounts/" + accountID.String() + "/sessions/" + string(item.status) + ".json",
			ErrorCode: errorCode,
		}); err != nil {
			t.Fatalf("Upsert(%s) error = %v", item.status, err)
		}
	}

	snapshot, err := repository.StatusSnapshot(context.Background())
	if err != nil {
		t.Fatalf("StatusSnapshot() error = %v", err)
	}

	expected := map[domain.SessionStatus]int64{
		domain.SessionStatusValid:       1,
		domain.SessionStatusInvalid:     1,
		domain.SessionStatusUnavailable: 1,
	}
	for status, want := range expected {
		if got := snapshot[status]; got != want {
			t.Fatalf("expected %s=%d, got %d", status, want, got)
		}
	}
}

func createTestAccount(t *testing.T, pool *pgxpool.Pool, username string) uuid.UUID {
	t.Helper()

	accountRepo := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	proxy := domain.Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     10000 + time.Now().Nanosecond()%1000,
		IsActive: true,
	}
	if err := accountRepo.CreateProxy(context.Background(), proxy); err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}

	accountID := uuid.New()
	err := accountRepo.CreateAccount(context.Background(), domain.Account{
		ID:               accountID,
		Username:         username,
		ProxyID:          proxy.ID,
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	return accountID
}

func prepareSessionMetadataSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS account_sessions (
			account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
			revision BIGINT NOT NULL CHECK (revision > 0),
			status TEXT NOT NULL,
			object_key TEXT NOT NULL,
			error_code TEXT NULL,
			last_restored_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT chk_account_sessions_status CHECK (
				status IN ('valid', 'invalid', 'unavailable')
			),
			CONSTRAINT chk_account_sessions_valid_without_error CHECK (
				NOT (status = 'valid' AND error_code IS NOT NULL)
			)
		);
	`)
	if err != nil {
		t.Fatalf("prepare session metadata schema: %v", err)
	}
}
