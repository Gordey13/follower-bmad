package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"follower/internal/domain"

	"github.com/google/uuid"
)

func TestPrepareExecutionContextRestoresSession(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	proxyID := uuid.New()
	metadata := domain.SessionMetadata{
		AccountID: accountID,
		Revision:  3,
		Status:    domain.SessionStatusValid,
		ObjectKey: "accounts/" + accountID.String() + "/sessions/3.json",
	}
	payload := []byte(`{"cookies":[{"name":"sid","value":"abc"}]}`)

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, gotAccountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			if gotAccountID != accountID {
				t.Fatalf("expected account id %s, got %s", accountID.String(), gotAccountID.String())
			}
			if executionContextID != "exec-ctx-1" {
				t.Fatalf("expected execution context id exec-ctx-1, got %s", executionContextID)
			}
			return domain.AccountWithProxy{
				Account: domain.Account{
					ID: gotAccountID,
				},
				Proxy: domain.Proxy{
					ID: proxyID,
				},
			}, nil
		},
	}

	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, gotAccountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			if gotAccountID != accountID {
				t.Fatalf("expected account id %s, got %s", accountID.String(), gotAccountID.String())
			}
			return metadata, payload, nil
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger())
	prepared, err := service.PrepareExecutionContext(context.Background(), accountID, "exec-ctx-1")
	if err != nil {
		t.Fatalf("PrepareExecutionContext() error = %v", err)
	}

	if prepared.AccountWithProxy.Proxy.ID != proxyID {
		t.Fatalf("expected proxy id %s, got %s", proxyID.String(), prepared.AccountWithProxy.Proxy.ID.String())
	}
	if prepared.SessionMetadata.Revision != 3 {
		t.Fatalf("expected session revision 3, got %d", prepared.SessionMetadata.Revision)
	}
	if string(prepared.SessionPayload) != string(payload) {
		t.Fatalf("expected payload %q, got %q", payload, prepared.SessionPayload)
	}
}

func TestPrepareExecutionContextRestoresSessionWithoutProxyBinding(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	metadata := domain.SessionMetadata{
		AccountID: accountID,
		Revision:  8,
		Status:    domain.SessionStatusValid,
		ObjectKey: "accounts/" + accountID.String() + "/sessions/8.json",
	}
	payload := []byte(`{"cookies":[{"name":"sid","value":"no-proxy"}]}`)

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, gotAccountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{
				Account: domain.Account{
					ID: gotAccountID,
				},
			}, nil
		},
	}

	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, gotAccountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			if gotAccountID != accountID {
				t.Fatalf("expected account id %s, got %s", accountID.String(), gotAccountID.String())
			}
			return metadata, payload, nil
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger())
	prepared, err := service.PrepareExecutionContext(context.Background(), accountID, "exec-ctx-no-proxy")
	if err != nil {
		t.Fatalf("PrepareExecutionContext() error = %v", err)
	}

	if prepared.AccountWithProxy.Account.ProxyID != uuid.Nil {
		t.Fatalf("expected empty proxy binding, got proxy_id=%s", prepared.AccountWithProxy.Account.ProxyID.String())
	}
	if !prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=true")
	}
}

func TestPrepareExecutionContextReleasesAccountOnRestoreError(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	releaseCalled := false

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, gotAccountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{
				Account: domain.Account{
					ID: gotAccountID,
				},
			}, nil
		},
		releaseFn: func(ctx context.Context, gotAccountID uuid.UUID, executionContextID string) error {
			releaseCalled = true
			return nil
		},
	}

	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, gotAccountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			return domain.SessionMetadata{}, nil, domain.NewDomainError(
				domain.ErrorCodeSessionPayloadMissing,
				"session payload missing",
			)
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger())
	_, err := service.PrepareExecutionContext(context.Background(), accountID, "exec-ctx-2")
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeSessionPayloadMissing, err)
	}
	if !releaseCalled {
		t.Fatal("expected Release to be called on restore error")
	}
}

func TestPrepareExecutionContextJoinsReleaseError(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	releaseErr := errors.New("release failed")

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, gotAccountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{
				Account: domain.Account{
					ID: gotAccountID,
				},
			}, nil
		},
		releaseFn: func(ctx context.Context, gotAccountID uuid.UUID, executionContextID string) error {
			return releaseErr
		},
	}

	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, gotAccountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			return domain.SessionMetadata{}, nil, domain.NewDomainError(
				domain.ErrorCodeSessionPayloadCorrupted,
				"payload is corrupted",
			)
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger())
	_, err := service.PrepareExecutionContext(context.Background(), accountID, "exec-ctx-3")
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadCorrupted) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeSessionPayloadCorrupted, err)
	}
	if !errors.Is(err, releaseErr) {
		t.Fatalf("expected joined error to include release error, got %v", err)
	}
}

func TestPrepareClaimedTaskContextRestoresSessionAndMarksReadyForFollow(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   2,
		ClaimedBy: "worker-claim-ctx-01",
	}
	proxyID := uuid.New()
	metadata := domain.SessionMetadata{
		AccountID: task.AccountID,
		Revision:  4,
		Status:    domain.SessionStatusValid,
		ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/4.json",
	}
	payload := []byte(`{"cookies":[{"name":"sid","value":"claim-success"}]}`)

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, gotAccountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			if gotAccountID != task.AccountID {
				t.Fatalf("expected account id %s, got %s", task.AccountID.String(), gotAccountID.String())
			}
			if executionContextID != task.ClaimedBy {
				t.Fatalf("expected execution context id %s, got %s", task.ClaimedBy, executionContextID)
			}
			return domain.AccountWithProxy{
				Account: domain.Account{
					ID: gotAccountID,
				},
				Proxy: domain.Proxy{
					ID: proxyID,
				},
			}, nil
		},
	}
	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, gotAccountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			if gotAccountID != task.AccountID {
				t.Fatalf("expected account id %s, got %s", task.AccountID.String(), gotAccountID.String())
			}
			return metadata, payload, nil
		},
	}
	taskCompleter := &mockExecutionTaskCompleter{
		completeFn: func(
			ctx context.Context,
			taskID uuid.UUID,
			claimedBy string,
			finalStatus domain.TaskStatus,
			errorCode domain.ErrorCode,
			resultReason string,
		) (domain.Task, error) {
			t.Fatal("task completer must not be called on successful preparation")
			return domain.Task{}, nil
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger(), taskCompleter)
	prepared, err := service.PrepareClaimedTaskContext(context.Background(), task)
	if err != nil {
		t.Fatalf("PrepareClaimedTaskContext() error = %v", err)
	}

	if prepared.TaskID != task.ID {
		t.Fatalf("expected task id %s, got %s", task.ID.String(), prepared.TaskID.String())
	}
	if prepared.Attempt != task.Attempt {
		t.Fatalf("expected attempt %d, got %d", task.Attempt, prepared.Attempt)
	}
	if prepared.ExecutionContextID != task.ClaimedBy {
		t.Fatalf("expected execution context id %s, got %s", task.ClaimedBy, prepared.ExecutionContextID)
	}
	if !prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=true on successful preparation")
	}
	if prepared.AccountWithProxy.Proxy.ID != proxyID {
		t.Fatalf("expected proxy id %s, got %s", proxyID.String(), prepared.AccountWithProxy.Proxy.ID.String())
	}
	if prepared.SessionMetadata.Revision != metadata.Revision {
		t.Fatalf("expected session revision %d, got %d", metadata.Revision, prepared.SessionMetadata.Revision)
	}
	if string(prepared.SessionPayload) != string(payload) {
		t.Fatalf("expected payload %q, got %q", payload, prepared.SessionPayload)
	}
}

func TestPrepareClaimedTaskContextReturnsBootstrapDecisionForMissingMetadata(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   1,
		ClaimedBy: "worker-claim-ctx-bootstrap-metadata",
	}
	completerCalled := false

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{Account: domain.Account{ID: accountID}}, nil
		},
	}
	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			return domain.SessionMetadata{}, nil, domain.NewDomainError(
				domain.ErrorCodeSessionMetadataNotFound,
				"session metadata is missing",
			)
		},
	}
	taskCompleter := &mockExecutionTaskCompleter{
		completeFn: func(
			ctx context.Context,
			taskID uuid.UUID,
			claimedBy string,
			finalStatus domain.TaskStatus,
			errorCode domain.ErrorCode,
			resultReason string,
		) (domain.Task, error) {
			completerCalled = true
			return domain.Task{}, nil
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger(), taskCompleter).
		WithSessionBootstrapPolicy(SessionBootstrapPolicy{
			BootstrapLoginEnabled: true,
		})
	prepared, err := service.PrepareClaimedTaskContext(context.Background(), task)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !prepared.BootstrapRequired {
		t.Fatal("expected bootstrap_required=true for missing metadata")
	}
	if prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=false for bootstrap-required context")
	}
	if prepared.BootstrapReason != domain.ErrorCodeAuthBootstrapRequired {
		t.Fatalf("expected bootstrap reason %s, got %s", domain.ErrorCodeAuthBootstrapRequired, prepared.BootstrapReason)
	}
	if prepared.BootstrapSource != domain.ErrorCodeSessionMetadataNotFound {
		t.Fatalf("expected bootstrap source %s, got %s", domain.ErrorCodeSessionMetadataNotFound, prepared.BootstrapSource)
	}
	if completerCalled {
		t.Fatal("task completer must not be called for bootstrap decision")
	}
}

func TestPrepareClaimedTaskContextReturnsBootstrapDecisionForMissingPayloadWhenEnabled(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   1,
		ClaimedBy: "worker-claim-ctx-bootstrap-payload",
	}
	completerCalled := false

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{Account: domain.Account{ID: accountID}}, nil
		},
	}
	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			return domain.SessionMetadata{
					AccountID: accountID,
					Revision:  1,
					Status:    domain.SessionStatusUnavailable,
					ObjectKey: "accounts/" + accountID.String() + "/sessions/1.json",
				}, nil, domain.NewDomainError(
					domain.ErrorCodeSessionPayloadMissing,
					"session payload missing",
				)
		},
	}
	taskCompleter := &mockExecutionTaskCompleter{
		completeFn: func(
			ctx context.Context,
			taskID uuid.UUID,
			claimedBy string,
			finalStatus domain.TaskStatus,
			errorCode domain.ErrorCode,
			resultReason string,
		) (domain.Task, error) {
			completerCalled = true
			return domain.Task{}, nil
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger(), taskCompleter).
		WithSessionBootstrapPolicy(SessionBootstrapPolicy{
			BootstrapLoginEnabled:         true,
			AllowMissingPayloadOnFirstRun: true,
		})
	prepared, err := service.PrepareClaimedTaskContext(context.Background(), task)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !prepared.BootstrapRequired {
		t.Fatal("expected bootstrap_required=true for missing payload")
	}
	if prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=false for bootstrap-required context")
	}
	if prepared.BootstrapReason != domain.ErrorCodeAuthBootstrapRequired {
		t.Fatalf("expected bootstrap reason %s, got %s", domain.ErrorCodeAuthBootstrapRequired, prepared.BootstrapReason)
	}
	if prepared.BootstrapSource != domain.ErrorCodeSessionPayloadMissing {
		t.Fatalf("expected bootstrap source %s, got %s", domain.ErrorCodeSessionPayloadMissing, prepared.BootstrapSource)
	}
	if completerCalled {
		t.Fatal("task completer must not be called for bootstrap decision")
	}
}

func TestPrepareClaimedTaskContextCompletesRetryForMissingSession(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   1,
		ClaimedBy: "worker-claim-ctx-02",
	}
	releaseCalled := false
	completerCalled := false

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{
				Account: domain.Account{ID: accountID},
			}, nil
		},
		releaseFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
			releaseCalled = true
			if executionContextID != task.ClaimedBy {
				t.Fatalf("expected release execution context %s, got %s", task.ClaimedBy, executionContextID)
			}
			return nil
		},
	}
	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			return domain.SessionMetadata{}, nil, domain.NewDomainError(
				domain.ErrorCodeSessionPayloadMissing,
				"payload missing",
			)
		},
	}
	taskCompleter := &mockExecutionTaskCompleter{
		completeFn: func(
			ctx context.Context,
			taskID uuid.UUID,
			claimedBy string,
			finalStatus domain.TaskStatus,
			errorCode domain.ErrorCode,
			resultReason string,
		) (domain.Task, error) {
			completerCalled = true
			if taskID != task.ID {
				t.Fatalf("expected complete task id %s, got %s", task.ID.String(), taskID.String())
			}
			if claimedBy != task.ClaimedBy {
				t.Fatalf("expected claimed_by %s, got %s", task.ClaimedBy, claimedBy)
			}
			if finalStatus != domain.TaskStatusRetry {
				t.Fatalf("expected status %s, got %s", domain.TaskStatusRetry, finalStatus)
			}
			if errorCode != domain.ErrorCodeSessionPayloadMissing {
				t.Fatalf("expected error code %s, got %s", domain.ErrorCodeSessionPayloadMissing, errorCode)
			}
			if !strings.Contains(resultReason, "status=retry") {
				t.Fatalf("expected deterministic status marker in result reason, got %q", resultReason)
			}
			if !strings.Contains(resultReason, "error_code="+string(domain.ErrorCodeSessionPayloadMissing)) {
				t.Fatalf("expected deterministic error_code marker in result reason, got %q", resultReason)
			}
			return domain.Task{
				ID:     taskID,
				Status: finalStatus,
			}, nil
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger(), taskCompleter)
	prepared, err := service.PrepareClaimedTaskContext(context.Background(), task)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeSessionPayloadMissing, err)
	}
	if prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=false on failed preparation")
	}
	if !releaseCalled {
		t.Fatal("expected account release on restore failure")
	}
	if !completerCalled {
		t.Fatal("expected task completion on preparation failure")
	}
}

func TestPrepareClaimedTaskContextCompletesFailForOwnershipMismatch(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   3,
		ClaimedBy: "worker-claim-ctx-03",
	}

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{Account: domain.Account{ID: accountID}}, nil
		},
		releaseFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
			return nil
		},
	}
	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			return domain.SessionMetadata{}, nil, domain.NewDomainError(
				domain.ErrorCodeSessionOwnershipMismatch,
				"ownership mismatch",
			)
		},
	}
	taskCompleter := &mockExecutionTaskCompleter{
		completeFn: func(
			ctx context.Context,
			taskID uuid.UUID,
			claimedBy string,
			finalStatus domain.TaskStatus,
			errorCode domain.ErrorCode,
			resultReason string,
		) (domain.Task, error) {
			if finalStatus != domain.TaskStatusFail {
				t.Fatalf("expected status %s, got %s", domain.TaskStatusFail, finalStatus)
			}
			if errorCode != domain.ErrorCodeSessionOwnershipMismatch {
				t.Fatalf("expected error code %s, got %s", domain.ErrorCodeSessionOwnershipMismatch, errorCode)
			}
			if !strings.Contains(resultReason, "status=fail") {
				t.Fatalf("expected deterministic status marker in result reason, got %q", resultReason)
			}
			if !strings.Contains(resultReason, "error_code="+string(domain.ErrorCodeSessionOwnershipMismatch)) {
				t.Fatalf("expected deterministic error_code marker in result reason, got %q", resultReason)
			}
			return domain.Task{
				ID:     taskID,
				Status: finalStatus,
			}, nil
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger(), taskCompleter)
	_, err := service.PrepareClaimedTaskContext(context.Background(), task)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionOwnershipMismatch) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeSessionOwnershipMismatch, err)
	}
}

func TestPrepareClaimedTaskContextCompletesFailForCorruptedPayload(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   5,
		ClaimedBy: "worker-claim-ctx-03b",
	}

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{Account: domain.Account{ID: accountID}}, nil
		},
		releaseFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
			return nil
		},
	}
	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			return domain.SessionMetadata{}, nil, domain.NewDomainError(
				domain.ErrorCodeSessionPayloadCorrupted,
				"payload corrupted",
			)
		},
	}
	taskCompleter := &mockExecutionTaskCompleter{
		completeFn: func(
			ctx context.Context,
			taskID uuid.UUID,
			claimedBy string,
			finalStatus domain.TaskStatus,
			errorCode domain.ErrorCode,
			resultReason string,
		) (domain.Task, error) {
			if finalStatus != domain.TaskStatusFail {
				t.Fatalf("expected status %s, got %s", domain.TaskStatusFail, finalStatus)
			}
			if errorCode != domain.ErrorCodeSessionPayloadCorrupted {
				t.Fatalf("expected error code %s, got %s", domain.ErrorCodeSessionPayloadCorrupted, errorCode)
			}
			if !strings.Contains(resultReason, "status=fail") {
				t.Fatalf("expected deterministic status marker in result reason, got %q", resultReason)
			}
			if !strings.Contains(resultReason, "error_code="+string(domain.ErrorCodeSessionPayloadCorrupted)) {
				t.Fatalf("expected deterministic error_code marker in result reason, got %q", resultReason)
			}
			return domain.Task{
				ID:     taskID,
				Status: finalStatus,
			}, nil
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger(), taskCompleter)
	_, err := service.PrepareClaimedTaskContext(context.Background(), task)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadCorrupted) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeSessionPayloadCorrupted, err)
	}
}

func TestPrepareClaimedTaskContextCorruptedPayloadStaysTerminalWhenBootstrapEnabled(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   6,
		ClaimedBy: "worker-claim-ctx-terminal-bootstrap",
	}

	completerCalled := false
	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{Account: domain.Account{ID: accountID}}, nil
		},
		releaseFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
			return nil
		},
	}
	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			return domain.SessionMetadata{}, nil, domain.NewDomainError(
				domain.ErrorCodeSessionPayloadCorrupted,
				"payload corrupted",
			)
		},
	}
	taskCompleter := &mockExecutionTaskCompleter{
		completeFn: func(
			ctx context.Context,
			taskID uuid.UUID,
			claimedBy string,
			finalStatus domain.TaskStatus,
			errorCode domain.ErrorCode,
			resultReason string,
		) (domain.Task, error) {
			completerCalled = true
			if finalStatus != domain.TaskStatusFail {
				t.Fatalf("expected status %s, got %s", domain.TaskStatusFail, finalStatus)
			}
			if errorCode != domain.ErrorCodeSessionPayloadCorrupted {
				t.Fatalf("expected error code %s, got %s", domain.ErrorCodeSessionPayloadCorrupted, errorCode)
			}
			return domain.Task{ID: taskID, Status: finalStatus}, nil
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger(), taskCompleter).
		WithSessionBootstrapPolicy(SessionBootstrapPolicy{
			BootstrapLoginEnabled:         true,
			AllowMissingPayloadOnFirstRun: true,
		})
	_, err := service.PrepareClaimedTaskContext(context.Background(), task)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadCorrupted) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeSessionPayloadCorrupted, err)
	}
	if !completerCalled {
		t.Fatal("expected terminal preparation error to complete task")
	}
}

func TestPrepareClaimedTaskContextJoinsCompletionFailure(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   1,
		ClaimedBy: "worker-claim-ctx-04",
	}
	completeErr := errors.New("complete failed")

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{Account: domain.Account{ID: accountID}}, nil
		},
		releaseFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
			return nil
		},
	}
	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			return domain.SessionMetadata{}, nil, domain.NewDomainError(
				domain.ErrorCodeSessionPayloadMissing,
				"payload missing",
			)
		},
	}
	taskCompleter := &mockExecutionTaskCompleter{
		completeFn: func(
			ctx context.Context,
			taskID uuid.UUID,
			claimedBy string,
			finalStatus domain.TaskStatus,
			errorCode domain.ErrorCode,
			resultReason string,
		) (domain.Task, error) {
			return domain.Task{}, completeErr
		},
	}

	service := NewExecutionService(guard, restorer, testExecutionLogger(), taskCompleter)
	_, err := service.PrepareClaimedTaskContext(context.Background(), task)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
		t.Fatalf("expected source error code %s, got %v", domain.ErrorCodeSessionPayloadMissing, err)
	}
	if !errors.Is(err, completeErr) {
		t.Fatalf("expected joined completion error, got %v", err)
	}
}

func TestPrepareClaimedTaskContextRejectsNonRunningTask(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusQueued,
		ClaimedBy: "worker-claim-ctx-05",
	}
	completerCalled := false

	taskCompleter := &mockExecutionTaskCompleter{
		completeFn: func(
			ctx context.Context,
			taskID uuid.UUID,
			claimedBy string,
			finalStatus domain.TaskStatus,
			errorCode domain.ErrorCode,
			resultReason string,
		) (domain.Task, error) {
			completerCalled = true
			return domain.Task{}, nil
		},
	}

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
		taskCompleter,
	)
	_, err := service.PrepareClaimedTaskContext(context.Background(), task)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeTaskNotRunning) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeTaskNotRunning, err)
	}
	if completerCalled {
		t.Fatal("task completer must not be called for invalid task preconditions")
	}
}

func TestPrepareClaimedTaskContextLogsClaimMetadata(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   7,
		ClaimedBy: "worker-claim-ctx-06",
	}

	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))

	guard := &mockExecutionContextGuard{
		acquireFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{
				Account: domain.Account{ID: accountID},
				Proxy:   domain.Proxy{ID: uuid.New()},
			}, nil
		},
	}
	restorer := &mockExecutionSessionRestorer{
		restoreFn: func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, []byte, error) {
			return domain.SessionMetadata{
				AccountID: accountID,
				Revision:  11,
				Status:    domain.SessionStatusValid,
				ObjectKey: "accounts/" + accountID.String() + "/sessions/11.json",
			}, []byte(`{"cookies":[]}`), nil
		},
	}

	service := NewExecutionService(guard, restorer, logger)
	_, err := service.PrepareClaimedTaskContext(context.Background(), task)
	if err != nil {
		t.Fatalf("PrepareClaimedTaskContext() error = %v", err)
	}

	output := buffer.String()
	if !strings.Contains(output, "execution_context.prepared") {
		t.Fatalf("expected log output to contain execution_context.prepared, got %q", output)
	}
	if !strings.Contains(output, "task_id="+task.ID.String()) {
		t.Fatalf("expected log output to contain task_id=%s, got %q", task.ID.String(), output)
	}
	if !strings.Contains(output, "attempt=7") {
		t.Fatalf("expected log output to contain attempt=7, got %q", output)
	}
}

func TestResolveBootstrapForClaimedTaskSavesSessionAndMarksReadyForFollow(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   1,
		ClaimedBy: "worker-bootstrap-resolve-success",
	}
	prepared := PreparedExecutionContext{
		AccountWithProxy: domain.AccountWithProxy{
			Account: domain.Account{
				ID:               task.AccountID,
				CredentialSource: domain.CredentialSourceEnv,
				CredentialRef:    "env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD",
			},
		},
		TaskID:             task.ID,
		Attempt:            task.Attempt,
		ExecutionContextID: task.ClaimedBy,
		ReadyForFollowFlow: false,
		BootstrapRequired:  true,
		BootstrapReason:    domain.ErrorCodeAuthBootstrapRequired,
		BootstrapSource:    domain.ErrorCodeSessionMetadataNotFound,
	}

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{
			saveFn: func(ctx context.Context, gotAccountID uuid.UUID, accountLogin string, payload []byte) (domain.SessionMetadata, error) {
				if gotAccountID != task.AccountID {
					t.Fatalf("expected account id %s, got %s", task.AccountID.String(), gotAccountID.String())
				}
				if len(payload) == 0 {
					t.Fatal("expected bootstrap payload to be persisted")
				}
				return domain.SessionMetadata{
					AccountID: gotAccountID,
					Revision:  12,
					Status:    domain.SessionStatusValid,
					ObjectKey: "accounts/" + gotAccountID.String() + "/sessions/12.json",
				}, nil
			},
		},
		testExecutionLogger(),
	)
	artifactStore := &mockExecutionArtifactStore{}
	service = service.WithSessionBootstrapPolicy(SessionBootstrapPolicy{
		BootstrapLoginEnabled: true,
	}).WithBootstrapLoginRunner(&mockExecutionBootstrapRunner{
		runFn: func(ctx context.Context, input domain.BootstrapLoginInput) (domain.BootstrapLoginResult, error) {
			if input.CredentialSource != domain.CredentialSourceEnv {
				t.Fatalf("expected credential source env, got %s", input.CredentialSource)
			}
			return domain.BootstrapLoginResult{
				Outcome:        domain.BootstrapLoginOutcomeSuccess,
				SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"bootstrap"}]}`),
				AuthScreenshots: map[string][]byte{
					"auth-home":                     []byte("home"),
					"auth-post-submit-profile-open": []byte("post-submit"),
				},
				Diagnostics: domain.BootstrapLoginDiagnostics{
					Engine:     "mock",
					DurationMS: 2,
				},
			}, nil
		},
	}).WithArtifactStore(artifactStore)

	resolved, err := service.ResolveBootstrapForClaimedTask(context.Background(), task, prepared)
	if err != nil {
		t.Fatalf("ResolveBootstrapForClaimedTask() error = %v", err)
	}
	if resolved.BootstrapRequired {
		t.Fatal("expected bootstrap_required=false after successful bootstrap")
	}
	if !resolved.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=true after successful bootstrap")
	}
	if resolved.SessionMetadata.Revision != 12 {
		t.Fatalf("expected session revision 12, got %d", resolved.SessionMetadata.Revision)
	}
	if len(resolved.SessionPayload) == 0 {
		t.Fatal("expected session payload to be set from bootstrap result")
	}
	if artifactStore.saveCount != 2 {
		t.Fatalf("expected 2 auth screenshots to be persisted, got %d", artifactStore.saveCount)
	}
}

func TestResolveBootstrapForClaimedTaskReturnsTypedErrorWhenBootstrapDisabled(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   1,
		ClaimedBy: "worker-bootstrap-disabled",
	}
	prepared := PreparedExecutionContext{
		AccountWithProxy: domain.AccountWithProxy{
			Account: domain.Account{
				ID:               task.AccountID,
				CredentialSource: domain.CredentialSourceEnv,
				CredentialRef:    "env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD",
			},
		},
		TaskID:             task.ID,
		Attempt:            task.Attempt,
		ExecutionContextID: task.ClaimedBy,
		ReadyForFollowFlow: false,
		BootstrapRequired:  true,
		BootstrapReason:    domain.ErrorCodeAuthBootstrapRequired,
		BootstrapSource:    domain.ErrorCodeSessionMetadataNotFound,
	}

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	).WithSessionBootstrapPolicy(SessionBootstrapPolicy{
		BootstrapLoginEnabled: false,
	})

	_, err := service.ResolveBootstrapForClaimedTask(context.Background(), task, prepared)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeAuthBootstrapDisabled) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeAuthBootstrapDisabled, err)
	}
}

func TestResolveBootstrapForClaimedTaskReturnsBootstrapRequiredWhenRunnerMissing(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   1,
		ClaimedBy: "worker-bootstrap-runner-missing",
	}
	prepared := PreparedExecutionContext{
		AccountWithProxy: domain.AccountWithProxy{
			Account: domain.Account{
				ID:               task.AccountID,
				CredentialSource: domain.CredentialSourceEnv,
				CredentialRef:    "env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD",
			},
		},
		TaskID:             task.ID,
		Attempt:            task.Attempt,
		ExecutionContextID: task.ClaimedBy,
		ReadyForFollowFlow: false,
		BootstrapRequired:  true,
		BootstrapReason:    domain.ErrorCodeAuthBootstrapRequired,
		BootstrapSource:    domain.ErrorCodeSessionMetadataNotFound,
	}

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	).WithSessionBootstrapPolicy(SessionBootstrapPolicy{
		BootstrapLoginEnabled: true,
	})

	_, err := service.ResolveBootstrapForClaimedTask(context.Background(), task, prepared)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeAuthBootstrapRequired) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeAuthBootstrapRequired, err)
	}
}

func TestResolveBootstrapForClaimedTaskMapsInvalidCredentialOutcome(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   1,
		ClaimedBy: "worker-bootstrap-invalid-credentials-outcome",
	}
	prepared := PreparedExecutionContext{
		AccountWithProxy: domain.AccountWithProxy{
			Account: domain.Account{
				ID:               task.AccountID,
				CredentialSource: domain.CredentialSourceEnv,
				CredentialRef:    "env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD",
			},
		},
		TaskID:             task.ID,
		Attempt:            task.Attempt,
		ExecutionContextID: task.ClaimedBy,
		ReadyForFollowFlow: false,
		BootstrapRequired:  true,
		BootstrapReason:    domain.ErrorCodeAuthBootstrapRequired,
		BootstrapSource:    domain.ErrorCodeSessionPayloadMissing,
	}

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	).WithSessionBootstrapPolicy(SessionBootstrapPolicy{
		BootstrapLoginEnabled: true,
	}).WithBootstrapLoginRunner(&mockExecutionBootstrapRunner{
		runFn: func(ctx context.Context, input domain.BootstrapLoginInput) (domain.BootstrapLoginResult, error) {
			return domain.BootstrapLoginResult{
				Outcome: domain.BootstrapLoginOutcomeAuthInvalidCredentials,
				Diagnostics: domain.BootstrapLoginDiagnostics{
					Engine:     "mock",
					DurationMS: 1,
				},
			}, nil
		},
	})

	_, err := service.ResolveBootstrapForClaimedTask(context.Background(), task, prepared)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeAuthInvalidCredentials) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeAuthInvalidCredentials, err)
	}
}

func TestResolveBootstrapForClaimedTaskReturnsSessionSaveFailedWithSourceCode(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   1,
		ClaimedBy: "worker-bootstrap-save-failure",
	}
	prepared := PreparedExecutionContext{
		AccountWithProxy: domain.AccountWithProxy{
			Account: domain.Account{
				ID:               task.AccountID,
				CredentialSource: domain.CredentialSourceEnv,
				CredentialRef:    "env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD",
			},
		},
		TaskID:             task.ID,
		Attempt:            task.Attempt,
		ExecutionContextID: task.ClaimedBy,
		ReadyForFollowFlow: false,
		BootstrapRequired:  true,
		BootstrapReason:    domain.ErrorCodeAuthBootstrapRequired,
		BootstrapSource:    domain.ErrorCodeSessionMetadataNotFound,
	}

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{
			saveFn: func(ctx context.Context, accountID uuid.UUID, accountLogin string, payload []byte) (domain.SessionMetadata, error) {
				return domain.SessionMetadata{}, domain.NewDomainError(
					domain.ErrorCodeSessionPayloadInvalid,
					"bootstrap session payload is invalid",
				)
			},
		},
		testExecutionLogger(),
	).WithSessionBootstrapPolicy(SessionBootstrapPolicy{
		BootstrapLoginEnabled: true,
	}).WithBootstrapLoginRunner(&mockExecutionBootstrapRunner{
		runFn: func(ctx context.Context, input domain.BootstrapLoginInput) (domain.BootstrapLoginResult, error) {
			return domain.BootstrapLoginResult{
				Outcome:        domain.BootstrapLoginOutcomeSuccess,
				SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"bootstrap"}]}`),
				Diagnostics: domain.BootstrapLoginDiagnostics{
					Engine: "mock",
				},
			}, nil
		},
	})

	_, err := service.ResolveBootstrapForClaimedTask(context.Background(), task, prepared)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionSaveFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionSaveFailed, err)
	}
	if !strings.Contains(err.Error(), "source_error_code=session_payload_invalid") {
		t.Fatalf("expected source error code marker in error, got %v", err)
	}
}

func TestResolveBootstrapForClaimedTaskDoesNotLogSensitiveCredentialOrSessionData(t *testing.T) {
	t.Parallel()

	task := domain.Task{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Status:    domain.TaskStatusRunning,
		Attempt:   1,
		ClaimedBy: "worker-bootstrap-log-safety",
	}
	prepared := PreparedExecutionContext{
		AccountWithProxy: domain.AccountWithProxy{
			Account: domain.Account{
				ID:               task.AccountID,
				CredentialSource: domain.CredentialSourceEnv,
				CredentialRef:    "env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD",
			},
		},
		TaskID:             task.ID,
		Attempt:            task.Attempt,
		ExecutionContextID: task.ClaimedBy,
		ReadyForFollowFlow: false,
		BootstrapRequired:  true,
		BootstrapReason:    domain.ErrorCodeAuthBootstrapRequired,
		BootstrapSource:    domain.ErrorCodeSessionMetadataNotFound,
	}

	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{
			saveFn: func(ctx context.Context, gotAccountID uuid.UUID, accountLogin string, payload []byte) (domain.SessionMetadata, error) {
				return domain.SessionMetadata{
					AccountID: gotAccountID,
					Revision:  2,
					Status:    domain.SessionStatusValid,
					ObjectKey: "accounts/" + gotAccountID.String() + "/sessions/2.json",
				}, nil
			},
		},
		logger,
	).WithSessionBootstrapPolicy(SessionBootstrapPolicy{
		BootstrapLoginEnabled: true,
	}).WithBootstrapLoginRunner(&mockExecutionBootstrapRunner{
		runFn: func(ctx context.Context, input domain.BootstrapLoginInput) (domain.BootstrapLoginResult, error) {
			return domain.BootstrapLoginResult{
				Outcome:        domain.BootstrapLoginOutcomeSuccess,
				SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"super-secret-cookie"}]}`),
				Diagnostics: domain.BootstrapLoginDiagnostics{
					Engine: "mock",
				},
			}, nil
		},
	})

	_, err := service.ResolveBootstrapForClaimedTask(context.Background(), task, prepared)
	if err != nil {
		t.Fatalf("ResolveBootstrapForClaimedTask() error = %v", err)
	}

	logs := buffer.String()
	if strings.Contains(logs, "super-secret-cookie") {
		t.Fatalf("expected session payload to be redacted from logs, got %q", logs)
	}
	if strings.Contains(logs, "FOLLOWER_BOOTSTRAP_PASSWORD") {
		t.Fatalf("expected credential references to be omitted from logs, got %q", logs)
	}
}

func TestRunFollowFlowReturnsErrorWhenRunnerNotConfigured(t *testing.T) {
	t.Parallel()

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	)

	accountID := uuid.New()
	_, _, err := service.RunFollowFlow(context.Background(), domain.FollowFlowInput{
		TaskID:             uuid.New(),
		AccountID:          accountID,
		Attempt:            1,
		ExecutionContextID: "worker-follow-no-runner",
		SessionMetadata: domain.SessionMetadata{
			AccountID: accountID,
			Revision:  1,
			Status:    domain.SessionStatusValid,
			ObjectKey: "accounts/" + accountID.String() + "/sessions/1.json",
		},
		SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		TargetProfile:  "target-user",
	})
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInternal) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeInternal, err)
	}
}

func TestRunFollowFlowDelegatesToConfiguredRunner(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	expectedDiagnostics := domain.FollowFlowDiagnostics{
		Engine:              "mock",
		WarmupDurationMS:    3,
		ExecutionDurationMS: 4,
	}

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	).WithFollowFlowRunner(&mockExecutionFollowFlowRunner{
		runFn: func(
			ctx context.Context,
			input domain.FollowFlowInput,
		) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
			if input.TargetProfile != "target-user" {
				t.Fatalf("expected target profile target-user, got %s", input.TargetProfile)
			}
			return domain.FollowFlowOutcomeAlreadyDone, expectedDiagnostics, nil
		},
	})

	outcome, diagnostics, err := service.RunFollowFlow(context.Background(), domain.FollowFlowInput{
		TaskID:             uuid.New(),
		AccountID:          accountID,
		Attempt:            1,
		ExecutionContextID: "worker-follow-runner",
		SessionMetadata: domain.SessionMetadata{
			AccountID: accountID,
			Revision:  1,
			Status:    domain.SessionStatusValid,
			ObjectKey: "accounts/" + accountID.String() + "/sessions/1.json",
		},
		SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		TargetProfile:  "target-user",
	})
	if err != nil {
		t.Fatalf("RunFollowFlow() error = %v", err)
	}
	if outcome != domain.FollowFlowOutcomeAlreadyDone {
		t.Fatalf("expected outcome %s, got %s", domain.FollowFlowOutcomeAlreadyDone, outcome)
	}
	if diagnostics != expectedDiagnostics {
		t.Fatalf("expected diagnostics %+v, got %+v", expectedDiagnostics, diagnostics)
	}
}

func TestVerifyFollowResultDelegatesToConfiguredRunner(t *testing.T) {
	t.Parallel()

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	).WithVerifyFlowRunner(&mockExecutionVerifyFlowRunner{
		verifyFn: func(
			ctx context.Context,
			input domain.FollowVerificationInput,
		) (domain.FollowVerificationResult, error) {
			if input.Outcome != domain.FollowFlowOutcomeCompleted {
				t.Fatalf("expected completed outcome, got %s", input.Outcome)
			}
			return domain.FollowVerificationResult{
				Verified:          true,
				Signal:            domain.FollowVerificationSignalFollowConfirmed,
				Reason:            "verified",
				SessionChanged:    true,
				SessionPayload:    []byte(`{"cookies":[{"name":"sid","value":"updated"}]}`),
				ScreenshotPayload: []byte("fake-png"),
			}, nil
		},
	})

	result, err := service.VerifyFollowResult(context.Background(), domain.FollowVerificationInput{
		TaskID:             uuid.New(),
		AccountID:          uuid.New(),
		Attempt:            1,
		ExecutionContextID: "worker-verify",
		TargetProfile:      "target-user",
		Outcome:            domain.FollowFlowOutcomeCompleted,
		SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"seed"}]}`),
	})
	if err != nil {
		t.Fatalf("VerifyFollowResult() error = %v", err)
	}
	if !result.Verified {
		t.Fatal("expected verified=true")
	}
	if result.Signal != domain.FollowVerificationSignalFollowConfirmed {
		t.Fatalf("expected signal %s, got %s", domain.FollowVerificationSignalFollowConfirmed, result.Signal)
	}
}

func TestVerifyFollowResultRejectsUnverifiedResultWithoutErrorCode(t *testing.T) {
	t.Parallel()

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	).WithVerifyFlowRunner(&mockExecutionVerifyFlowRunner{
		verifyFn: func(
			ctx context.Context,
			input domain.FollowVerificationInput,
		) (domain.FollowVerificationResult, error) {
			return domain.FollowVerificationResult{
				Verified:          false,
				Signal:            domain.FollowVerificationSignalVerifyFailed,
				Reason:            "verify UI did not confirm follow state",
				SessionChanged:    false,
				ScreenshotPayload: []byte("fake-png"),
			}, nil
		},
	})

	_, err := service.VerifyFollowResult(context.Background(), domain.FollowVerificationInput{
		TaskID:             uuid.New(),
		AccountID:          uuid.New(),
		Attempt:            1,
		ExecutionContextID: "worker-verify",
		TargetProfile:      "target-user",
		Outcome:            domain.FollowFlowOutcomeCompleted,
		SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"seed"}]}`),
	})
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowVerifyFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowVerifyFailed, err)
	}
}

func TestFinalizeFollowExecutionPersistsArtifactsResultAndSession(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()

	var savedResult domain.FollowResult
	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{
			saveFn: func(ctx context.Context, gotAccountID uuid.UUID, accountLogin string, payload []byte) (domain.SessionMetadata, error) {
				if gotAccountID != accountID {
					t.Fatalf("expected account id %s, got %s", accountID.String(), gotAccountID.String())
				}
				if len(payload) == 0 {
					t.Fatal("expected non-empty session payload")
				}
				return domain.SessionMetadata{
					AccountID: gotAccountID,
					Revision:  5,
					Status:    domain.SessionStatusValid,
					ObjectKey: "accounts/" + gotAccountID.String() + "/sessions/5.json",
				}, nil
			},
		},
		testExecutionLogger(),
	).WithResultRepository(&mockExecutionResultRepository{
		upsertFn: func(ctx context.Context, result domain.FollowResult) (domain.FollowResult, error) {
			savedResult = result
			return result, nil
		},
	}).WithScreenshotStore(&mockExecutionScreenshotStore{}).
		WithArtifactStore(&mockExecutionArtifactStore{})

	stored, err := service.FinalizeFollowExecution(
		context.Background(),
		domain.FollowExecutionFinalizationInput{
			TaskID:        taskID,
			AccountID:     accountID,
			TargetProfile: "target-user",
			Attempt:       1,
			FollowOutcome: domain.FollowFlowOutcomeCompleted,
			FollowDiagnostics: domain.FollowFlowDiagnostics{
				Engine:              "mock",
				WarmupDurationMS:    3,
				ExecutionDurationMS: 8,
			},
			Verification: domain.FollowVerificationResult{
				Verified:          true,
				Signal:            domain.FollowVerificationSignalFollowConfirmed,
				Reason:            "verified",
				SessionChanged:    true,
				SessionPayload:    []byte(`{"cookies":[{"name":"sid","value":"updated"}]}`),
				ScreenshotPayload: []byte("fake-png"),
			},
			SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		},
	)
	if err != nil {
		t.Fatalf("FinalizeFollowExecution() error = %v", err)
	}
	if stored.TaskID != taskID {
		t.Fatalf("expected task id %s, got %s", taskID.String(), stored.TaskID.String())
	}
	if !savedResult.Verified {
		t.Fatal("expected saved result to be verified")
	}
	if savedResult.SessionRevision != 5 {
		t.Fatalf("expected session revision 5, got %d", savedResult.SessionRevision)
	}
	if len(savedResult.ArtifactObjectKeys) == 0 {
		t.Fatal("expected artifact keys to be persisted")
	}
	if savedResult.ScreenshotObjectKey == "" {
		t.Fatal("expected screenshot key to be persisted")
	}
}

func TestFinalizeFollowExecutionKeepsPreparedSessionRevisionWhenSessionUnchanged(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()

	var savedResult domain.FollowResult
	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	).WithResultRepository(&mockExecutionResultRepository{
		upsertFn: func(ctx context.Context, result domain.FollowResult) (domain.FollowResult, error) {
			savedResult = result
			return result, nil
		},
	}).WithScreenshotStore(&mockExecutionScreenshotStore{}).
		WithArtifactStore(&mockExecutionArtifactStore{})

	_, err := service.FinalizeFollowExecution(
		context.Background(),
		domain.FollowExecutionFinalizationInput{
			TaskID:          taskID,
			AccountID:       accountID,
			TargetProfile:   "target-user",
			Attempt:         1,
			SessionRevision: 9,
			FollowOutcome:   domain.FollowFlowOutcomeCompleted,
			FollowDiagnostics: domain.FollowFlowDiagnostics{
				Engine:              "mock",
				WarmupDurationMS:    1,
				ExecutionDurationMS: 2,
			},
			Verification: domain.FollowVerificationResult{
				Verified:          true,
				Signal:            domain.FollowVerificationSignalFollowConfirmed,
				Reason:            "verified without session mutation",
				SessionChanged:    false,
				ScreenshotPayload: []byte("fake-png"),
			},
		},
	)
	if err != nil {
		t.Fatalf("FinalizeFollowExecution() error = %v", err)
	}
	if savedResult.SessionRevision != 9 {
		t.Fatalf("expected session revision 9, got %d", savedResult.SessionRevision)
	}
}

func TestFinalizeFollowExecutionCleansArtifactsWhenResultPersistenceFails(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()

	screenshotStore := &mockExecutionScreenshotStore{}
	artifactStore := &mockExecutionArtifactStore{}
	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	).WithResultRepository(&mockExecutionResultRepository{
		upsertFn: func(ctx context.Context, result domain.FollowResult) (domain.FollowResult, error) {
			return domain.FollowResult{}, errors.New("db unavailable")
		},
	}).WithScreenshotStore(screenshotStore).
		WithArtifactStore(artifactStore)

	_, err := service.FinalizeFollowExecution(
		context.Background(),
		domain.FollowExecutionFinalizationInput{
			TaskID:        taskID,
			AccountID:     accountID,
			TargetProfile: "target-user",
			Attempt:       1,
			FollowOutcome: domain.FollowFlowOutcomeNavigationFailed,
			FollowDiagnostics: domain.FollowFlowDiagnostics{
				Engine:              "mock",
				WarmupDurationMS:    1,
				ExecutionDurationMS: 2,
			},
			Verification: domain.FollowVerificationResult{
				Verified:          false,
				Signal:            domain.FollowVerificationSignalNavigationFailed,
				Reason:            "transient runtime failure",
				ErrorCode:         domain.ErrorCodeFollowNavigationFailed,
				SessionChanged:    false,
				ScreenshotPayload: []byte("fake-png"),
			},
		},
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowResultPersistFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowResultPersistFailed, err)
	}
	if screenshotStore.deleteCount == 0 {
		t.Fatal("expected screenshot cleanup on follow result persistence failure")
	}
	if artifactStore.deleteCount == 0 {
		t.Fatal("expected artifact cleanup on follow result persistence failure")
	}
}

func TestFinalizeFollowExecutionFailsWhenSessionChangedAndSaverMissing(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()

	screenshotStore := &mockExecutionScreenshotStore{}
	artifactStore := &mockExecutionArtifactStore{}
	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	).
		WithResultRepository(&mockExecutionResultRepository{}).
		WithScreenshotStore(screenshotStore).
		WithArtifactStore(artifactStore).
		WithSessionSaver(nil)

	_, err := service.FinalizeFollowExecution(
		context.Background(),
		domain.FollowExecutionFinalizationInput{
			TaskID:        taskID,
			AccountID:     accountID,
			TargetProfile: "target-user",
			Attempt:       1,
			FollowOutcome: domain.FollowFlowOutcomeCompleted,
			FollowDiagnostics: domain.FollowFlowDiagnostics{
				Engine:              "mock",
				WarmupDurationMS:    2,
				ExecutionDurationMS: 4,
			},
			Verification: domain.FollowVerificationResult{
				Verified:          true,
				Signal:            domain.FollowVerificationSignalFollowConfirmed,
				Reason:            "verified",
				SessionChanged:    true,
				SessionPayload:    []byte(`{"cookies":[{"name":"sid","value":"updated"}]}`),
				ScreenshotPayload: []byte("fake-png"),
			},
			SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		},
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionSaveFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionSaveFailed, err)
	}
	if screenshotStore.deleteCount == 0 {
		t.Fatal("expected screenshot cleanup when session saver is missing")
	}
	if artifactStore.deleteCount == 0 {
		t.Fatal("expected artifact cleanup when session saver is missing")
	}
}

func TestFinalizeFollowExecutionReturnsSessionSaveFailedWithSourceCodeAndCleansArtifacts(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()

	screenshotStore := &mockExecutionScreenshotStore{}
	artifactStore := &mockExecutionArtifactStore{}
	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{
			saveFn: func(ctx context.Context, accountID uuid.UUID, accountLogin string, payload []byte) (domain.SessionMetadata, error) {
				return domain.SessionMetadata{}, domain.NewDomainError(
					domain.ErrorCodeSessionPayloadInvalid,
					"session payload is invalid",
				)
			},
		},
		testExecutionLogger(),
	).
		WithResultRepository(&mockExecutionResultRepository{}).
		WithScreenshotStore(screenshotStore).
		WithArtifactStore(artifactStore)

	_, err := service.FinalizeFollowExecution(
		context.Background(),
		domain.FollowExecutionFinalizationInput{
			TaskID:        taskID,
			AccountID:     accountID,
			TargetProfile: "target-user",
			Attempt:       1,
			FollowOutcome: domain.FollowFlowOutcomeCompleted,
			FollowDiagnostics: domain.FollowFlowDiagnostics{
				Engine:              "mock",
				WarmupDurationMS:    2,
				ExecutionDurationMS: 4,
			},
			Verification: domain.FollowVerificationResult{
				Verified:          true,
				Signal:            domain.FollowVerificationSignalFollowConfirmed,
				Reason:            "verified",
				SessionChanged:    true,
				SessionPayload:    []byte(`{"cookies":[{"name":"sid","value":"updated"}]}`),
				ScreenshotPayload: []byte("fake-png"),
			},
			SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		},
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionSaveFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionSaveFailed, err)
	}
	if !strings.Contains(err.Error(), "source_error_code=session_payload_invalid") {
		t.Fatalf("expected source error code marker in error, got %v", err)
	}
	if screenshotStore.deleteCount == 0 {
		t.Fatal("expected screenshot cleanup when session save fails")
	}
	if artifactStore.deleteCount == 0 {
		t.Fatal("expected artifact cleanup when session save fails")
	}
}

func TestFinalizeFollowExecutionLogsArtifactAndResultEvents(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()

	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		logger,
	).WithResultRepository(&mockExecutionResultRepository{}).
		WithScreenshotStore(&mockExecutionScreenshotStore{}).
		WithArtifactStore(&mockExecutionArtifactStore{})

	_, err := service.FinalizeFollowExecution(
		context.Background(),
		domain.FollowExecutionFinalizationInput{
			TaskID:        taskID,
			AccountID:     accountID,
			TargetProfile: "target-user",
			Attempt:       1,
			FollowOutcome: domain.FollowFlowOutcomeCompleted,
			FollowDiagnostics: domain.FollowFlowDiagnostics{
				Engine:              "mock",
				WarmupDurationMS:    1,
				ExecutionDurationMS: 2,
			},
			Verification: domain.FollowVerificationResult{
				Verified:          true,
				Signal:            domain.FollowVerificationSignalFollowConfirmed,
				Reason:            "verified",
				SessionChanged:    true,
				SessionPayload:    []byte(`{"cookies":[{"name":"sid","value":"updated"}]}`),
				ScreenshotPayload: []byte("fake-png"),
			},
			SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		},
	)
	if err != nil {
		t.Fatalf("FinalizeFollowExecution() error = %v", err)
	}

	logs := buffer.String()
	if !strings.Contains(logs, "artifact.saved") {
		t.Fatalf("expected logs to contain artifact.saved, got %q", logs)
	}
	if !strings.Contains(logs, "follow.result.persisted") {
		t.Fatalf("expected logs to contain follow.result.persisted, got %q", logs)
	}
	if !strings.Contains(logs, "session.saved") {
		t.Fatalf("expected logs to contain session.saved, got %q", logs)
	}
	if !strings.Contains(logs, "session_revision=") {
		t.Fatalf("expected logs to contain session_revision attribute, got %q", logs)
	}
	if !strings.Contains(logs, "object_key=") {
		t.Fatalf("expected logs to contain object_key attribute, got %q", logs)
	}
	if !strings.Contains(logs, "save_source=follow_finalization") {
		t.Fatalf("expected logs to contain save_source=follow_finalization, got %q", logs)
	}
}

type mockExecutionContextGuard struct {
	acquireFn func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.AccountWithProxy, error)
	releaseFn func(ctx context.Context, accountID uuid.UUID, executionContextID string) error
}

func (m *mockExecutionContextGuard) Acquire(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
) (domain.AccountWithProxy, error) {
	if m.acquireFn != nil {
		return m.acquireFn(ctx, accountID, executionContextID)
	}
	return domain.AccountWithProxy{}, nil
}

func (m *mockExecutionContextGuard) Release(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
) error {
	if m.releaseFn != nil {
		return m.releaseFn(ctx, accountID, executionContextID)
	}
	return nil
}

type mockExecutionSessionRestorer struct {
	restoreFn func(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, []byte, error)
	saveFn    func(ctx context.Context, accountID uuid.UUID, accountLogin string, payload []byte) (domain.SessionMetadata, error)
}

func (m *mockExecutionSessionRestorer) Restore(
	ctx context.Context,
	accountID uuid.UUID,
) (domain.SessionMetadata, []byte, error) {
	if m.restoreFn != nil {
		return m.restoreFn(ctx, accountID)
	}
	return domain.SessionMetadata{}, nil, nil
}

func (m *mockExecutionSessionRestorer) Save(
	ctx context.Context,
	accountID uuid.UUID,
	accountLogin string,
	payload []byte,
) (domain.SessionMetadata, error) {
	if m.saveFn != nil {
		return m.saveFn(ctx, accountID, accountLogin, payload)
	}
	return domain.SessionMetadata{
		AccountID: accountID,
		Revision:  1,
		Status:    domain.SessionStatusValid,
		ObjectKey: accountID.String() + "/latest.json",
	}, nil
}

type mockExecutionTaskCompleter struct {
	completeFn func(
		ctx context.Context,
		taskID uuid.UUID,
		claimedBy string,
		finalStatus domain.TaskStatus,
		errorCode domain.ErrorCode,
		resultReason string,
	) (domain.Task, error)
}

func (m *mockExecutionTaskCompleter) Complete(
	ctx context.Context,
	taskID uuid.UUID,
	claimedBy string,
	finalStatus domain.TaskStatus,
	errorCode domain.ErrorCode,
	resultReason string,
) (domain.Task, error) {
	if m.completeFn != nil {
		return m.completeFn(ctx, taskID, claimedBy, finalStatus, errorCode, resultReason)
	}

	return domain.Task{}, nil
}

type mockExecutionFollowFlowRunner struct {
	runFn func(
		ctx context.Context,
		input domain.FollowFlowInput,
	) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error)
}

type mockExecutionBootstrapRunner struct {
	runFn func(
		ctx context.Context,
		input domain.BootstrapLoginInput,
	) (domain.BootstrapLoginResult, error)
}

func (m *mockExecutionBootstrapRunner) RunBootstrapLogin(
	ctx context.Context,
	input domain.BootstrapLoginInput,
) (domain.BootstrapLoginResult, error) {
	if m.runFn != nil {
		return m.runFn(ctx, input)
	}
	return domain.BootstrapLoginResult{
		Outcome:        domain.BootstrapLoginOutcomeSuccess,
		SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		Diagnostics: domain.BootstrapLoginDiagnostics{
			Engine: "mock",
		},
	}, nil
}

func (m *mockExecutionFollowFlowRunner) RunFollowFlow(
	ctx context.Context,
	input domain.FollowFlowInput,
) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
	if m.runFn != nil {
		return m.runFn(ctx, input)
	}
	return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{}, nil
}

type mockExecutionVerifyFlowRunner struct {
	verifyFn func(
		ctx context.Context,
		input domain.FollowVerificationInput,
	) (domain.FollowVerificationResult, error)
}

func (m *mockExecutionVerifyFlowRunner) VerifyFollowResult(
	ctx context.Context,
	input domain.FollowVerificationInput,
) (domain.FollowVerificationResult, error) {
	if m.verifyFn != nil {
		return m.verifyFn(ctx, input)
	}
	return domain.FollowVerificationResult{}, nil
}

type mockExecutionResultRepository struct {
	upsertFn func(ctx context.Context, result domain.FollowResult) (domain.FollowResult, error)
	listFn   func(ctx context.Context, query domain.FollowResultsHistoryQuery) ([]domain.FollowResultHistoryEntry, error)
}

func (m *mockExecutionResultRepository) Upsert(
	ctx context.Context,
	result domain.FollowResult,
) (domain.FollowResult, error) {
	if m.upsertFn != nil {
		return m.upsertFn(ctx, result)
	}
	return result, nil
}

func (m *mockExecutionResultRepository) ListHistory(
	ctx context.Context,
	query domain.FollowResultsHistoryQuery,
) ([]domain.FollowResultHistoryEntry, error) {
	if m.listFn != nil {
		return m.listFn(ctx, query)
	}
	return nil, nil
}

type mockExecutionScreenshotStore struct {
	saveCount   int
	deleteCount int
}

func (m *mockExecutionScreenshotStore) Save(
	ctx context.Context,
	accountID uuid.UUID,
	accountLogin string,
	payload []byte,
) (string, error) {
	m.saveCount++
	if strings.TrimSpace(accountLogin) == "" {
		accountLogin = accountID.String()
	}
	return accountLogin + "/screenshot/2026-04-23-101112.png", nil
}

func (m *mockExecutionScreenshotStore) Delete(ctx context.Context, objectKey string) error {
	m.deleteCount++
	return nil
}

type mockExecutionArtifactStore struct {
	saveCount   int
	deleteCount int
}

func (m *mockExecutionArtifactStore) Save(
	ctx context.Context,
	accountID uuid.UUID,
	accountLogin string,
	payload []byte,
) (string, error) {
	m.saveCount++
	if strings.TrimSpace(accountLogin) == "" {
		accountLogin = accountID.String()
	}
	return accountLogin + "/artifacts/2026-04-23-101112.json", nil
}

func (m *mockExecutionArtifactStore) Delete(ctx context.Context, objectKey string) error {
	m.deleteCount++
	return nil
}

func testExecutionLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
