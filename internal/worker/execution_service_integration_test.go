package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"follower/internal/audit"
	"follower/internal/browser"
	"follower/internal/domain"
	postgresrepo "follower/internal/repository/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPrepareClaimedTaskContextIntegrationSuccessKeepsTaskRunningForNextStage(t *testing.T) {
	pool := mustOpenWorkerTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	accountRepository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	sessionRepository := postgresrepo.NewSessionRepository(pool)
	taskRepository := postgresrepo.NewTaskRepository(pool)

	accountID, err := createWorkerTestAccount(context.Background(), accountRepository, "exec-prepare-success-01")
	if err != nil {
		t.Fatalf("createWorkerTestAccount() error = %v", err)
	}

	store := newInMemoryIntegrationSessionStore()
	sessionObjectKey := "accounts/" + accountID.String() + "/sessions/1.json"
	if err := store.SaveObject(sessionObjectKey, []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`)); err != nil {
		t.Fatalf("SaveObject() error = %v", err)
	}
	if _, err := sessionRepository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountID,
		Revision:  1,
		Status:    domain.SessionStatusValid,
		ObjectKey: sessionObjectKey,
	}); err != nil {
		t.Fatalf("sessionRepository.Upsert() error = %v", err)
	}

	queuedTask, claimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-success",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask() error = %v", err)
	}

	accountGuard := NewAccountGuard(accountRepository, domain.DefaultRuntimeGuardrails(), logger)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, store, logger)
	service := NewExecutionService(accountGuard, sessionRestorer, logger, taskRepository)

	prepared, err := service.PrepareClaimedTaskContext(context.Background(), claimedTask)
	if err != nil {
		t.Fatalf("PrepareClaimedTaskContext() error = %v", err)
	}

	if !prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=true")
	}
	if prepared.TaskID != queuedTask.ID {
		t.Fatalf("expected task id %s, got %s", queuedTask.ID.String(), prepared.TaskID.String())
	}
	if prepared.ExecutionContextID != claimedTask.ClaimedBy {
		t.Fatalf("expected execution context %s, got %s", claimedTask.ClaimedBy, prepared.ExecutionContextID)
	}

	gotTask, err := taskRepository.GetByID(context.Background(), queuedTask.ID)
	if err != nil {
		t.Fatalf("taskRepository.GetByID() error = %v", err)
	}
	if gotTask.Status != domain.TaskStatusRunning {
		t.Fatalf("expected task status %s after successful prepare, got %s", domain.TaskStatusRunning, gotTask.Status)
	}
	if gotTask.ClaimedBy != claimedTask.ClaimedBy {
		t.Fatalf("expected claimed_by %s, got %s", claimedTask.ClaimedBy, gotTask.ClaimedBy)
	}

	accountWithProxy, err := accountRepository.GetAccountWithProxy(context.Background(), accountID)
	if err != nil {
		t.Fatalf("accountRepository.GetAccountWithProxy() error = %v", err)
	}
	if accountWithProxy.Account.ActiveExecutionContextID != claimedTask.ClaimedBy {
		t.Fatalf(
			"expected account execution context %s, got %s",
			claimedTask.ClaimedBy,
			accountWithProxy.Account.ActiveExecutionContextID,
		)
	}
}

func TestPrepareClaimedTaskContextIntegrationSuccessWithoutProxyWhenBindingDisabled(t *testing.T) {
	pool := mustOpenWorkerTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	guardrails := proxyBindingDisabledRuntimeGuardrails()
	accountRepository := postgresrepo.NewAccountRepository(pool, guardrails)
	sessionRepository := postgresrepo.NewSessionRepository(pool)
	taskRepository := postgresrepo.NewTaskRepository(pool)

	accountID, err := createWorkerTestAccountWithoutProxy(
		context.Background(),
		accountRepository,
		"exec-prepare-success-no-proxy-01",
	)
	if err != nil {
		t.Fatalf("createWorkerTestAccountWithoutProxy() error = %v", err)
	}

	store := newInMemoryIntegrationSessionStore()
	sessionObjectKey := "accounts/" + accountID.String() + "/sessions/101.json"
	if err := store.SaveObject(sessionObjectKey, []byte(`{"cookies":[{"name":"sid","value":"ok-no-proxy"}]}`)); err != nil {
		t.Fatalf("SaveObject() error = %v", err)
	}
	if _, err := sessionRepository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountID,
		Revision:  101,
		Status:    domain.SessionStatusValid,
		ObjectKey: sessionObjectKey,
	}); err != nil {
		t.Fatalf("sessionRepository.Upsert() error = %v", err)
	}

	queuedTask, claimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-success-no-proxy",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask() error = %v", err)
	}

	accountGuard := NewAccountGuard(accountRepository, guardrails, logger)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, store, logger)
	service := NewExecutionService(accountGuard, sessionRestorer, logger, taskRepository)

	prepared, err := service.PrepareClaimedTaskContext(context.Background(), claimedTask)
	if err != nil {
		t.Fatalf("PrepareClaimedTaskContext() error = %v", err)
	}
	if !prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=true")
	}
	if prepared.AccountWithProxy.Account.ProxyID != uuid.Nil {
		t.Fatalf("expected proxyless account in prepared context, got proxy_id=%s", prepared.AccountWithProxy.Account.ProxyID.String())
	}

	gotTask, err := taskRepository.GetByID(context.Background(), queuedTask.ID)
	if err != nil {
		t.Fatalf("taskRepository.GetByID() error = %v", err)
	}
	if gotTask.Status != domain.TaskStatusRunning {
		t.Fatalf("expected task status %s after successful prepare, got %s", domain.TaskStatusRunning, gotTask.Status)
	}

	accountWithProxy, err := accountRepository.GetAccountWithProxy(context.Background(), accountID)
	if err != nil {
		t.Fatalf("accountRepository.GetAccountWithProxy() error = %v", err)
	}
	if accountWithProxy.Account.ActiveExecutionContextID != claimedTask.ClaimedBy {
		t.Fatalf(
			"expected account execution context %s, got %s",
			claimedTask.ClaimedBy,
			accountWithProxy.Account.ActiveExecutionContextID,
		)
	}
	if accountWithProxy.Account.ProxyID != uuid.Nil {
		t.Fatalf("expected proxyless account to remain proxyless, got proxy_id=%s", accountWithProxy.Account.ProxyID.String())
	}
}

func TestPrepareClaimedTaskContextIntegrationMissingPayloadReturnsBootstrapDecision(t *testing.T) {
	pool := mustOpenWorkerTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	accountRepository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	sessionRepository := postgresrepo.NewSessionRepository(pool)
	taskRepository := postgresrepo.NewTaskRepository(pool)

	accountID, err := createWorkerTestAccount(context.Background(), accountRepository, "exec-prepare-missing-01")
	if err != nil {
		t.Fatalf("createWorkerTestAccount() error = %v", err)
	}

	store := newInMemoryIntegrationSessionStore()
	sessionObjectKey := "accounts/" + accountID.String() + "/sessions/2.json"
	if _, err := sessionRepository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountID,
		Revision:  2,
		Status:    domain.SessionStatusValid,
		ObjectKey: sessionObjectKey,
	}); err != nil {
		t.Fatalf("sessionRepository.Upsert() error = %v", err)
	}

	queuedTask, claimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-missing",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask() error = %v", err)
	}

	accountGuard := NewAccountGuard(accountRepository, domain.DefaultRuntimeGuardrails(), logger)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, store, logger)
	service := NewExecutionService(accountGuard, sessionRestorer, logger, taskRepository).
		WithSessionBootstrapPolicy(SessionBootstrapPolicy{
			BootstrapLoginEnabled:         true,
			AllowMissingPayloadOnFirstRun: true,
		})

	prepared, err := service.PrepareClaimedTaskContext(context.Background(), claimedTask)
	if err != nil {
		t.Fatalf("PrepareClaimedTaskContext() error = %v", err)
	}
	if !prepared.BootstrapRequired {
		t.Fatal("expected bootstrap_required=true for missing payload")
	}
	if prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=false for bootstrap decision")
	}
	if prepared.BootstrapReason != domain.ErrorCodeAuthBootstrapRequired {
		t.Fatalf("expected bootstrap reason %s, got %s", domain.ErrorCodeAuthBootstrapRequired, prepared.BootstrapReason)
	}
	if prepared.BootstrapSource != domain.ErrorCodeSessionPayloadMissing {
		t.Fatalf("expected bootstrap source %s, got %s", domain.ErrorCodeSessionPayloadMissing, prepared.BootstrapSource)
	}

	gotTask, err := taskRepository.GetByID(context.Background(), queuedTask.ID)
	if err != nil {
		t.Fatalf("taskRepository.GetByID() error = %v", err)
	}
	if gotTask.Status != domain.TaskStatusRunning {
		t.Fatalf("expected task status %s, got %s", domain.TaskStatusRunning, gotTask.Status)
	}

	accountWithProxy, err := accountRepository.GetAccountWithProxy(context.Background(), accountID)
	if err != nil {
		t.Fatalf("accountRepository.GetAccountWithProxy() error = %v", err)
	}
	if accountWithProxy.Account.ActiveExecutionContextID != claimedTask.ClaimedBy {
		t.Fatalf(
			"expected account execution context %s, got %s",
			claimedTask.ClaimedBy,
			accountWithProxy.Account.ActiveExecutionContextID,
		)
	}

	metadata, err := sessionRepository.GetByAccountID(context.Background(), accountID)
	if err != nil {
		t.Fatalf("sessionRepository.GetByAccountID() error = %v", err)
	}
	if metadata.Status != domain.SessionStatusUnavailable {
		t.Fatalf("expected session status %s, got %s", domain.SessionStatusUnavailable, metadata.Status)
	}
}

func TestPrepareClaimedTaskContextIntegrationMissingMetadataReturnsBootstrapDecision(t *testing.T) {
	pool := mustOpenWorkerTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	accountRepository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	sessionRepository := postgresrepo.NewSessionRepository(pool)
	taskRepository := postgresrepo.NewTaskRepository(pool)

	accountID, err := createWorkerTestAccount(context.Background(), accountRepository, "exec-prepare-missing-metadata-01")
	if err != nil {
		t.Fatalf("createWorkerTestAccount() error = %v", err)
	}

	store := newInMemoryIntegrationSessionStore()
	queuedTask, claimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-missing-metadata",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask() error = %v", err)
	}

	accountGuard := NewAccountGuard(accountRepository, domain.DefaultRuntimeGuardrails(), logger)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, store, logger)
	service := NewExecutionService(accountGuard, sessionRestorer, logger, taskRepository).
		WithSessionBootstrapPolicy(SessionBootstrapPolicy{
			BootstrapLoginEnabled: true,
		})

	prepared, err := service.PrepareClaimedTaskContext(context.Background(), claimedTask)
	if err != nil {
		t.Fatalf("PrepareClaimedTaskContext() error = %v", err)
	}
	if !prepared.BootstrapRequired {
		t.Fatal("expected bootstrap_required=true for missing metadata")
	}
	if prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=false for bootstrap decision")
	}
	if prepared.BootstrapReason != domain.ErrorCodeAuthBootstrapRequired {
		t.Fatalf("expected bootstrap reason %s, got %s", domain.ErrorCodeAuthBootstrapRequired, prepared.BootstrapReason)
	}
	if prepared.BootstrapSource != domain.ErrorCodeSessionMetadataNotFound {
		t.Fatalf("expected bootstrap source %s, got %s", domain.ErrorCodeSessionMetadataNotFound, prepared.BootstrapSource)
	}

	gotTask, err := taskRepository.GetByID(context.Background(), queuedTask.ID)
	if err != nil {
		t.Fatalf("taskRepository.GetByID() error = %v", err)
	}
	if gotTask.Status != domain.TaskStatusRunning {
		t.Fatalf("expected task status %s, got %s", domain.TaskStatusRunning, gotTask.Status)
	}
	if gotTask.ErrorCode != "" {
		t.Fatalf("expected empty task error_code for bootstrap decision, got %s", gotTask.ErrorCode)
	}

	accountWithProxy, err := accountRepository.GetAccountWithProxy(context.Background(), accountID)
	if err != nil {
		t.Fatalf("accountRepository.GetAccountWithProxy() error = %v", err)
	}
	if accountWithProxy.Account.ActiveExecutionContextID != claimedTask.ClaimedBy {
		t.Fatalf(
			"expected account execution context %s, got %s",
			claimedTask.ClaimedBy,
			accountWithProxy.Account.ActiveExecutionContextID,
		)
	}

	_, err = sessionRepository.GetByAccountID(context.Background(), accountID)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionMetadataNotFound) {
		t.Fatalf("expected session metadata error %s, got %v", domain.ErrorCodeSessionMetadataNotFound, err)
	}
}

func TestResolveBootstrapForClaimedTaskIntegrationMissingMetadataPersistsValidSession(t *testing.T) {
	pool := mustOpenWorkerTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	accountRepository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	sessionRepository := postgresrepo.NewSessionRepository(pool)
	taskRepository := postgresrepo.NewTaskRepository(pool)

	accountID, err := createWorkerTestAccount(context.Background(), accountRepository, "exec-bootstrap-success-metadata-01")
	if err != nil {
		t.Fatalf("createWorkerTestAccount() error = %v", err)
	}

	store := newInMemoryIntegrationSessionStore()
	_, claimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-bootstrap-metadata-success",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask() error = %v", err)
	}

	accountGuard := NewAccountGuard(accountRepository, domain.DefaultRuntimeGuardrails(), logger)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, store, logger)
	service := NewExecutionService(accountGuard, sessionRestorer, logger, taskRepository).
		WithSessionBootstrapPolicy(SessionBootstrapPolicy{
			BootstrapLoginEnabled: true,
		}).
		WithBootstrapLoginRunner(&mockExecutionBootstrapRunner{
			runFn: func(ctx context.Context, input domain.BootstrapLoginInput) (domain.BootstrapLoginResult, error) {
				return domain.BootstrapLoginResult{
					Outcome:        domain.BootstrapLoginOutcomeSuccess,
					SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"bootstrap"}]}`),
					Diagnostics: domain.BootstrapLoginDiagnostics{
						Engine:     "mock",
						DurationMS: 1,
					},
				}, nil
			},
		})

	prepared, err := service.PrepareClaimedTaskContext(context.Background(), claimedTask)
	if err != nil {
		t.Fatalf("PrepareClaimedTaskContext() error = %v", err)
	}
	if !prepared.BootstrapRequired {
		t.Fatal("expected bootstrap_required=true before bootstrap resolution")
	}

	resolved, err := service.ResolveBootstrapForClaimedTask(context.Background(), claimedTask, prepared)
	if err != nil {
		t.Fatalf("ResolveBootstrapForClaimedTask() error = %v", err)
	}
	if resolved.BootstrapRequired {
		t.Fatal("expected bootstrap_required=false after bootstrap success")
	}
	if !resolved.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=true after bootstrap success")
	}
	if resolved.SessionMetadata.Status != domain.SessionStatusValid {
		t.Fatalf("expected session status %s, got %s", domain.SessionStatusValid, resolved.SessionMetadata.Status)
	}
	if resolved.SessionMetadata.Revision <= 0 {
		t.Fatalf("expected session revision > 0, got %d", resolved.SessionMetadata.Revision)
	}
}

func TestResolveBootstrapForClaimedTaskIntegrationMissingPayloadPersistsNewRevision(t *testing.T) {
	pool := mustOpenWorkerTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	accountRepository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	sessionRepository := postgresrepo.NewSessionRepository(pool)
	taskRepository := postgresrepo.NewTaskRepository(pool)

	accountID, err := createWorkerTestAccount(context.Background(), accountRepository, "exec-bootstrap-success-payload-01")
	if err != nil {
		t.Fatalf("createWorkerTestAccount() error = %v", err)
	}

	store := newInMemoryIntegrationSessionStore()
	// Metadata exists, payload is missing in object store -> bootstrap-required path.
	if _, err := sessionRepository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountID,
		Revision:  2,
		Status:    domain.SessionStatusValid,
		ObjectKey: "accounts/" + accountID.String() + "/sessions/2.json",
	}); err != nil {
		t.Fatalf("sessionRepository.Upsert() error = %v", err)
	}

	_, claimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-bootstrap-payload-success",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask() error = %v", err)
	}

	accountGuard := NewAccountGuard(accountRepository, domain.DefaultRuntimeGuardrails(), logger)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, store, logger)
	service := NewExecutionService(accountGuard, sessionRestorer, logger, taskRepository).
		WithSessionBootstrapPolicy(SessionBootstrapPolicy{
			BootstrapLoginEnabled:         true,
			AllowMissingPayloadOnFirstRun: true,
		}).
		WithBootstrapLoginRunner(&mockExecutionBootstrapRunner{
			runFn: func(ctx context.Context, input domain.BootstrapLoginInput) (domain.BootstrapLoginResult, error) {
				return domain.BootstrapLoginResult{
					Outcome:        domain.BootstrapLoginOutcomeSuccess,
					SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"bootstrap-fresh"}]}`),
					Diagnostics: domain.BootstrapLoginDiagnostics{
						Engine:     "mock",
						DurationMS: 1,
					},
				}, nil
			},
		})

	prepared, err := service.PrepareClaimedTaskContext(context.Background(), claimedTask)
	if err != nil {
		t.Fatalf("PrepareClaimedTaskContext() error = %v", err)
	}
	if !prepared.BootstrapRequired {
		t.Fatal("expected bootstrap_required=true before bootstrap resolution")
	}

	resolved, err := service.ResolveBootstrapForClaimedTask(context.Background(), claimedTask, prepared)
	if err != nil {
		t.Fatalf("ResolveBootstrapForClaimedTask() error = %v", err)
	}
	if resolved.SessionMetadata.Status != domain.SessionStatusValid {
		t.Fatalf("expected session status %s, got %s", domain.SessionStatusValid, resolved.SessionMetadata.Status)
	}
	if resolved.SessionMetadata.Revision != 3 {
		t.Fatalf("expected revision 3 after bootstrap save, got %d", resolved.SessionMetadata.Revision)
	}
}

func TestResolveBootstrapIntegrationReusesSavedSessionInNextTaskWithoutBootstrap(t *testing.T) {
	pool := mustOpenWorkerTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	accountRepository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	sessionRepository := postgresrepo.NewSessionRepository(pool)
	taskRepository := postgresrepo.NewTaskRepository(pool)

	accountID, err := createWorkerTestAccount(context.Background(), accountRepository, "exec-bootstrap-reuse-01")
	if err != nil {
		t.Fatalf("createWorkerTestAccount() error = %v", err)
	}

	store := newInMemoryIntegrationSessionStore()
	_, firstClaimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-bootstrap-reuse-first",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask(first) error = %v", err)
	}

	bootstrapCalls := 0
	accountGuard := NewAccountGuard(accountRepository, domain.DefaultRuntimeGuardrails(), logger)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, store, logger)
	service := NewExecutionService(accountGuard, sessionRestorer, logger, taskRepository).
		WithSessionBootstrapPolicy(SessionBootstrapPolicy{
			BootstrapLoginEnabled: true,
		}).
		WithBootstrapLoginRunner(&mockExecutionBootstrapRunner{
			runFn: func(ctx context.Context, input domain.BootstrapLoginInput) (domain.BootstrapLoginResult, error) {
				bootstrapCalls++
				return domain.BootstrapLoginResult{
					Outcome:        domain.BootstrapLoginOutcomeSuccess,
					SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"bootstrap-reuse"}]}`),
					Diagnostics: domain.BootstrapLoginDiagnostics{
						Engine:     "mock",
						DurationMS: 1,
					},
				}, nil
			},
		})

	preparedFirst, err := service.PrepareClaimedTaskContext(context.Background(), firstClaimedTask)
	if err != nil {
		t.Fatalf("PrepareClaimedTaskContext(first) error = %v", err)
	}
	if !preparedFirst.BootstrapRequired {
		t.Fatal("expected first task to require bootstrap")
	}
	if _, err := service.ResolveBootstrapForClaimedTask(context.Background(), firstClaimedTask, preparedFirst); err != nil {
		t.Fatalf("ResolveBootstrapForClaimedTask(first) error = %v", err)
	}
	if bootstrapCalls != 1 {
		t.Fatalf("expected exactly 1 bootstrap call after first task, got %d", bootstrapCalls)
	}

	_, secondClaimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-bootstrap-reuse-second",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask(second) error = %v", err)
	}

	preparedSecond, err := service.PrepareClaimedTaskContext(context.Background(), secondClaimedTask)
	if err != nil {
		t.Fatalf("PrepareClaimedTaskContext(second) error = %v", err)
	}
	if preparedSecond.BootstrapRequired {
		t.Fatal("expected second task to reuse persisted session without bootstrap")
	}
	if !preparedSecond.ReadyForFollowFlow {
		t.Fatal("expected second task to be ready for follow flow")
	}
	if preparedSecond.SessionMetadata.Revision != 1 {
		t.Fatalf("expected reused session revision 1, got %d", preparedSecond.SessionMetadata.Revision)
	}
	if len(preparedSecond.SessionPayload) == 0 {
		t.Fatal("expected reused session payload to be restored")
	}
	if bootstrapCalls != 1 {
		t.Fatalf("expected no additional bootstrap call during second prepare, got %d", bootstrapCalls)
	}
}

func TestPrepareClaimedTaskContextIntegrationCorruptedPayloadCompletesFailAndReleasesAccount(t *testing.T) {
	pool := mustOpenWorkerTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	accountRepository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	sessionRepository := postgresrepo.NewSessionRepository(pool)
	taskRepository := postgresrepo.NewTaskRepository(pool)

	accountID, err := createWorkerTestAccount(context.Background(), accountRepository, "exec-prepare-corrupted-01")
	if err != nil {
		t.Fatalf("createWorkerTestAccount() error = %v", err)
	}

	store := newInMemoryIntegrationSessionStore()
	sessionObjectKey := "accounts/" + accountID.String() + "/sessions/20.json"
	store.SetLoadError(
		sessionObjectKey,
		domain.NewDomainError(domain.ErrorCodeSessionPayloadCorrupted, "payload corrupted"),
	)
	if _, err := sessionRepository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountID,
		Revision:  20,
		Status:    domain.SessionStatusValid,
		ObjectKey: sessionObjectKey,
	}); err != nil {
		t.Fatalf("sessionRepository.Upsert() error = %v", err)
	}

	queuedTask, claimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-corrupted",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask() error = %v", err)
	}

	accountGuard := NewAccountGuard(accountRepository, domain.DefaultRuntimeGuardrails(), logger)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, store, logger)
	service := NewExecutionService(accountGuard, sessionRestorer, logger, taskRepository)

	prepared, err := service.PrepareClaimedTaskContext(context.Background(), claimedTask)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadCorrupted) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeSessionPayloadCorrupted, err)
	}
	if prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=false on failed prepare")
	}

	gotTask, err := taskRepository.GetByID(context.Background(), queuedTask.ID)
	if err != nil {
		t.Fatalf("taskRepository.GetByID() error = %v", err)
	}
	if gotTask.Status != domain.TaskStatusFail {
		t.Fatalf("expected task status %s, got %s", domain.TaskStatusFail, gotTask.Status)
	}
	if gotTask.ErrorCode != domain.ErrorCodeSessionPayloadCorrupted {
		t.Fatalf("expected task error_code %s, got %s", domain.ErrorCodeSessionPayloadCorrupted, gotTask.ErrorCode)
	}
	if strings.TrimSpace(gotTask.ResultReason) == "" {
		t.Fatal("expected non-empty result_reason on fail completion")
	}

	accountWithProxy, err := accountRepository.GetAccountWithProxy(context.Background(), accountID)
	if err != nil {
		t.Fatalf("accountRepository.GetAccountWithProxy() error = %v", err)
	}
	if accountWithProxy.Account.ActiveExecutionContextID != "" {
		t.Fatalf("expected released account context, got %s", accountWithProxy.Account.ActiveExecutionContextID)
	}

	metadata, err := sessionRepository.GetByAccountID(context.Background(), accountID)
	if err != nil {
		t.Fatalf("sessionRepository.GetByAccountID() error = %v", err)
	}
	if metadata.Status != domain.SessionStatusInvalid {
		t.Fatalf("expected session status %s, got %s", domain.SessionStatusInvalid, metadata.Status)
	}
}

func TestPrepareClaimedTaskContextIntegrationOwnershipMismatchCompletesFailAndReleasesAccount(t *testing.T) {
	pool := mustOpenWorkerTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	accountRepository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	sessionRepository := postgresrepo.NewSessionRepository(pool)
	taskRepository := postgresrepo.NewTaskRepository(pool)

	accountID, err := createWorkerTestAccount(context.Background(), accountRepository, "exec-prepare-ownership-01")
	if err != nil {
		t.Fatalf("createWorkerTestAccount() error = %v", err)
	}

	store := newInMemoryIntegrationSessionStore()
	sessionObjectKey := "accounts/" + accountID.String() + "/sessions/21.json"
	store.SetLoadError(
		sessionObjectKey,
		domain.NewDomainError(domain.ErrorCodeSessionOwnershipMismatch, "session ownership mismatch"),
	)
	if _, err := sessionRepository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountID,
		Revision:  21,
		Status:    domain.SessionStatusValid,
		ObjectKey: sessionObjectKey,
	}); err != nil {
		t.Fatalf("sessionRepository.Upsert() error = %v", err)
	}

	queuedTask, claimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-ownership",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask() error = %v", err)
	}

	accountGuard := NewAccountGuard(accountRepository, domain.DefaultRuntimeGuardrails(), logger)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, store, logger)
	service := NewExecutionService(accountGuard, sessionRestorer, logger, taskRepository)

	prepared, err := service.PrepareClaimedTaskContext(context.Background(), claimedTask)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionOwnershipMismatch) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeSessionOwnershipMismatch, err)
	}
	if prepared.ReadyForFollowFlow {
		t.Fatal("expected ready_for_follow_flow=false on failed prepare")
	}

	gotTask, err := taskRepository.GetByID(context.Background(), queuedTask.ID)
	if err != nil {
		t.Fatalf("taskRepository.GetByID() error = %v", err)
	}
	if gotTask.Status != domain.TaskStatusFail {
		t.Fatalf("expected task status %s, got %s", domain.TaskStatusFail, gotTask.Status)
	}
	if gotTask.ErrorCode != domain.ErrorCodeSessionOwnershipMismatch {
		t.Fatalf("expected task error_code %s, got %s", domain.ErrorCodeSessionOwnershipMismatch, gotTask.ErrorCode)
	}
	if strings.TrimSpace(gotTask.ResultReason) == "" {
		t.Fatal("expected non-empty result_reason on fail completion")
	}

	accountWithProxy, err := accountRepository.GetAccountWithProxy(context.Background(), accountID)
	if err != nil {
		t.Fatalf("accountRepository.GetAccountWithProxy() error = %v", err)
	}
	if accountWithProxy.Account.ActiveExecutionContextID != "" {
		t.Fatalf("expected released account context, got %s", accountWithProxy.Account.ActiveExecutionContextID)
	}

	metadata, err := sessionRepository.GetByAccountID(context.Background(), accountID)
	if err != nil {
		t.Fatalf("sessionRepository.GetByAccountID() error = %v", err)
	}
	if metadata.Status != domain.SessionStatusInvalid {
		t.Fatalf("expected session status %s, got %s", domain.SessionStatusInvalid, metadata.Status)
	}
}

func TestPrepareClaimedTaskContextIntegrationKeepsDomainStateWhenAuditFails(t *testing.T) {
	pool := mustOpenWorkerTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	accountRepository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	sessionRepository := postgresrepo.NewSessionRepository(pool)
	taskRepository := postgresrepo.NewTaskRepository(
		pool,
		audit.NewLog(&workerFailingAuditStore{appendErr: errors.New("audit down")}, nil),
	)

	accountID, err := createWorkerTestAccount(context.Background(), accountRepository, "exec-prepare-audit-fail-01")
	if err != nil {
		t.Fatalf("createWorkerTestAccount() error = %v", err)
	}

	store := newInMemoryIntegrationSessionStore()
	sessionObjectKey := "accounts/" + accountID.String() + "/sessions/3.json"
	if _, err := sessionRepository.Upsert(context.Background(), domain.SessionMetadata{
		AccountID: accountID,
		Revision:  3,
		Status:    domain.SessionStatusValid,
		ObjectKey: sessionObjectKey,
	}); err != nil {
		t.Fatalf("sessionRepository.Upsert() error = %v", err)
	}

	queuedTask, claimedTask, err := enqueueAndClaimTask(
		context.Background(),
		taskRepository,
		accountID,
		"integration-worker-audit-fail",
	)
	if err != nil {
		t.Fatalf("enqueueAndClaimTask() error = %v", err)
	}

	accountGuard := NewAccountGuard(accountRepository, domain.DefaultRuntimeGuardrails(), logger)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, store, logger)
	service := NewExecutionService(accountGuard, sessionRestorer, logger, taskRepository)

	_, err = service.PrepareClaimedTaskContext(context.Background(), claimedTask)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeSessionPayloadMissing, err)
	}

	gotTask, err := taskRepository.GetByID(context.Background(), queuedTask.ID)
	if err != nil {
		t.Fatalf("taskRepository.GetByID() error = %v", err)
	}
	if gotTask.Status != domain.TaskStatusRetry {
		t.Fatalf("expected retry status to be persisted even with audit failure, got %s", gotTask.Status)
	}
	if gotTask.ErrorCode != domain.ErrorCodeSessionPayloadMissing {
		t.Fatalf("expected error_code %s, got %s", domain.ErrorCodeSessionPayloadMissing, gotTask.ErrorCode)
	}
}

type inMemoryIntegrationSessionStore struct {
	mu         sync.RWMutex
	objects    map[string][]byte
	loadErrors map[string]error
}

func newInMemoryIntegrationSessionStore() *inMemoryIntegrationSessionStore {
	return &inMemoryIntegrationSessionStore{
		objects:    map[string][]byte{},
		loadErrors: map[string]error{},
	}
}

func (s *inMemoryIntegrationSessionStore) Save(
	ctx context.Context,
	accountID uuid.UUID,
	revision int64,
	payload []byte,
) (string, error) {
	objectKey := fmtSessionObjectKey(accountID, revision)
	if err := s.SaveObject(objectKey, payload); err != nil {
		return "", err
	}
	return objectKey, nil
}

func (s *inMemoryIntegrationSessionStore) SaveObject(objectKey string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	copied := make([]byte, len(payload))
	copy(copied, payload)
	s.objects[objectKey] = copied
	return nil
}

func (s *inMemoryIntegrationSessionStore) SetLoadError(objectKey string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadErrors[objectKey] = err
}

func (s *inMemoryIntegrationSessionStore) Load(
	ctx context.Context,
	accountID uuid.UUID,
	objectKey string,
) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err, ok := s.loadErrors[objectKey]; ok {
		return nil, err
	}

	payload, ok := s.objects[objectKey]
	if !ok {
		return nil, domain.NewDomainError(
			domain.ErrorCodeSessionPayloadMissing,
			"missing payload",
		)
	}

	copied := make([]byte, len(payload))
	copy(copied, payload)
	return copied, nil
}

func (s *inMemoryIntegrationSessionStore) Delete(ctx context.Context, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.objects, objectKey)
	return nil
}

type workerFailingAuditStore struct {
	appendErr error
}

func (s *workerFailingAuditStore) Append(ctx context.Context, record audit.Record) (audit.Record, error) {
	return audit.Record{}, s.appendErr
}

func (s *workerFailingAuditStore) ListRecent(ctx context.Context, limit int) ([]audit.Record, error) {
	return []audit.Record{}, nil
}

func enqueueAndClaimTask(
	ctx context.Context,
	taskRepository *postgresrepo.TaskPostgresRepository,
	accountID uuid.UUID,
	workerID string,
) (domain.Task, domain.Task, error) {
	taskID := uuid.New()
	queuedTask, err := taskRepository.Enqueue(ctx, domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "integration-target-profile",
		Status:        domain.TaskStatusQueued,
	})
	if err != nil {
		return domain.Task{}, domain.Task{}, err
	}

	claimedTask, claimed, err := taskRepository.ClaimNextQueued(ctx, workerID)
	if err != nil {
		return domain.Task{}, domain.Task{}, err
	}
	if !claimed {
		return domain.Task{}, domain.Task{}, errors.New("expected task to be claimed")
	}

	return queuedTask, claimedTask, nil
}

func createWorkerTestAccount(
	ctx context.Context,
	accountRepository *postgresrepo.AccountPostgresRepository,
	username string,
) (uuid.UUID, error) {
	proxy := domain.Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9060,
		IsActive: true,
	}
	if err := accountRepository.CreateProxy(ctx, proxy); err != nil {
		return uuid.Nil, err
	}

	accountID := uuid.New()
	if err := accountRepository.CreateAccount(ctx, domain.Account{
		ID:               accountID,
		Username:         username,
		ProxyID:          proxy.ID,
		CredentialSource: domain.CredentialSourceEnv,
		CredentialRef:    "env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD",
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}); err != nil {
		return uuid.Nil, err
	}

	return accountID, nil
}

func createWorkerTestAccountWithoutProxy(
	ctx context.Context,
	accountRepository *postgresrepo.AccountPostgresRepository,
	username string,
) (uuid.UUID, error) {
	accountID := uuid.New()
	if err := accountRepository.CreateAccount(ctx, domain.Account{
		ID:               accountID,
		Username:         username,
		CredentialSource: domain.CredentialSourceEnv,
		CredentialRef:    "env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD",
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}); err != nil {
		return uuid.Nil, err
	}

	return accountID, nil
}

func mustOpenWorkerTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("FOLLOWER_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("skipping integration test, FOLLOWER_TEST_POSTGRES_URL is not set")
	}

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Skipf("skipping integration test, invalid FOLLOWER_TEST_POSTGRES_URL: %v", err)
	}
	dbName := strings.ToLower(poolConfig.ConnConfig.Database)
	if dbName == "" || (!strings.Contains(dbName, "test") && !strings.Contains(dbName, "automation")) {
		t.Skipf(
			"skipping integration test, database %q is not marked as test-safe (expected name containing test/automation)",
			poolConfig.ConnConfig.Database,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Skipf("skipping integration test, cannot create postgres pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skipping integration test, postgres unavailable: %v", err)
	}

	prepareWorkerTestDatabase(t, pool)
	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}

func prepareWorkerTestDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx, `
		DROP TABLE IF EXISTS tasks;
		DROP TABLE IF EXISTS account_sessions;
		DROP TABLE IF EXISTS accounts;
		DROP TABLE IF EXISTS proxies;

		CREATE TABLE IF NOT EXISTS proxies (
			id UUID PRIMARY KEY,
			host TEXT NOT NULL,
			port INTEGER NOT NULL CHECK (port > 0),
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS accounts (
			id UUID PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			proxy_id UUID REFERENCES proxies(id) ON DELETE RESTRICT,
			operational_state TEXT NOT NULL,
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			is_ready BOOLEAN NOT NULL DEFAULT TRUE,
			is_quarantined BOOLEAN NOT NULL DEFAULT FALSE,
			is_restricted BOOLEAN NOT NULL DEFAULT FALSE,
			limit_reached BOOLEAN NOT NULL DEFAULT FALSE,
			active_execution_context_id TEXT NULL,
			credential_source TEXT NOT NULL DEFAULT 'manual',
			credential_ref TEXT NOT NULL DEFAULT 'manual://legacy',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT chk_accounts_operational_state CHECK (
				operational_state IN ('active','busy','invalid_session','quarantined','restricted')
			),
			CONSTRAINT chk_accounts_credential_source CHECK (
				credential_source IN ('env','vault','file','manual')
			),
			CONSTRAINT chk_accounts_credential_ref_nonempty CHECK (
				NULLIF(BTRIM(credential_ref), '') IS NOT NULL
			)
		);

		CREATE UNIQUE INDEX IF NOT EXISTS ux_accounts_active_execution_context
			ON accounts (active_execution_context_id)
			WHERE active_execution_context_id IS NOT NULL;

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

		CREATE TABLE IF NOT EXISTS tasks (
			id UUID PRIMARY KEY,
			account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
			target_profile TEXT NOT NULL,
			status TEXT NOT NULL,
			attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
			claimed_by TEXT NULL,
			claimed_at TIMESTAMPTZ NULL,
			started_at TIMESTAMPTZ NULL,
			finished_at TIMESTAMPTZ NULL,
			error_code TEXT NULL,
			result_reason TEXT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT chk_tasks_status CHECK (
				status IN ('queued', 'running', 'success', 'retry', 'fail')
			),
			CONSTRAINT chk_tasks_target_profile_nonempty CHECK (
				NULLIF(BTRIM(target_profile), '') IS NOT NULL
			),
			CONSTRAINT chk_tasks_reason_for_terminal_failure CHECK (
				status IN ('queued', 'running', 'success')
				OR COALESCE(error_code, '') <> ''
				OR COALESCE(result_reason, '') <> ''
			)
		);

		CREATE INDEX IF NOT EXISTS idx_tasks_status_claimed_at
			ON tasks (status, claimed_at);
	`)
	if err != nil {
		t.Fatalf("prepare worker integration schema: %v", err)
	}
}

func fmtSessionObjectKey(accountID uuid.UUID, revision int64) string {
	return "accounts/" + accountID.String() + "/sessions/" + fmt.Sprintf("%d", revision) + ".json"
}

func proxyBindingDisabledRuntimeGuardrails() domain.RuntimeGuardrails {
	guardrails := domain.DefaultRuntimeGuardrails()
	guardrails.RequireProxyBinding = false
	return guardrails
}
