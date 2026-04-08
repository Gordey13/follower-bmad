package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"follower/internal/domain"
	postgresrepo "follower/internal/repository/postgres"

	"github.com/google/uuid"
)

func TestTaskRepositoryClaimNextQueuedSetsRunningLifecycle(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-claim-lifecycle-01")
	enqueued, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            uuid.New(),
		AccountID:     accountID,
		TargetProfile: "target-profile-test",
		Status:        domain.TaskStatusQueued,
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	claimed, ok, err := repository.ClaimNextQueued(context.Background(), "worker-claim-01")
	if err != nil {
		t.Fatalf("ClaimNextQueued() error = %v", err)
	}
	if !ok {
		t.Fatal("expected queued task to be claimed")
	}
	if claimed.ID != enqueued.ID {
		t.Fatalf("expected claimed task id %s, got %s", enqueued.ID.String(), claimed.ID.String())
	}
	if claimed.Status != domain.TaskStatusRunning {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusRunning, claimed.Status)
	}
	if claimed.Attempt != 1 {
		t.Fatalf("expected attempt 1, got %d", claimed.Attempt)
	}
	if claimed.ClaimedBy != "worker-claim-01" {
		t.Fatalf("expected claimed_by worker-claim-01, got %s", claimed.ClaimedBy)
	}
	if claimed.ClaimedAt == nil || claimed.StartedAt == nil {
		t.Fatalf("expected claimed_at and started_at to be set, got claimed_at=%v started_at=%v", claimed.ClaimedAt, claimed.StartedAt)
	}
	if claimed.FinishedAt != nil {
		t.Fatalf("expected finished_at to be nil for running task, got %v", claimed.FinishedAt)
	}
}

func TestTaskRepositoryClaimNextQueuedReturnsNoTaskWithoutError(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	claimed, ok, err := repository.ClaimNextQueued(context.Background(), "worker-empty-queue-01")
	if err != nil {
		t.Fatalf("ClaimNextQueued() error = %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false on empty queue, got task %+v", claimed)
	}
}

func TestTaskRepositoryClaimNextQueuedIsConcurrentSafe(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-claim-concurrency-01")
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            uuid.New(),
		AccountID:     accountID,
		TargetProfile: "target-profile-test",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	type claimResult struct {
		task domain.Task
		ok   bool
		err  error
	}

	start := make(chan struct{})
	results := make(chan claimResult, 2)
	workers := []string{"worker-a", "worker-b"}

	var waitGroup sync.WaitGroup
	waitGroup.Add(len(workers))
	for _, workerID := range workers {
		workerID := workerID
		go func() {
			defer waitGroup.Done()
			<-start
			task, ok, err := repository.ClaimNextQueued(context.Background(), workerID)
			results <- claimResult{task: task, ok: ok, err: err}
		}()
	}

	close(start)
	waitGroup.Wait()
	close(results)

	successCount := 0
	emptyCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("ClaimNextQueued() unexpected error = %v", result.err)
		}
		if result.ok {
			successCount++
			continue
		}
		emptyCount++
	}

	if successCount != 1 {
		t.Fatalf("expected exactly one successful claim, got %d", successCount)
	}
	if emptyCount != 1 {
		t.Fatalf("expected exactly one empty-queue result, got %d", emptyCount)
	}
}

func TestTaskRepositoryCompleteTransitionsToTerminalStatuses(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	tests := []struct {
		name         string
		finalStatus  domain.TaskStatus
		errorCode    domain.ErrorCode
		resultReason string
	}{
		{
			name:        "success",
			finalStatus: domain.TaskStatusSuccess,
		},
		{
			name:         "retry",
			finalStatus:  domain.TaskStatusRetry,
			errorCode:    domain.ErrorCodeInternal,
			resultReason: "temporary dependency timeout",
		},
		{
			name:         "fail",
			finalStatus:  domain.TaskStatusFail,
			errorCode:    domain.ErrorCodeSessionPayloadCorrupted,
			resultReason: "session restore hard failure",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			accountID := createTestAccount(
				t,
				pool,
				"task-complete-"+testCase.name+"-"+uuid.New().String(),
			)
			taskID := uuid.New()

			if _, err := repository.Enqueue(context.Background(), domain.Task{
				ID:            taskID,
				AccountID:     accountID,
				TargetProfile: "target-profile-test",
				Status:        domain.TaskStatusQueued,
			}); err != nil {
				t.Fatalf("Enqueue() error = %v", err)
			}

			claimed, ok, err := repository.ClaimNextQueued(context.Background(), "worker-complete-01")
			if err != nil {
				t.Fatalf("ClaimNextQueued() error = %v", err)
			}
			if !ok {
				t.Fatal("expected claim to succeed")
			}
			if claimed.ID != taskID {
				t.Fatalf("expected claimed task id %s, got %s", taskID.String(), claimed.ID.String())
			}

			completed, err := repository.Complete(
				context.Background(),
				taskID,
				"worker-complete-01",
				testCase.finalStatus,
				testCase.errorCode,
				testCase.resultReason,
			)
			if err != nil {
				t.Fatalf("Complete() error = %v", err)
			}

			if completed.Status != testCase.finalStatus {
				t.Fatalf("expected final status %s, got %s", testCase.finalStatus, completed.Status)
			}
			if completed.FinishedAt == nil {
				t.Fatal("expected finished_at to be set on terminal transition")
			}
			if testCase.finalStatus == domain.TaskStatusSuccess {
				if completed.ErrorCode != "" || completed.ResultReason != "" {
					t.Fatalf("expected success completion to clear reason fields, got error_code=%s reason=%s", completed.ErrorCode, completed.ResultReason)
				}
			}
		})
	}
}

func TestTaskRepositoryCompleteRejectsClaimOwnerMismatch(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-owner-mismatch-01")
	taskID := uuid.New()

	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-test",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	if _, ok, err := repository.ClaimNextQueued(context.Background(), "worker-owner-1"); err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}

	_, err := repository.Complete(
		context.Background(),
		taskID,
		"worker-owner-2",
		domain.TaskStatusFail,
		domain.ErrorCodeInternal,
		"ownership mismatch check",
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeTaskClaimOwnerMismatch) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeTaskClaimOwnerMismatch, err)
	}
}

func TestTaskRepositoryCompleteRequiresReasonForRetryOrFail(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-reason-required-01")
	taskID := uuid.New()

	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-test",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	if _, ok, err := repository.ClaimNextQueued(context.Background(), "worker-reason-check"); err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}

	_, err := repository.Complete(
		context.Background(),
		taskID,
		"worker-reason-check",
		domain.TaskStatusRetry,
		"",
		"",
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeTaskCompletionReason) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeTaskCompletionReason, err)
	}
}

func TestTaskRepositoryCompleteSucceedsWhenAuditFails(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(
		pool,
		newFailingAuditLog(errors.New("audit store unavailable")),
	)

	accountID := createTestAccount(t, pool, "task-audit-fail-open-01")
	taskID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-test",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	if _, ok, err := repository.ClaimNextQueued(context.Background(), "worker-audit-fail-open"); err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}

	completed, err := repository.Complete(
		context.Background(),
		taskID,
		"worker-audit-fail-open",
		domain.TaskStatusSuccess,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("Complete() must succeed when audit fails, got error = %v", err)
	}
	if completed.Status != domain.TaskStatusSuccess {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusSuccess, completed.Status)
	}

	got, err := repository.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != domain.TaskStatusSuccess {
		t.Fatalf("expected persisted status %s, got %s", domain.TaskStatusSuccess, got.Status)
	}
	if got.FinishedAt == nil {
		t.Fatal("expected finished_at to be persisted")
	}
}

func TestTaskRepositoryClaimNextQueuedRejectsEmptyClaimedBy(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	_, _, err := repository.ClaimNextQueued(context.Background(), "")
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidTaskClaimedBy) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeInvalidTaskClaimedBy, err)
	}
}

func TestTaskRepositoryGetByIDReturnsNotFound(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	_, err := repository.GetByID(context.Background(), uuid.New())
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeTaskNotFound) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeTaskNotFound, err)
	}
}

func TestTaskRepositoryCompleteRejectsNonRunningTask(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-not-running-01")
	taskID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-test",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	_, err := repository.Complete(
		context.Background(),
		taskID,
		"worker-not-running",
		domain.TaskStatusSuccess,
		"",
		"",
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeTaskClaimOwnerMismatch) &&
		!domain.IsDomainErrorCode(err, domain.ErrorCodeTaskNotRunning) {
		t.Fatalf("expected %s or %s, got %v", domain.ErrorCodeTaskClaimOwnerMismatch, domain.ErrorCodeTaskNotRunning, err)
	}
}

func TestTaskRepositoryClaimAndCompletePreserveAttemptAcrossRetry(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-attempt-retry-01")
	taskID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-test",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	claimed, ok, err := repository.ClaimNextQueued(context.Background(), "worker-attempt-retry")
	if err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}
	if claimed.Attempt != 1 {
		t.Fatalf("expected attempt=1 after first claim, got %d", claimed.Attempt)
	}

	completed, err := repository.Complete(
		context.Background(),
		taskID,
		"worker-attempt-retry",
		domain.TaskStatusRetry,
		domain.ErrorCodeInternal,
		"transient failure",
	)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if completed.Attempt != 1 {
		t.Fatalf("expected attempt to remain 1 on completion, got %d", completed.Attempt)
	}
	if completed.FinishedAt == nil {
		t.Fatal("expected finished_at to be set on retry completion")
	}
}

func TestTaskRepositoryClaimNextQueuedOrdersByCreatedAt(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-claim-order-01")
	firstID := uuid.New()
	secondID := uuid.New()

	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            firstID,
		AccountID:     accountID,
		TargetProfile: "target-profile-test",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            secondID,
		AccountID:     accountID,
		TargetProfile: "target-profile-test",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue(second) error = %v", err)
	}

	firstClaim, ok, err := repository.ClaimNextQueued(context.Background(), "worker-order-01")
	if err != nil || !ok {
		t.Fatalf("first ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}
	if firstClaim.ID != firstID {
		t.Fatalf("expected first claimed id %s, got %s", firstID.String(), firstClaim.ID.String())
	}

	secondClaim, ok, err := repository.ClaimNextQueued(context.Background(), "worker-order-02")
	if err != nil || !ok {
		t.Fatalf("second ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}
	if secondClaim.ID != secondID {
		t.Fatalf("expected second claimed id %s, got %s", secondID.String(), secondClaim.ID.String())
	}
}

func TestTaskRepositoryTaskQueueSnapshotReturnsCountsByStatus(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-snapshot-counts-01")

	enqueueAndClaim := func(taskID uuid.UUID, workerID string) {
		t.Helper()
		if _, err := repository.Enqueue(context.Background(), domain.Task{
			ID:            taskID,
			AccountID:     accountID,
			TargetProfile: domain.TargetProfileDescriptor("snapshot-target-" + workerID),
			Status:        domain.TaskStatusQueued,
		}); err != nil {
			t.Fatalf("Enqueue(%s) error = %v", workerID, err)
		}
		if _, ok, err := repository.ClaimNextQueued(context.Background(), workerID); err != nil || !ok {
			t.Fatalf("ClaimNextQueued(%s) expected success, got ok=%v err=%v", workerID, ok, err)
		}
	}

	runningID := uuid.New()
	enqueueAndClaim(runningID, "worker-running")

	successID := uuid.New()
	enqueueAndClaim(successID, "worker-success")
	if _, err := repository.Complete(
		context.Background(),
		successID,
		"worker-success",
		domain.TaskStatusSuccess,
		"",
		"",
	); err != nil {
		t.Fatalf("Complete(success) error = %v", err)
	}

	retryID := uuid.New()
	enqueueAndClaim(retryID, "worker-retry")
	if _, err := repository.Complete(
		context.Background(),
		retryID,
		"worker-retry",
		domain.TaskStatusRetry,
		domain.ErrorCodeInternal,
		"transient refresh failure",
	); err != nil {
		t.Fatalf("Complete(retry) error = %v", err)
	}

	failID := uuid.New()
	enqueueAndClaim(failID, "worker-fail")
	if _, err := repository.Complete(
		context.Background(),
		failID,
		"worker-fail",
		domain.TaskStatusFail,
		domain.ErrorCodeFollowTargetUnreachable,
		"target profile unreachable",
	); err != nil {
		t.Fatalf("Complete(fail) error = %v", err)
	}

	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            uuid.New(),
		AccountID:     accountID,
		TargetProfile: domain.TargetProfileDescriptor("snapshot-target-queued"),
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue(queued) error = %v", err)
	}

	snapshot, err := repository.TaskQueueSnapshot(context.Background())
	if err != nil {
		t.Fatalf("TaskQueueSnapshot() error = %v", err)
	}

	expected := map[domain.TaskStatus]int64{
		domain.TaskStatusQueued:  1,
		domain.TaskStatusRunning: 1,
		domain.TaskStatusSuccess: 1,
		domain.TaskStatusRetry:   1,
		domain.TaskStatusFail:    1,
	}
	for status, want := range expected {
		if got := snapshot[status]; got != want {
			t.Fatalf("expected %s=%d, got %d", status, want, got)
		}
	}
}
