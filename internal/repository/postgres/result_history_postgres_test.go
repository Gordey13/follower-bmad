package postgres_test

import (
	"context"
	"testing"
	"time"

	"follower/internal/domain"
	postgresrepo "follower/internal/repository/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResultRepositoryListHistoryFiltersAndSorts(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareFollowResultsSchema(t, pool)

	taskRepository := postgresrepo.NewTaskRepository(pool)
	resultRepository := postgresrepo.NewResultRepository(pool)

	accountOne := createTestAccount(t, pool, "result-history-account-01")
	accountTwo := createTestAccount(t, pool, "result-history-account-02")

	taskA := seedHistoryResult(
		t,
		pool,
		taskRepository,
		resultRepository,
		accountOne,
		"target-alpha",
		domain.TaskStatusSuccess,
		domain.FollowFlowOutcomeCompleted,
		true,
		domain.FollowVerificationSignalFollowConfirmed,
		"",
		time.Now().UTC().Add(-3*time.Minute),
	)
	taskB := seedHistoryResult(
		t,
		pool,
		taskRepository,
		resultRepository,
		accountOne,
		"target-beta",
		domain.TaskStatusFail,
		domain.FollowFlowOutcomeActionUnavailable,
		false,
		domain.FollowVerificationSignalActionUnavailable,
		domain.ErrorCodeFollowActionUnavailable,
		time.Now().UTC().Add(-2*time.Minute),
	)
	_ = seedHistoryResult(
		t,
		pool,
		taskRepository,
		resultRepository,
		accountTwo,
		"target-alpha",
		domain.TaskStatusRetry,
		domain.FollowFlowOutcomeNavigationFailed,
		false,
		domain.FollowVerificationSignalNavigationFailed,
		domain.ErrorCodeFollowNavigationFailed,
		time.Now().UTC().Add(-1*time.Minute),
	)

	history, err := resultRepository.ListHistory(context.Background(), domain.FollowResultsHistoryQuery{
		AccountID: accountOne,
		Limit:     10,
		Offset:    0,
	})
	if err != nil {
		t.Fatalf("ListHistory() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 rows for account one, got %d", len(history))
	}
	if history[0].TaskID != taskB {
		t.Fatalf("expected newest task %s first, got %s", taskB.String(), history[0].TaskID.String())
	}
	if history[0].TaskStatus != domain.TaskStatusFail {
		t.Fatalf("expected task status %s, got %s", domain.TaskStatusFail, history[0].TaskStatus)
	}
	if history[1].TaskID != taskA {
		t.Fatalf("expected oldest task %s second, got %s", taskA.String(), history[1].TaskID.String())
	}
	if history[1].TaskStatus != domain.TaskStatusSuccess {
		t.Fatalf("expected task status %s, got %s", domain.TaskStatusSuccess, history[1].TaskStatus)
	}
}

func TestResultRepositoryListHistorySupportsPaginationAndTargetFilter(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareFollowResultsSchema(t, pool)

	taskRepository := postgresrepo.NewTaskRepository(pool)
	resultRepository := postgresrepo.NewResultRepository(pool)

	accountID := createTestAccount(t, pool, "result-history-account-03")

	firstTask := seedHistoryResult(
		t,
		pool,
		taskRepository,
		resultRepository,
		accountID,
		"target-1",
		domain.TaskStatusSuccess,
		domain.FollowFlowOutcomeCompleted,
		true,
		domain.FollowVerificationSignalFollowConfirmed,
		"",
		time.Now().UTC().Add(-3*time.Minute),
	)
	secondTask := seedHistoryResult(
		t,
		pool,
		taskRepository,
		resultRepository,
		accountID,
		"target-2",
		domain.TaskStatusSuccess,
		domain.FollowFlowOutcomeAlreadyDone,
		true,
		domain.FollowVerificationSignalAlreadyDone,
		"",
		time.Now().UTC().Add(-2*time.Minute),
	)

	paged, err := resultRepository.ListHistory(context.Background(), domain.FollowResultsHistoryQuery{
		AccountID: accountID,
		Limit:     1,
		Offset:    1,
	})
	if err != nil {
		t.Fatalf("ListHistory() pagination error = %v", err)
	}
	if len(paged) != 1 {
		t.Fatalf("expected one paged row, got %d", len(paged))
	}
	if paged[0].TaskID != firstTask {
		t.Fatalf("expected paged task %s, got %s", firstTask.String(), paged[0].TaskID.String())
	}

	filtered, err := resultRepository.ListHistory(context.Background(), domain.FollowResultsHistoryQuery{
		AccountID:     accountID,
		TargetProfile: "target-2",
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("ListHistory() target filter error = %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected one filtered row, got %d", len(filtered))
	}
	if filtered[0].TaskID != secondTask {
		t.Fatalf("expected task %s, got %s", secondTask.String(), filtered[0].TaskID.String())
	}
	if filtered[0].FollowOutcome != domain.FollowFlowOutcomeAlreadyDone {
		t.Fatalf("expected outcome %s, got %s", domain.FollowFlowOutcomeAlreadyDone, filtered[0].FollowOutcome)
	}
}

func seedHistoryResult(
	t *testing.T,
	pool *pgxpool.Pool,
	taskRepository *postgresrepo.TaskPostgresRepository,
	resultRepository *postgresrepo.ResultPostgresRepository,
	accountID uuid.UUID,
	targetProfile domain.TargetProfileDescriptor,
	finalStatus domain.TaskStatus,
	outcome domain.FollowFlowOutcome,
	verified bool,
	signal domain.FollowVerificationSignal,
	errorCode domain.ErrorCode,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	taskID := uuid.New()
	if _, err := taskRepository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: targetProfile,
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	workerID := "worker-history-" + uuid.New().String()
	claimed, ok, err := taskRepository.ClaimNextQueued(context.Background(), workerID)
	if err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}

	taskErrorCode := domain.ErrorCode("")
	taskResultReason := ""
	if finalStatus == domain.TaskStatusFail || finalStatus == domain.TaskStatusRetry {
		taskErrorCode = errorCode
		taskResultReason = "history seeded failure path"
	}
	if _, err := taskRepository.Complete(
		context.Background(),
		claimed.ID,
		workerID,
		finalStatus,
		taskErrorCode,
		taskResultReason,
	); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	result := domain.FollowResult{
		TaskID:              claimed.ID,
		AccountID:           claimed.AccountID,
		TargetProfile:       claimed.TargetProfile,
		Attempt:             claimed.Attempt,
		Outcome:             outcome,
		Verified:            verified,
		VerificationSignal:  signal,
		VerificationReason:  "history-seeded",
		ErrorCode:           errorCode,
		ScreenshotObjectKey: claimed.AccountID.String() + "/screenshot/2026-04-23-101112.png",
		ArtifactObjectKeys: []string{
			claimed.AccountID.String() + "/artifacts/2026-04-23-101113.json",
		},
	}
	if _, err := resultRepository.Upsert(context.Background(), result); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	if _, err := pool.Exec(
		context.Background(),
		`
			UPDATE follow_results
			SET created_at = $3, updated_at = $3
			WHERE task_id = $1 AND attempt = $2
		`,
		claimed.ID,
		claimed.Attempt,
		createdAt,
	); err != nil {
		t.Fatalf("update follow_results created_at: %v", err)
	}

	return claimed.ID
}
