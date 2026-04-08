package browser

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"follower/internal/domain"
	"follower/internal/observability"

	"github.com/google/uuid"
)

func TestRestoreMarksUnavailableWhenPayloadMissing(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	updateCalled := false

	repository := &mockSessionRepository{
		getByAccountIDFn: func(ctx context.Context, id uuid.UUID) (domain.SessionMetadata, error) {
			return domain.SessionMetadata{
				AccountID: id,
				Revision:  2,
				Status:    domain.SessionStatusValid,
				ObjectKey: "accounts/" + id.String() + "/sessions/2.json",
			}, nil
		},
		updateStatusFn: func(ctx context.Context, id uuid.UUID, status domain.SessionStatus, errorCode domain.ErrorCode) (domain.SessionMetadata, error) {
			updateCalled = true
			if status != domain.SessionStatusUnavailable {
				t.Fatalf("expected status %s, got %s", domain.SessionStatusUnavailable, status)
			}
			if errorCode != domain.ErrorCodeSessionPayloadMissing {
				t.Fatalf("expected error code %s, got %s", domain.ErrorCodeSessionPayloadMissing, errorCode)
			}
			return domain.SessionMetadata{}, nil
		},
	}

	store := &mockSessionStore{
		loadFn: func(ctx context.Context, id uuid.UUID, objectKey string) ([]byte, error) {
			return nil, domain.NewDomainError(domain.ErrorCodeSessionPayloadMissing, "missing")
		},
	}

	restorer := NewSessionRestorer(repository, store, testLogger())
	_, _, err := restorer.Restore(context.Background(), accountID)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionPayloadMissing, err)
	}
	if !updateCalled {
		t.Fatal("expected UpdateStatus to be called")
	}
}

func TestRestoreMarksInvalidOnOwnershipMismatch(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()

	repository := &mockSessionRepository{
		getByAccountIDFn: func(ctx context.Context, id uuid.UUID) (domain.SessionMetadata, error) {
			return domain.SessionMetadata{
				AccountID: id,
				Revision:  3,
				Status:    domain.SessionStatusValid,
				ObjectKey: "accounts/" + id.String() + "/sessions/3.json",
			}, nil
		},
		updateStatusFn: func(ctx context.Context, id uuid.UUID, status domain.SessionStatus, errorCode domain.ErrorCode) (domain.SessionMetadata, error) {
			if status != domain.SessionStatusInvalid {
				t.Fatalf("expected status %s, got %s", domain.SessionStatusInvalid, status)
			}
			if errorCode != domain.ErrorCodeSessionOwnershipMismatch {
				t.Fatalf("expected error code %s, got %s", domain.ErrorCodeSessionOwnershipMismatch, errorCode)
			}
			return domain.SessionMetadata{}, nil
		},
	}

	store := &mockSessionStore{
		loadFn: func(ctx context.Context, id uuid.UUID, objectKey string) ([]byte, error) {
			return nil, domain.NewDomainError(domain.ErrorCodeSessionOwnershipMismatch, "ownership mismatch")
		},
	}

	restorer := NewSessionRestorer(repository, store, testLogger())
	_, _, err := restorer.Restore(context.Background(), accountID)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionOwnershipMismatch) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionOwnershipMismatch, err)
	}
}

func TestBootstrapReasonForRestoreErrorMarksMissingMetadataAsBootstrapRequired(t *testing.T) {
	t.Parallel()

	reason, bootstrapRequired := BootstrapReasonForRestoreError(
		domain.NewDomainError(domain.ErrorCodeSessionMetadataNotFound, "metadata not found"),
	)
	if !bootstrapRequired {
		t.Fatal("expected bootstrap_required=true for session metadata not found")
	}
	if reason != domain.ErrorCodeAuthBootstrapRequired {
		t.Fatalf("expected bootstrap reason %s, got %s", domain.ErrorCodeAuthBootstrapRequired, reason)
	}
}

func TestBootstrapReasonForRestoreErrorMarksMissingPayloadAsBootstrapRequired(t *testing.T) {
	t.Parallel()

	reason, bootstrapRequired := BootstrapReasonForRestoreError(
		domain.NewDomainError(domain.ErrorCodeSessionPayloadMissing, "payload missing"),
	)
	if !bootstrapRequired {
		t.Fatal("expected bootstrap_required=true for session payload missing")
	}
	if reason != domain.ErrorCodeAuthBootstrapRequired {
		t.Fatalf("expected bootstrap reason %s, got %s", domain.ErrorCodeAuthBootstrapRequired, reason)
	}
}

func TestSaveCreatesFirstRevisionWhenMetadataMissing(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	repository := &mockSessionRepository{
		getByAccountIDFn: func(ctx context.Context, id uuid.UUID) (domain.SessionMetadata, error) {
			return domain.SessionMetadata{}, domain.NewDomainError(
				domain.ErrorCodeSessionMetadataNotFound,
				"not found",
			)
		},
		upsertFn: func(ctx context.Context, metadata domain.SessionMetadata) (domain.SessionMetadata, error) {
			if metadata.Revision != 1 {
				t.Fatalf("expected revision 1, got %d", metadata.Revision)
			}
			if metadata.Status != domain.SessionStatusValid {
				t.Fatalf("expected status %s, got %s", domain.SessionStatusValid, metadata.Status)
			}
			now := time.Now()
			metadata.CreatedAt = now
			metadata.UpdatedAt = now
			return metadata, nil
		},
	}

	store := &mockSessionStore{
		saveFn: func(ctx context.Context, id uuid.UUID, revision int64, payload []byte) (string, error) {
			if revision != 1 {
				t.Fatalf("expected revision 1, got %d", revision)
			}
			return "accounts/" + id.String() + "/sessions/1.json", nil
		},
	}

	restorer := NewSessionRestorer(repository, store, testLogger())
	metadata, err := restorer.Save(context.Background(), accountID, []byte(`{"cookies":[]}`))
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if metadata.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", metadata.Revision)
	}
}

func TestSaveIncrementsRevisionAndClearsErrorCodeWhenMetadataExists(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	repository := &mockSessionRepository{
		getByAccountIDFn: func(ctx context.Context, id uuid.UUID) (domain.SessionMetadata, error) {
			return domain.SessionMetadata{
				AccountID: id,
				Revision:  4,
				Status:    domain.SessionStatusInvalid,
				ObjectKey: "accounts/" + id.String() + "/sessions/4.json",
				ErrorCode: domain.ErrorCodeSessionPayloadCorrupted,
			}, nil
		},
		upsertFn: func(ctx context.Context, metadata domain.SessionMetadata) (domain.SessionMetadata, error) {
			if metadata.Revision != 5 {
				t.Fatalf("expected revision 5, got %d", metadata.Revision)
			}
			if metadata.Status != domain.SessionStatusValid {
				t.Fatalf("expected status %s, got %s", domain.SessionStatusValid, metadata.Status)
			}
			if metadata.ErrorCode != "" {
				t.Fatalf("expected empty error code for valid metadata, got %s", metadata.ErrorCode)
			}
			now := time.Now()
			metadata.CreatedAt = now
			metadata.UpdatedAt = now
			return metadata, nil
		},
	}

	store := &mockSessionStore{
		saveFn: func(ctx context.Context, id uuid.UUID, revision int64, payload []byte) (string, error) {
			if revision != 5 {
				t.Fatalf("expected revision 5, got %d", revision)
			}
			return "accounts/" + id.String() + "/sessions/5.json", nil
		},
	}

	restorer := NewSessionRestorer(repository, store, testLogger())
	metadata, err := restorer.Save(context.Background(), accountID, []byte(`{"cookies":[]}`))
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if metadata.Revision != 5 {
		t.Fatalf("expected revision 5, got %d", metadata.Revision)
	}
	if metadata.Status != domain.SessionStatusValid {
		t.Fatalf("expected status %s, got %s", domain.SessionStatusValid, metadata.Status)
	}
	if metadata.ErrorCode != "" {
		t.Fatalf("expected empty error code, got %s", metadata.ErrorCode)
	}
}

func TestSaveCompensatesWhenMetadataUpsertFails(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	upsertErr := errors.New("postgres unavailable")
	deleted := false

	repository := &mockSessionRepository{
		getByAccountIDFn: func(ctx context.Context, id uuid.UUID) (domain.SessionMetadata, error) {
			return domain.SessionMetadata{}, domain.NewDomainError(
				domain.ErrorCodeSessionMetadataNotFound,
				"not found",
			)
		},
		upsertFn: func(ctx context.Context, metadata domain.SessionMetadata) (domain.SessionMetadata, error) {
			return domain.SessionMetadata{}, upsertErr
		},
	}

	store := &mockSessionStore{
		saveFn: func(ctx context.Context, id uuid.UUID, revision int64, payload []byte) (string, error) {
			return "accounts/" + id.String() + "/sessions/1.json", nil
		},
		deleteFn: func(ctx context.Context, objectKey string) error {
			deleted = true
			return nil
		},
	}

	restorer := NewSessionRestorer(repository, store, testLogger())
	_, err := restorer.Save(context.Background(), accountID, []byte(`{"cookies":[]}`))
	if !errors.Is(err, upsertErr) {
		t.Fatalf("expected upsert error, got %v", err)
	}
	if !deleted {
		t.Fatal("expected cleanup Delete to be called")
	}
}

func TestRestoreLogsLifecycleEventsWithRequiredFields(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))

	repository := &mockSessionRepository{
		getByAccountIDFn: func(ctx context.Context, id uuid.UUID) (domain.SessionMetadata, error) {
			return domain.SessionMetadata{
				AccountID: id,
				Revision:  7,
				Status:    domain.SessionStatusValid,
				ObjectKey: "accounts/" + id.String() + "/sessions/7.json",
			}, nil
		},
		markRestoredFn: func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, error) {
			return domain.SessionMetadata{
				AccountID: accountID,
				Revision:  7,
				Status:    domain.SessionStatusValid,
				ObjectKey: "accounts/" + accountID.String() + "/sessions/7.json",
			}, nil
		},
	}
	store := &mockSessionStore{
		loadFn: func(ctx context.Context, id uuid.UUID, objectKey string) ([]byte, error) {
			return []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`), nil
		},
	}

	restorer := NewSessionRestorer(repository, store, logger)
	restoreCtx := observability.WithRestoreLifecycleContext(context.Background(), "task-restore-1", 7)
	_, _, err := restorer.Restore(restoreCtx, accountID)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	output := buffer.String()
	requiredTokens := []string{
		"session.restore_started",
		"session.restored",
		"component=browser.session_restorer",
		"task_id=task-restore-1",
		"account_id=" + accountID.String(),
		"attempt=7",
		"error_code=eligible",
		"duration_ms=",
		"session_revision=7",
		"object_key=accounts/" + accountID.String() + "/sessions/7.json",
	}
	for _, token := range requiredTokens {
		if !strings.Contains(output, token) {
			t.Fatalf("expected log output to contain %q, got %q", token, output)
		}
	}
}

func TestRestoreFailureLogsDiagnosticMessageWithoutSensitiveValue(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))

	repository := &mockSessionRepository{
		getByAccountIDFn: func(ctx context.Context, id uuid.UUID) (domain.SessionMetadata, error) {
			return domain.SessionMetadata{
				AccountID: id,
				Revision:  2,
				Status:    domain.SessionStatusValid,
				ObjectKey: "accounts/" + id.String() + "/sessions/2.json",
			}, nil
		},
		updateStatusFn: func(ctx context.Context, id uuid.UUID, status domain.SessionStatus, errorCode domain.ErrorCode) (domain.SessionMetadata, error) {
			return domain.SessionMetadata{}, nil
		},
	}
	store := &mockSessionStore{
		loadFn: func(ctx context.Context, id uuid.UUID, objectKey string) ([]byte, error) {
			return nil, domain.NewDomainError(
				domain.ErrorCodeSessionPayloadMissing,
				"cannot load payload: credentials=super-secret",
			)
		},
	}

	restorer := NewSessionRestorer(repository, store, logger)
	_, _, err := restorer.Restore(context.Background(), accountID)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionPayloadMissing, err)
	}

	output := buffer.String()
	if !strings.Contains(output, "session.restore_failed") {
		t.Fatalf("expected restore failure log, got %q", output)
	}
	if !strings.Contains(output, "diagnostic_message=") {
		t.Fatalf("expected diagnostic_message field, got %q", output)
	}
	if strings.Contains(output, "super-secret") {
		t.Fatalf("expected sensitive value to be redacted, got %q", output)
	}
}

type mockSessionRepository struct {
	getByAccountIDFn func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, error)
	upsertFn         func(ctx context.Context, metadata domain.SessionMetadata) (domain.SessionMetadata, error)
	updateStatusFn   func(
		ctx context.Context,
		accountID uuid.UUID,
		status domain.SessionStatus,
		errorCode domain.ErrorCode,
	) (domain.SessionMetadata, error)
	markRestoredFn func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, error)
}

func (m *mockSessionRepository) GetByAccountID(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, error) {
	if m.getByAccountIDFn != nil {
		return m.getByAccountIDFn(ctx, accountID)
	}
	return domain.SessionMetadata{}, nil
}

func (m *mockSessionRepository) StatusSnapshot(ctx context.Context) (map[domain.SessionStatus]int64, error) {
	return map[domain.SessionStatus]int64{}, nil
}

func (m *mockSessionRepository) Upsert(ctx context.Context, metadata domain.SessionMetadata) (domain.SessionMetadata, error) {
	if m.upsertFn != nil {
		return m.upsertFn(ctx, metadata)
	}
	return metadata, nil
}

func (m *mockSessionRepository) UpdateStatus(
	ctx context.Context,
	accountID uuid.UUID,
	status domain.SessionStatus,
	errorCode domain.ErrorCode,
) (domain.SessionMetadata, error) {
	if m.updateStatusFn != nil {
		return m.updateStatusFn(ctx, accountID, status, errorCode)
	}
	return domain.SessionMetadata{}, nil
}

func (m *mockSessionRepository) MarkRestored(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, error) {
	if m.markRestoredFn != nil {
		return m.markRestoredFn(ctx, accountID)
	}
	return domain.SessionMetadata{}, nil
}

type mockSessionStore struct {
	saveFn   func(ctx context.Context, accountID uuid.UUID, revision int64, payload []byte) (string, error)
	loadFn   func(ctx context.Context, accountID uuid.UUID, objectKey string) ([]byte, error)
	deleteFn func(ctx context.Context, objectKey string) error
}

func (m *mockSessionStore) Save(
	ctx context.Context,
	accountID uuid.UUID,
	revision int64,
	payload []byte,
) (string, error) {
	if m.saveFn != nil {
		return m.saveFn(ctx, accountID, revision, payload)
	}
	return "", nil
}

func (m *mockSessionStore) Load(ctx context.Context, accountID uuid.UUID, objectKey string) ([]byte, error) {
	if m.loadFn != nil {
		return m.loadFn(ctx, accountID, objectKey)
	}
	return nil, nil
}

func (m *mockSessionStore) Delete(ctx context.Context, objectKey string) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, objectKey)
	}
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
