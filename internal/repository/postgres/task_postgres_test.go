package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"follower/internal/audit"
	"follower/internal/domain"
	"follower/internal/repository"
	postgresrepo "follower/internal/repository/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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

func TestTaskRepositoryGetByIDReturnsTask(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-getbyid-valid-01")
	enqueued, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            uuid.New(),
		AccountID:     accountID,
		TargetProfile: "target-profile-getbyid",
		Status:        domain.TaskStatusQueued,
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	got, err := repository.GetByID(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.ID != enqueued.ID {
		t.Fatalf("expected id %s, got %s", enqueued.ID.String(), got.ID.String())
	}
	if got.Status != domain.TaskStatusQueued {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusQueued, got.Status)
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

func TestTaskRepositoryListFailuresFiltersSortsAndPaginates(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)
	accountID := createTestAccount(t, pool, "task-list-failures-01")

	enqueueClaimComplete := func(
		target string,
		worker string,
		status domain.TaskStatus,
		errorCode domain.ErrorCode,
		reason string,
	) {
		t.Helper()
		taskID := uuid.New()
		if _, err := repository.Enqueue(context.Background(), domain.Task{
			ID:            taskID,
			AccountID:     accountID,
			TargetProfile: domain.TargetProfileDescriptor(target),
			Status:        domain.TaskStatusQueued,
		}); err != nil {
			t.Fatalf("Enqueue(%s) error = %v", target, err)
		}
		if _, ok, err := repository.ClaimNextQueued(context.Background(), worker); err != nil || !ok {
			t.Fatalf("ClaimNextQueued(%s) expected success, got ok=%v err=%v", worker, ok, err)
		}
		if _, err := repository.Complete(
			context.Background(),
			taskID,
			worker,
			status,
			errorCode,
			reason,
		); err != nil {
			t.Fatalf("Complete(%s) error = %v", target, err)
		}
	}

	enqueueClaimComplete(
		"https://oskelly.ru/profile/fail-1",
		"worker-fail-1",
		domain.TaskStatusFail,
		domain.ErrorCodeFollowTargetUnreachable,
		"target unreachable",
	)
	time.Sleep(10 * time.Millisecond)
	enqueueClaimComplete(
		"https://oskelly.ru/profile/retry-1",
		"worker-retry-1",
		domain.TaskStatusRetry,
		domain.ErrorCodeFollowNavigationFailed,
		"navigation timeout",
	)
	time.Sleep(10 * time.Millisecond)
	enqueueClaimComplete(
		"https://oskelly.ru/profile/success-1",
		"worker-success-1",
		domain.TaskStatusSuccess,
		"",
		"",
	)

	failures, err := repository.ListFailures(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("ListFailures() error = %v", err)
	}
	if len(failures) != 2 {
		t.Fatalf("expected 2 failure tasks, got %d", len(failures))
	}
	if failures[0].Status != domain.TaskStatusRetry {
		t.Fatalf("expected first status %s, got %s", domain.TaskStatusRetry, failures[0].Status)
	}
	if failures[1].Status != domain.TaskStatusFail {
		t.Fatalf("expected second status %s, got %s", domain.TaskStatusFail, failures[1].Status)
	}
	for _, task := range failures {
		if task.Status != domain.TaskStatusFail && task.Status != domain.TaskStatusRetry {
			t.Fatalf("unexpected non-triage status in failures list: %s", task.Status)
		}
	}

	page, err := repository.ListFailures(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("ListFailures() pagination error = %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("expected pagination result size 1, got %d", len(page))
	}
	if page[0].Status != domain.TaskStatusFail {
		t.Fatalf("expected paged status %s, got %s", domain.TaskStatusFail, page[0].Status)
	}
}

func TestTaskRepositoryListFailuresReturnsEmptyWhenNoTriageTasks(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewTaskRepository(pool)

	failures, err := repository.ListFailures(context.Background(), 20, 0)
	if err != nil {
		t.Fatalf("ListFailures() error = %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("expected empty failures list, got %d items", len(failures))
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

func TestTaskRepositoryEnqueueValidatedBatchCreatesQueuedTasks(t *testing.T) {
	pool := mustOpenTestPool(t)
	repo := postgresrepo.NewTaskRepository(pool)
	ctx := context.Background()

	accountID := createTestAccount(t, pool, "task-batch-create-01")
	rows := []repository.EnqueueValidatedRow{
		{
			Row:           2,
			AccountID:     accountID,
			TargetProfile: "https://oskelly.ru/profile/900001",
		},
		{
			Row:           3,
			AccountID:     accountID,
			TargetProfile: "https://oskelly.ru/profile/900002",
		},
	}

	result, err := repo.EnqueueValidatedBatch(ctx, rows)
	if err != nil {
		t.Fatalf("EnqueueValidatedBatch() error = %v", err)
	}

	if result.RowsCreated != 2 {
		t.Fatalf("expected rows_created=2, got %d", result.RowsCreated)
	}
	if result.RowsSkipped != 0 {
		t.Fatalf("expected rows_skipped=0, got %d", result.RowsSkipped)
	}
	if len(result.SkippedRows) != 0 {
		t.Fatalf("expected no skipped rows, got %+v", result.SkippedRows)
	}

	var queuedCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)::INT
		FROM tasks
		WHERE status = 'queued'
	`).Scan(&queuedCount); err != nil {
		t.Fatalf("count queued tasks: %v", err)
	}
	if queuedCount != 2 {
		t.Fatalf("expected 2 queued tasks, got %d", queuedCount)
	}
}

func TestTaskRepositoryEnqueueValidatedBatchSkipsDuplicateActiveOnRepeat(t *testing.T) {
	pool := mustOpenTestPool(t)
	repo := postgresrepo.NewTaskRepository(pool)
	ctx := context.Background()

	accountID := createTestAccount(t, pool, "task-batch-repeat-01")
	rows := []repository.EnqueueValidatedRow{
		{
			Row:           2,
			AccountID:     accountID,
			TargetProfile: "https://oskelly.ru/profile/901001",
		},
		{
			Row:           3,
			AccountID:     accountID,
			TargetProfile: "https://oskelly.ru/profile/901002",
		},
	}

	first, err := repo.EnqueueValidatedBatch(ctx, rows)
	if err != nil {
		t.Fatalf("first EnqueueValidatedBatch() error = %v", err)
	}
	if first.RowsCreated != 2 || first.RowsSkipped != 0 {
		t.Fatalf("unexpected first result: %+v", first)
	}

	second, err := repo.EnqueueValidatedBatch(ctx, rows)
	if err != nil {
		t.Fatalf("second EnqueueValidatedBatch() error = %v", err)
	}
	if second.RowsCreated != 0 {
		t.Fatalf("expected rows_created=0 on repeat import, got %d", second.RowsCreated)
	}
	if second.RowsSkipped != 2 {
		t.Fatalf("expected rows_skipped=2 on repeat import, got %d", second.RowsSkipped)
	}
	for _, skipped := range second.SkippedRows {
		if skipped.Code != "duplicate_active_task" {
			t.Fatalf("expected duplicate_active_task reason, got %q", skipped.Code)
		}
	}
}

func TestTaskRepositoryEnqueueValidatedBatchMixedCreateAndSkip(t *testing.T) {
	pool := mustOpenTestPool(t)
	repo := postgresrepo.NewTaskRepository(pool)
	ctx := context.Background()

	accountID := createTestAccount(t, pool, "task-batch-mixed-01")
	preseedRows := []repository.EnqueueValidatedRow{
		{
			Row:           2,
			AccountID:     accountID,
			TargetProfile: "https://oskelly.ru/profile/902001",
		},
	}
	if _, err := repo.EnqueueValidatedBatch(ctx, preseedRows); err != nil {
		t.Fatalf("preseed EnqueueValidatedBatch() error = %v", err)
	}

	mixedRows := []repository.EnqueueValidatedRow{
		{
			Row:           2,
			AccountID:     accountID,
			TargetProfile: "https://oskelly.ru/profile/902001",
		},
		{
			Row:           3,
			AccountID:     accountID,
			TargetProfile: "https://oskelly.ru/profile/902002",
		},
	}
	result, err := repo.EnqueueValidatedBatch(ctx, mixedRows)
	if err != nil {
		t.Fatalf("mixed EnqueueValidatedBatch() error = %v", err)
	}

	if result.RowsCreated != 1 {
		t.Fatalf("expected rows_created=1, got %d", result.RowsCreated)
	}
	if result.RowsSkipped != 1 {
		t.Fatalf("expected rows_skipped=1, got %d", result.RowsSkipped)
	}
	if len(result.SkippedRows) != 1 {
		t.Fatalf("expected one skipped row, got %+v", result.SkippedRows)
	}
	if result.SkippedRows[0].Code != "duplicate_active_task" {
		t.Fatalf("expected duplicate_active_task reason, got %q", result.SkippedRows[0].Code)
	}
}

func TestTaskRepositoryEnqueueValidatedBatchRollsBackOnWriteFailure(t *testing.T) {
	pool := mustOpenTestPool(t)
	repo := postgresrepo.NewTaskRepository(pool)
	ctx := context.Background()

	accountID := createTestAccount(t, pool, "task-batch-fail-01")
	rows := []repository.EnqueueValidatedRow{
		{
			Row:           2,
			AccountID:     accountID,
			TargetProfile: "https://oskelly.ru/profile/903001",
		},
		{
			Row:           3,
			AccountID:     accountID,
			TargetProfile: "   ",
		},
	}

	_, err := repo.EnqueueValidatedBatch(ctx, rows)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeTaskQueueWriteFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeTaskQueueWriteFailed, err)
	}

	var taskCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::INT FROM tasks`).Scan(&taskCount); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("expected rollback with zero inserted tasks, got %d", taskCount)
	}
}

func TestTaskRepositoryApplyAdminTransitionCancelCommitsStateAndAuditAtomically(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareAuditLogsSchema(t, pool)
	prepareTaskSchemaForCanceledLifecycle(t, pool)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-admin-cancel-atomic-01")
	taskID := uuid.New()
	enqueued, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-admin-cancel",
		Status:        domain.TaskStatusQueued,
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	ctx := audit.WithActor(context.Background(), audit.Actor{
		Type: audit.ActorTypeAdminOperator,
		ID:   "admin-cancel-01",
	})
	const cancelReason = "admin canceled before worker claim"
	updated, err := repository.CancelTask(ctx, taskID, cancelReason)
	if err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}

	if updated.Status != domain.TaskStatusCanceled {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusCanceled, updated.Status)
	}
	if updated.FinishedAt == nil {
		t.Fatal("expected finished_at to be set for canceled status")
	}
	if updated.ClaimedBy != "" || updated.ClaimedAt != nil || updated.StartedAt != nil {
		t.Fatalf("expected claim/start fields to be reset on cancel, got %+v", updated)
	}
	if updated.ErrorCode != "" {
		t.Fatalf("expected error_code to be empty after cancel, got %s", updated.ErrorCode)
	}
	if updated.ResultReason != cancelReason {
		t.Fatalf("expected result_reason %q, got %q", cancelReason, updated.ResultReason)
	}

	// Post-action read must return consistent latest state.
	got, err := repository.GetByID(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != domain.TaskStatusCanceled {
		t.Fatalf("expected persisted status %s, got %s", domain.TaskStatusCanceled, got.Status)
	}

	var auditCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)::INT
		FROM audit_logs
		WHERE target_type = 'task'
		  AND target_id = $1
		  AND action = 'task.canceled'
	`, taskID.String()).Scan(&auditCount); err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("expected exactly 1 task.canceled audit row, got %d", auditCount)
	}
}

func TestTaskRepositoryApplyAdminTransitionRollsBackWhenAuditWriteFails(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareTaskSchemaForCanceledLifecycle(t, pool)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-admin-cancel-rollback-01")
	taskID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-admin-rollback",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	// audit_logs table is absent here: tx must rollback state mutation.
	_, err := repository.CancelTask(context.Background(), taskID, "rollback check")
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeTaskQueueWriteFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeTaskQueueWriteFailed, err)
	}

	got, err := repository.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != domain.TaskStatusQueued {
		t.Fatalf("expected status to remain queued after rollback, got %s", got.Status)
	}
	if got.FinishedAt != nil {
		t.Fatalf("expected finished_at to remain nil after rollback, got %v", got.FinishedAt)
	}
}

func TestTaskRepositoryApplyAdminTransitionCancelVsClaimRaceHasSingleWinner(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareAuditLogsSchema(t, pool)
	prepareTaskSchemaForCanceledLifecycle(t, pool)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-admin-race-cancel-claim-01")
	taskID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-race-cancel-claim",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	start := make(chan struct{})
	type cancelResult struct {
		task domain.Task
		err  error
	}
	type claimResult struct {
		task domain.Task
		ok   bool
		err  error
	}

	cancelCh := make(chan cancelResult, 1)
	claimCh := make(chan claimResult, 1)

	go func() {
		<-start
		ctx := audit.WithActor(context.Background(), audit.Actor{
			Type: audit.ActorTypeAdminOperator,
			ID:   "admin-race-cancel-claim",
		})
		task, err := repository.CancelTask(ctx, taskID, "race-cancel-claim")
		cancelCh <- cancelResult{task: task, err: err}
	}()

	go func() {
		<-start
		task, ok, err := repository.ClaimNextQueued(context.Background(), "worker-race-cancel-claim")
		claimCh <- claimResult{task: task, ok: ok, err: err}
	}()

	close(start)
	cancelRes := <-cancelCh
	claimRes := <-claimCh

	if claimRes.err != nil {
		t.Fatalf("ClaimNextQueued() unexpected error = %v", claimRes.err)
	}
	cancelSucceeded := cancelRes.err == nil
	claimSucceeded := claimRes.ok

	if cancelSucceeded == claimSucceeded {
		t.Fatalf("expected exactly one winner, got cancel_success=%v claim_success=%v cancel_err=%v", cancelSucceeded, claimSucceeded, cancelRes.err)
	}
	if !cancelSucceeded && !domain.IsDomainErrorCode(cancelRes.err, domain.ErrorCodeCancelNotAllowed) {
		t.Fatalf("expected cancel loser to return %s, got %v", domain.ErrorCodeCancelNotAllowed, cancelRes.err)
	}

	got, err := repository.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != domain.TaskStatusCanceled && got.Status != domain.TaskStatusRunning {
		t.Fatalf("expected final status canceled or running, got %s", got.Status)
	}
}

func TestTaskRepositoryApplyAdminTransitionRetryVsCompleteRaceHasSingleWinner(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareAuditLogsSchema(t, pool)
	prepareTaskSchemaForCanceledLifecycle(t, pool)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-admin-race-retry-complete-01")
	taskID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-race-retry-complete",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if _, ok, err := repository.ClaimNextQueued(context.Background(), "worker-race-retry-complete"); err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}

	start := make(chan struct{})
	completeErrCh := make(chan error, 1)
	retryErrCh := make(chan error, 1)

	go func() {
		<-start
		_, err := repository.Complete(
			context.Background(),
			taskID,
			"worker-race-retry-complete",
			domain.TaskStatusSuccess,
			"",
			"",
		)
		completeErrCh <- err
	}()

	go func() {
		<-start
		ctx := audit.WithActor(context.Background(), audit.Actor{
			Type: audit.ActorTypeAdminOperator,
			ID:   "admin-race-retry-complete",
		})
		_, err := repository.ApplyAdminTransition(ctx, taskID, domain.TaskAdminActionRetry)
		retryErrCh <- err
	}()

	close(start)
	completeErr := <-completeErrCh
	retryErr := <-retryErrCh

	if completeErr != nil {
		t.Fatalf("Complete() expected success in race, got %v", completeErr)
	}
	if !domain.IsDomainErrorCode(retryErr, domain.ErrorCodeRetryNotAllowed) {
		t.Fatalf("expected retry loser to return %s, got %v", domain.ErrorCodeRetryNotAllowed, retryErr)
	}

	got, err := repository.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != domain.TaskStatusSuccess {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusSuccess, got.Status)
	}
}

func TestTaskRepositoryApplyAdminTransitionRetryVsRetryRaceHasSingleWinner(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareAuditLogsSchema(t, pool)
	prepareTaskSchemaForCanceledLifecycle(t, pool)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-admin-race-retry-retry-01")
	taskID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-race-retry-retry",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if _, ok, err := repository.ClaimNextQueued(context.Background(), "worker-race-retry-retry"); err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}
	if _, err := repository.Complete(
		context.Background(),
		taskID,
		"worker-race-retry-retry",
		domain.TaskStatusRetry,
		domain.ErrorCodeInternal,
		"transient error",
	); err != nil {
		t.Fatalf("Complete() expected retry status, got %v", err)
	}

	start := make(chan struct{})
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(iteration int) {
			<-start
			ctx := audit.WithActor(context.Background(), audit.Actor{
				Type: audit.ActorTypeAdminOperator,
				ID:   "admin-race-retry-retry-" + uuid.NewString(),
			})
			_, err := repository.ApplyAdminTransition(ctx, taskID, domain.TaskAdminActionRetry)
			errCh <- err
		}(i)
	}

	close(start)
	firstErr := <-errCh
	secondErr := <-errCh

	successCount := 0
	conflictCount := 0
	for _, err := range []error{firstErr, secondErr} {
		if err == nil {
			successCount++
			continue
		}
		if domain.IsDomainErrorCode(err, domain.ErrorCodeRetryNotAllowed) {
			conflictCount++
			continue
		}
		t.Fatalf("unexpected retry error: %v", err)
	}

	if successCount != 1 || conflictCount != 1 {
		t.Fatalf("expected one success and one retry conflict, got success=%d conflict=%d", successCount, conflictCount)
	}

	got, err := repository.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.Status != domain.TaskStatusQueued {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusQueued, got.Status)
	}
}

func TestTaskRepositoryRetryFromTaskCreatesQueuedChildWithSourceLinkage(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareAuditLogsSchema(t, pool)
	prepareTaskSchemaForCanceledLifecycle(t, pool)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-retry-linkage-01")
	sourceID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            sourceID,
		AccountID:     accountID,
		TargetProfile: "target-profile-retry-source-linkage",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if _, ok, err := repository.ClaimNextQueued(context.Background(), "worker-retry-source-linkage"); err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}
	if _, err := repository.Complete(
		context.Background(),
		sourceID,
		"worker-retry-source-linkage",
		domain.TaskStatusRetry,
		domain.ErrorCodeInternal,
		"transient retry source",
	); err != nil {
		t.Fatalf("Complete(retry) error = %v", err)
	}

	ctx := audit.WithActor(context.Background(), audit.Actor{
		Type: audit.ActorTypeAdminOperator,
		ID:   "admin-retry-source-linkage",
	})
	newTask, err := repository.RetryFromTask(ctx, sourceID)
	if err != nil {
		t.Fatalf("RetryFromTask() error = %v", err)
	}
	if newTask.Status != domain.TaskStatusQueued {
		t.Fatalf("expected new task status %s, got %s", domain.TaskStatusQueued, newTask.Status)
	}
	if newTask.SourceTaskID == nil || *newTask.SourceTaskID != sourceID {
		t.Fatalf("expected source_task_id=%s, got %+v", sourceID.String(), newTask.SourceTaskID)
	}
	if newTask.AccountID != accountID {
		t.Fatalf("expected account_id=%s, got %s", accountID.String(), newTask.AccountID.String())
	}
	if newTask.Attempt != 0 {
		t.Fatalf("expected attempt=0 for new retry task, got %d", newTask.Attempt)
	}
	if newTask.ClaimedBy != "" || newTask.ClaimedAt != nil || newTask.StartedAt != nil || newTask.FinishedAt != nil {
		t.Fatalf("expected clean queued lifecycle fields, got %+v", newTask)
	}
	if newTask.ErrorCode != "" || newTask.ResultReason != "" {
		t.Fatalf("expected empty error fields for new retry task, got error_code=%s result_reason=%s", newTask.ErrorCode, newTask.ResultReason)
	}

	sourceTask, err := repository.GetByID(context.Background(), sourceID)
	if err != nil {
		t.Fatalf("GetByID(source) error = %v", err)
	}
	if sourceTask.Status != domain.TaskStatusRetry {
		t.Fatalf("expected source status to remain %s, got %s", domain.TaskStatusRetry, sourceTask.Status)
	}

	reloadedNewTask, err := repository.GetByID(context.Background(), newTask.ID)
	if err != nil {
		t.Fatalf("GetByID(new) error = %v", err)
	}
	if reloadedNewTask.SourceTaskID == nil || *reloadedNewTask.SourceTaskID != sourceID {
		t.Fatalf("expected persisted source_task_id=%s, got %+v", sourceID.String(), reloadedNewTask.SourceTaskID)
	}
}

func TestTaskRepositoryRetryFromTaskRejectsNotAllowedStatusWithoutInsert(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareAuditLogsSchema(t, pool)
	prepareTaskSchemaForCanceledLifecycle(t, pool)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-retry-deny-01")
	sourceID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            sourceID,
		AccountID:     accountID,
		TargetProfile: "target-profile-retry-deny",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	_, err := repository.RetryFromTask(context.Background(), sourceID)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeRetryNotAllowed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeRetryNotAllowed, err)
	}

	var linkedChildren int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)::INT
		FROM tasks
		WHERE source_task_id = $1
	`, sourceID).Scan(&linkedChildren); err != nil {
		t.Fatalf("count linked retry tasks: %v", err)
	}
	if linkedChildren != 0 {
		t.Fatalf("expected 0 linked retry tasks on denied retry, got %d", linkedChildren)
	}
}

func TestTaskRepositoryRetryFromTaskRollsBackWhenAuditWriteFails(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareTaskSchemaForCanceledLifecycle(t, pool)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-retry-rollback-01")
	sourceID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            sourceID,
		AccountID:     accountID,
		TargetProfile: "target-profile-retry-rollback",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if _, ok, err := repository.ClaimNextQueued(context.Background(), "worker-retry-rollback"); err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}
	if _, err := repository.Complete(
		context.Background(),
		sourceID,
		"worker-retry-rollback",
		domain.TaskStatusRetry,
		domain.ErrorCodeInternal,
		"transient retry source for rollback",
	); err != nil {
		t.Fatalf("Complete(retry) error = %v", err)
	}

	// audit_logs table is absent here -> entire retry tx must rollback.
	_, err := repository.RetryFromTask(context.Background(), sourceID)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeTaskQueueWriteFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeTaskQueueWriteFailed, err)
	}

	var linkedChildren int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)::INT
		FROM tasks
		WHERE source_task_id = $1
	`, sourceID).Scan(&linkedChildren); err != nil {
		t.Fatalf("count linked retry tasks after rollback: %v", err)
	}
	if linkedChildren != 0 {
		t.Fatalf("expected rollback with 0 linked tasks, got %d", linkedChildren)
	}
}

func TestTaskRepositoryRetryFromTaskConcurrentRequestsKeepSourceUnchanged(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareAuditLogsSchema(t, pool)
	prepareTaskSchemaForCanceledLifecycle(t, pool)
	repository := postgresrepo.NewTaskRepository(pool)

	accountID := createTestAccount(t, pool, "task-retry-concurrency-01")
	sourceID := uuid.New()
	if _, err := repository.Enqueue(context.Background(), domain.Task{
		ID:            sourceID,
		AccountID:     accountID,
		TargetProfile: "target-profile-retry-concurrency",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if _, ok, err := repository.ClaimNextQueued(context.Background(), "worker-retry-concurrency"); err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}
	if _, err := repository.Complete(
		context.Background(),
		sourceID,
		"worker-retry-concurrency",
		domain.TaskStatusRetry,
		domain.ErrorCodeInternal,
		"transient source for concurrent retry",
	); err != nil {
		t.Fatalf("Complete(retry) error = %v", err)
	}

	start := make(chan struct{})
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			ctx := audit.WithActor(context.Background(), audit.Actor{
				Type: audit.ActorTypeAdminOperator,
				ID:   "admin-retry-concurrency-" + uuid.NewString(),
			})
			_, err := repository.RetryFromTask(ctx, sourceID)
			errCh <- err
		}()
	}

	close(start)
	firstErr := <-errCh
	secondErr := <-errCh
	for _, err := range []error{firstErr, secondErr} {
		if err != nil {
			t.Fatalf("expected concurrent retries to succeed, got %v", err)
		}
	}

	sourceTask, err := repository.GetByID(context.Background(), sourceID)
	if err != nil {
		t.Fatalf("GetByID(source) error = %v", err)
	}
	if sourceTask.Status != domain.TaskStatusRetry {
		t.Fatalf("expected source status to remain %s, got %s", domain.TaskStatusRetry, sourceTask.Status)
	}

	var linkedChildren int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)::INT
		FROM tasks
		WHERE source_task_id = $1
	`, sourceID).Scan(&linkedChildren); err != nil {
		t.Fatalf("count linked retry tasks: %v", err)
	}
	if linkedChildren != 2 {
		t.Fatalf("expected 2 linked retry tasks from concurrent retries, got %d", linkedChildren)
	}
}

func prepareTaskSchemaForCanceledLifecycle(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	_, err := pool.Exec(context.Background(), `
		ALTER TABLE tasks
			DROP CONSTRAINT IF EXISTS chk_tasks_status;
		ALTER TABLE tasks
			ADD CONSTRAINT chk_tasks_status CHECK (
				status IN ('queued', 'running', 'success', 'retry', 'fail', 'canceled')
			);

		ALTER TABLE tasks
			DROP CONSTRAINT IF EXISTS chk_tasks_lifecycle_consistency;
		ALTER TABLE tasks
			DROP CONSTRAINT IF EXISTS chk_tasks_reason_for_terminal_failure;
		ALTER TABLE tasks
			ADD CONSTRAINT chk_tasks_lifecycle_consistency CHECK (
				(
					status = 'queued'
					AND attempt >= 0
					AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
					AND claimed_by IS NULL
					AND claimed_at IS NULL
					AND started_at IS NULL
					AND finished_at IS NULL
					AND error_code IS NULL
					AND result_reason IS NULL
				)
				OR (
					status = 'running'
					AND attempt > 0
					AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
					AND claimed_by IS NOT NULL
					AND BTRIM(claimed_by) <> ''
					AND claimed_at IS NOT NULL
					AND started_at IS NOT NULL
					AND finished_at IS NULL
					AND error_code IS NULL
					AND result_reason IS NULL
				)
				OR (
					status = 'success'
					AND attempt > 0
					AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
					AND claimed_by IS NOT NULL
					AND BTRIM(claimed_by) <> ''
					AND claimed_at IS NOT NULL
					AND started_at IS NOT NULL
					AND finished_at IS NOT NULL
					AND error_code IS NULL
					AND result_reason IS NULL
				)
				OR (
					status IN ('retry', 'fail')
					AND attempt > 0
					AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
					AND claimed_by IS NOT NULL
					AND BTRIM(claimed_by) <> ''
					AND claimed_at IS NOT NULL
					AND started_at IS NOT NULL
					AND finished_at IS NOT NULL
					AND (
						NULLIF(BTRIM(error_code), '') IS NOT NULL
						OR NULLIF(BTRIM(result_reason), '') IS NOT NULL
					)
				)
				OR (
					status = 'canceled'
					AND attempt >= 0
					AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
					AND claimed_by IS NULL
					AND claimed_at IS NULL
					AND started_at IS NULL
					AND finished_at IS NOT NULL
					AND error_code IS NULL
					AND result_reason IS NULL
				)
			);
	`)
	if err != nil {
		t.Fatalf("prepare task schema for canceled lifecycle: %v", err)
	}
}
