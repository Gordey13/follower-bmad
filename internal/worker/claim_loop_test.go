package worker

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

type mockClaimLoopRepository struct {
	claimNextQueuedFn func(ctx context.Context, claimedBy string) (domain.Task, bool, error)
	completeFn        func(
		ctx context.Context,
		taskID uuid.UUID,
		claimedBy string,
		finalStatus domain.TaskStatus,
		errorCode domain.ErrorCode,
		resultReason string,
	) (domain.Task, error)
}

func (m *mockClaimLoopRepository) ClaimNextQueued(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
	if m.claimNextQueuedFn != nil {
		return m.claimNextQueuedFn(ctx, claimedBy)
	}

	return domain.Task{}, false, nil
}

func (m *mockClaimLoopRepository) Complete(
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

type mockClaimLoopHealth struct {
	status string
}

func (m *mockClaimLoopHealth) Snapshot(ctx context.Context) observability.HealthStatus {
	return observability.HealthStatus{
		Status: m.status,
	}
}

type mockClaimLoopMetrics struct {
	claimedCount int
	startedCount int
	completed    []string
	errorByStage map[string]int
}

type mockClaimLoopExecutionService struct {
	prepareFn func(
		ctx context.Context,
		task domain.Task,
	) (PreparedExecutionContext, error)
	resolveBootstrapFn func(
		ctx context.Context,
		task domain.Task,
		prepared PreparedExecutionContext,
	) (PreparedExecutionContext, error)
	runFollowFn func(
		ctx context.Context,
		input domain.FollowFlowInput,
	) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error)
	verifyFn func(
		ctx context.Context,
		input domain.FollowVerificationInput,
	) (domain.FollowVerificationResult, error)
	finalizeFn func(
		ctx context.Context,
		input domain.FollowExecutionFinalizationInput,
	) (domain.FollowResult, error)
	releaseFn func(ctx context.Context, accountID uuid.UUID, executionContextID string) error
}

func (m *mockClaimLoopExecutionService) PrepareClaimedTaskContext(
	ctx context.Context,
	task domain.Task,
) (PreparedExecutionContext, error) {
	if m.prepareFn != nil {
		return m.prepareFn(ctx, task)
	}
	return PreparedExecutionContext{}, nil
}

func (m *mockClaimLoopExecutionService) ResolveBootstrapForClaimedTask(
	ctx context.Context,
	task domain.Task,
	prepared PreparedExecutionContext,
) (PreparedExecutionContext, error) {
	if m.resolveBootstrapFn != nil {
		return m.resolveBootstrapFn(ctx, task, prepared)
	}
	return prepared, nil
}

func (m *mockClaimLoopExecutionService) RunFollowFlow(
	ctx context.Context,
	input domain.FollowFlowInput,
) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
	if m.runFollowFn != nil {
		return m.runFollowFn(ctx, input)
	}
	return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{}, nil
}

func (m *mockClaimLoopExecutionService) VerifyFollowResult(
	ctx context.Context,
	input domain.FollowVerificationInput,
) (domain.FollowVerificationResult, error) {
	if m.verifyFn != nil {
		return m.verifyFn(ctx, input)
	}

	return domain.FollowVerificationResult{
		Verified:          true,
		Signal:            defaultVerificationSignal(input.Outcome),
		Reason:            "verified in test",
		SessionChanged:    false,
		ScreenshotPayload: []byte("fake-png"),
	}, nil
}

func (m *mockClaimLoopExecutionService) FinalizeFollowExecution(
	ctx context.Context,
	input domain.FollowExecutionFinalizationInput,
) (domain.FollowResult, error) {
	if m.finalizeFn != nil {
		return m.finalizeFn(ctx, input)
	}

	return domain.FollowResult{
		TaskID:              input.TaskID,
		AccountID:           input.AccountID,
		TargetProfile:       input.TargetProfile,
		Attempt:             input.Attempt,
		Outcome:             input.FollowOutcome,
		Verified:            input.Verification.Verified,
		VerificationSignal:  input.Verification.Signal,
		VerificationReason:  input.Verification.Reason,
		ErrorCode:           input.Verification.ErrorCode,
		ScreenshotObjectKey: "accounts/" + input.AccountID.String() + "/tasks/" + input.TaskID.String() + "/attempts/1/screenshots/follow.png",
		ArtifactObjectKeys: []string{
			"accounts/" + input.AccountID.String() + "/tasks/" + input.TaskID.String() + "/attempts/1/artifacts/execution.json",
		},
	}, nil
}

func (m *mockClaimLoopExecutionService) ReleaseExecutionContext(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
) error {
	if m.releaseFn != nil {
		return m.releaseFn(ctx, accountID, executionContextID)
	}
	return nil
}

func defaultVerificationSignal(outcome domain.FollowFlowOutcome) domain.FollowVerificationSignal {
	switch outcome {
	case domain.FollowFlowOutcomeCompleted:
		return domain.FollowVerificationSignalFollowConfirmed
	case domain.FollowFlowOutcomeAlreadyDone:
		return domain.FollowVerificationSignalAlreadyDone
	case domain.FollowFlowOutcomeActionUnavailable:
		return domain.FollowVerificationSignalActionUnavailable
	case domain.FollowFlowOutcomeTargetUnreachable:
		return domain.FollowVerificationSignalTargetUnreachable
	default:
		return domain.FollowVerificationSignalNavigationFailed
	}
}

func (m *mockClaimLoopMetrics) RecordClaimed() {
	m.claimedCount++
}

func (m *mockClaimLoopMetrics) RecordStarted() {
	m.startedCount++
}

func (m *mockClaimLoopMetrics) RecordCompleted(status string) {
	m.completed = append(m.completed, status)
}

func (m *mockClaimLoopMetrics) RecordError(stage string) {
	if m.errorByStage == nil {
		m.errorByStage = map[string]int{}
	}
	m.errorByStage[stage]++
}

func TestClaimOnceSkipsWhenServiceNotReady(t *testing.T) {
	t.Parallel()

	repoCalled := false
	repository := &mockClaimLoopRepository{
		claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
			repoCalled = true
			return domain.Task{}, false, nil
		},
	}

	loop := NewClaimLoop(
		repository,
		&mockClaimLoopHealth{status: observability.StatusNotReady},
		&mockClaimLoopMetrics{},
		"worker-not-ready",
		time.Second,
		testClaimLoopLogger(),
	)

	_, claimed, err := loop.ClaimOnce(context.Background())
	if err != nil {
		t.Fatalf("ClaimOnce() error = %v", err)
	}
	if claimed {
		t.Fatal("expected claimed=false when service is not ready")
	}
	if repoCalled {
		t.Fatal("expected repository claim not to be called when service is not ready")
	}
}

func TestClaimOnceClaimsTaskAndRecordsMetrics(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	var gotClaimedBy string
	metrics := &mockClaimLoopMetrics{}

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				gotClaimedBy = claimedBy
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-user-success",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-ready-01",
		time.Second,
		testClaimLoopLogger(),
	)

	task, claimed, err := loop.ClaimOnce(context.Background())
	if err != nil {
		t.Fatalf("ClaimOnce() error = %v", err)
	}
	if !claimed {
		t.Fatal("expected claimed=true")
	}
	if task.ID != taskID {
		t.Fatalf("expected task id %s, got %s", taskID.String(), task.ID.String())
	}
	if gotClaimedBy != "worker-ready-01" {
		t.Fatalf("expected claimed_by worker-ready-01, got %s", gotClaimedBy)
	}
	if metrics.claimedCount != 1 || metrics.startedCount != 1 {
		t.Fatalf("expected claim/start metrics to increment once, got claimed=%d started=%d", metrics.claimedCount, metrics.startedCount)
	}
}

func TestClaimOnceRecordsClaimErrorMetric(t *testing.T) {
	t.Parallel()

	metrics := &mockClaimLoopMetrics{}
	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{}, false, errors.New("db unavailable")
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-claim-error",
		time.Second,
		testClaimLoopLogger(),
	)

	_, _, err := loop.ClaimOnce(context.Background())
	if err == nil {
		t.Fatal("expected claim error")
	}
	if metrics.errorByStage["claim"] != 1 {
		t.Fatalf("expected claim error metric = 1, got %d", metrics.errorByStage["claim"])
	}
}

func TestCompleteRecordsCompletionMetric(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	var gotClaimedBy string
	metrics := &mockClaimLoopMetrics{}

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotClaimedBy = claimedBy
				return domain.Task{
					ID:        gotTaskID,
					AccountID: accountID,
					Status:    finalStatus,
					Attempt:   2,
					ClaimedBy: claimedBy,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-complete-01",
		time.Second,
		testClaimLoopLogger(),
	)

	completed, err := loop.Complete(
		context.Background(),
		taskID,
		domain.TaskStatusSuccess,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if completed.ID != taskID {
		t.Fatalf("expected task id %s, got %s", taskID.String(), completed.ID.String())
	}
	if gotClaimedBy != "worker-complete-01" {
		t.Fatalf("expected claimed_by worker-complete-01, got %s", gotClaimedBy)
	}
	if len(metrics.completed) != 1 || metrics.completed[0] != string(domain.TaskStatusSuccess) {
		t.Fatalf("expected completion metric for success, got %+v", metrics.completed)
	}
}

func TestCompleteRecordsErrorMetricOnFailure(t *testing.T) {
	t.Parallel()

	metrics := &mockClaimLoopMetrics{}
	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			completeFn: func(
				ctx context.Context,
				taskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				return domain.Task{}, errors.New("update failed")
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-complete-error",
		time.Second,
		testClaimLoopLogger(),
	)

	_, err := loop.Complete(
		context.Background(),
		uuid.New(),
		domain.TaskStatusFail,
		domain.ErrorCodeInternal,
		"failure",
	)
	if err == nil {
		t.Fatal("expected complete error")
	}
	if metrics.errorByStage["complete"] != 1 {
		t.Fatalf("expected complete error metric = 1, got %d", metrics.errorByStage["complete"])
	}
}

func TestRunIterationCompletesClaimedTaskAfterExecutionPreparation(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	metrics := &mockClaimLoopMetrics{}

	var completeCalled bool
	var prepareCalled bool
	var releaseCalled bool

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-user-success",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				completeCalled = true
				if gotTaskID != taskID {
					t.Fatalf("expected task id %s, got %s", taskID.String(), gotTaskID.String())
				}
				if finalStatus != domain.TaskStatusSuccess {
					t.Fatalf("expected final status %s, got %s", domain.TaskStatusSuccess, finalStatus)
				}
				if errorCode != "" || resultReason != "" {
					t.Fatalf("expected empty completion reason for success, got error_code=%s reason=%s", errorCode, resultReason)
				}

				return domain.Task{
					ID:            gotTaskID,
					AccountID:     accountID,
					TargetProfile: "target-user-success",
					Status:        finalStatus,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-orchestrate-success",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(
				ctx context.Context,
				gotTask domain.Task,
			) (PreparedExecutionContext, error) {
				prepareCalled = true
				if gotTask.ID != taskID {
					t.Fatalf("expected task id %s, got %s", taskID.String(), gotTask.ID.String())
				}
				if gotTask.AccountID != accountID {
					t.Fatalf("expected account id %s, got %s", accountID.String(), gotTask.AccountID.String())
				}
				if gotTask.ClaimedBy != "worker-orchestrate-success" {
					t.Fatalf("expected claimed_by worker-orchestrate-success, got %s", gotTask.ClaimedBy)
				}
				if gotTask.Attempt != 1 {
					t.Fatalf("expected attempt 1, got %d", gotTask.Attempt)
				}
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: gotTask.AccountID},
					},
					ExecutionContextID: gotTask.ClaimedBy,
					SessionMetadata: domain.SessionMetadata{
						AccountID: gotTask.AccountID,
						Revision:  1,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + gotTask.AccountID.String() + "/sessions/1.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				if input.TargetProfile != "target-user-success" {
					t.Fatalf("expected target profile target-user-success, got %s", input.TargetProfile)
				}
				return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    5,
					ExecutionDurationMS: 8,
				}, nil
			},
			releaseFn: func(ctx context.Context, gotAccountID uuid.UUID, executionContextID string) error {
				releaseCalled = true
				if gotAccountID != accountID {
					t.Fatalf("expected account id %s, got %s", accountID.String(), gotAccountID.String())
				}
				if executionContextID != "worker-orchestrate-success" {
					t.Fatalf("expected execution context worker-orchestrate-success, got %s", executionContextID)
				}
				return nil
			},
		},
	)

	loop.runIteration(context.Background())

	if !prepareCalled {
		t.Fatal("expected PrepareClaimedTaskContext to be called")
	}
	if !releaseCalled {
		t.Fatal("expected ReleaseExecutionContext to be called")
	}
	if !completeCalled {
		t.Fatal("expected task completion to be called")
	}
	if len(metrics.completed) != 1 || metrics.completed[0] != string(domain.TaskStatusSuccess) {
		t.Fatalf("expected success completion metric, got %+v", metrics.completed)
	}
}

func TestRunIterationDoesNotDoubleCompleteWhenPreparationFails(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	metrics := &mockClaimLoopMetrics{}

	var completeCalled bool

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-user-prep-fail",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				_ context.Context,
				_ uuid.UUID,
				_ string,
				_ domain.TaskStatus,
				_ domain.ErrorCode,
				_ string,
			) (domain.Task, error) {
				completeCalled = true
				return domain.Task{}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-orchestrate-retry",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(
				ctx context.Context,
				gotTask domain.Task,
			) (PreparedExecutionContext, error) {
				if gotTask.ID != taskID {
					t.Fatalf("expected task id %s, got %s", taskID.String(), gotTask.ID.String())
				}
				return PreparedExecutionContext{}, domain.NewDomainError(
					domain.ErrorCodeInternal,
					"temporary storage outage",
				)
			},
		},
	)

	loop.runIteration(context.Background())

	if completeCalled {
		t.Fatal("expected claim loop not to call Complete on preparation failure (ExecutionService owns failure completion)")
	}
	if len(metrics.completed) != 0 {
		t.Fatalf("expected no completion metric from claim loop on preparation failure, got %+v", metrics.completed)
	}
}

func TestRunIterationCompletesRetryWhenBootstrapResolutionFails(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	metrics := &mockClaimLoopMetrics{}

	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode
	var gotResultReason string
	runFollowCalled := false
	releaseCalled := false

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-bootstrap-required",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				gotResultReason = resultReason
				return domain.Task{
					ID:           gotTaskID,
					AccountID:    accountID,
					Status:       finalStatus,
					Attempt:      1,
					ClaimedBy:    claimedBy,
					ErrorCode:    errorCode,
					ResultReason: resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-bootstrap-required",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: false,
					BootstrapRequired:  true,
					BootstrapReason:    domain.ErrorCodeAuthBootstrapRequired,
					BootstrapSource:    domain.ErrorCodeSessionPayloadMissing,
				}, nil
			},
			resolveBootstrapFn: func(
				ctx context.Context,
				task domain.Task,
				prepared PreparedExecutionContext,
			) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{}, domain.NewDomainError(
					domain.ErrorCodeAuthBootstrapFailed,
					"bootstrap runner timed out",
				)
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				runFollowCalled = true
				return "", domain.FollowFlowDiagnostics{}, nil
			},
			releaseFn: func(ctx context.Context, gotAccountID uuid.UUID, executionContextID string) error {
				releaseCalled = true
				return nil
			},
		},
	)

	loop.runIteration(context.Background())

	if gotFinalStatus != domain.TaskStatusRetry {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusRetry, gotFinalStatus)
	}
	if gotErrorCode != domain.ErrorCodeAuthBootstrapFailed {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeAuthBootstrapFailed, gotErrorCode)
	}
	if !strings.Contains(gotResultReason, "bootstrap_login_failed") {
		t.Fatalf("expected bootstrap result reason, got %q", gotResultReason)
	}
	if runFollowCalled {
		t.Fatal("RunFollowFlow must not be called when bootstrap is required")
	}
	if !releaseCalled {
		t.Fatal("expected ReleaseExecutionContext to be called")
	}
	if len(metrics.completed) != 1 || metrics.completed[0] != string(domain.TaskStatusRetry) {
		t.Fatalf("expected retry completion metric, got %+v", metrics.completed)
	}
}

func TestRunIterationCompletesFailOnBootstrapInvalidCredentials(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()

	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-bootstrap-invalid-credentials",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				return domain.Task{
					ID:           gotTaskID,
					AccountID:    accountID,
					Status:       finalStatus,
					Attempt:      1,
					ClaimedBy:    claimedBy,
					ErrorCode:    errorCode,
					ResultReason: resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-bootstrap-invalid-credentials",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: false,
					BootstrapRequired:  true,
					BootstrapReason:    domain.ErrorCodeAuthBootstrapRequired,
					BootstrapSource:    domain.ErrorCodeSessionMetadataNotFound,
				}, nil
			},
			resolveBootstrapFn: func(
				ctx context.Context,
				task domain.Task,
				prepared PreparedExecutionContext,
			) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{}, domain.NewDomainError(
					domain.ErrorCodeAuthInvalidCredentials,
					"credential source returned invalid pair",
				)
			},
		},
	)

	loop.runIteration(context.Background())

	if gotFinalStatus != domain.TaskStatusFail {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusFail, gotFinalStatus)
	}
	if gotErrorCode != domain.ErrorCodeAuthInvalidCredentials {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeAuthInvalidCredentials, gotErrorCode)
	}
}

func TestRunIterationRunsFollowAfterBootstrapResolutionSuccess(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	runFollowCalled := false

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-bootstrap-success-follow",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				return domain.Task{
					ID:        gotTaskID,
					AccountID: accountID,
					Status:    finalStatus,
					Attempt:   1,
					ClaimedBy: claimedBy,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-bootstrap-success-follow",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: false,
					BootstrapRequired:  true,
					BootstrapReason:    domain.ErrorCodeAuthBootstrapRequired,
					BootstrapSource:    domain.ErrorCodeSessionMetadataNotFound,
				}, nil
			},
			resolveBootstrapFn: func(
				ctx context.Context,
				task domain.Task,
				prepared PreparedExecutionContext,
			) (PreparedExecutionContext, error) {
				prepared.BootstrapRequired = false
				prepared.ReadyForFollowFlow = true
				prepared.SessionMetadata = domain.SessionMetadata{
					AccountID: task.AccountID,
					Revision:  1,
					Status:    domain.SessionStatusValid,
					ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/1.json",
				}
				prepared.SessionPayload = []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`)
				return prepared, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				runFollowCalled = true
				return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    1,
					ExecutionDurationMS: 1,
				}, nil
			},
		},
	)

	loop.runIteration(context.Background())

	if !runFollowCalled {
		t.Fatal("expected RunFollowFlow to execute after bootstrap resolution success")
	}
}

func TestRunIterationTreatsFollowAlreadyDoneAsSuccess(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	metrics := &mockClaimLoopMetrics{}

	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode
	var gotReason string
	releaseCalled := false

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-already-done",
					Status:        domain.TaskStatusRunning,
					Attempt:       2,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				gotReason = resultReason
				return domain.Task{
					ID:            gotTaskID,
					AccountID:     accountID,
					TargetProfile: "target-already-done",
					Status:        finalStatus,
					Attempt:       2,
					ClaimedBy:     claimedBy,
					ErrorCode:     errorCode,
					ResultReason:  resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-already-done",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  10,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/10.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeAlreadyDone, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    4,
					ExecutionDurationMS: 6,
				}, nil
			},
			releaseFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
				releaseCalled = true
				return nil
			},
		},
	)

	loop.runIteration(context.Background())

	if gotFinalStatus != domain.TaskStatusSuccess {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusSuccess, gotFinalStatus)
	}
	if gotErrorCode != "" || gotReason != "" {
		t.Fatalf("expected empty reason fields for success completion, got error_code=%s reason=%s", gotErrorCode, gotReason)
	}
	if !releaseCalled {
		t.Fatal("expected ReleaseExecutionContext to be called")
	}
}

func TestRunIterationMapsActionUnavailableToFail(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()

	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode
	releaseCalled := false

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-action-unavailable",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				return domain.Task{
					ID:            gotTaskID,
					AccountID:     accountID,
					TargetProfile: "target-action-unavailable",
					Status:        finalStatus,
					Attempt:       1,
					ClaimedBy:     claimedBy,
					ErrorCode:     errorCode,
					ResultReason:  resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-action-unavailable",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  1,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/1.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeActionUnavailable, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    1,
					ExecutionDurationMS: 2,
				}, nil
			},
			releaseFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
				releaseCalled = true
				return nil
			},
		},
	)

	loop.runIteration(context.Background())

	if gotFinalStatus != domain.TaskStatusFail {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusFail, gotFinalStatus)
	}
	if gotErrorCode != domain.ErrorCodeFollowActionUnavailable {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowActionUnavailable, gotErrorCode)
	}
	if !releaseCalled {
		t.Fatal("expected ReleaseExecutionContext to be called")
	}
}

func TestRunIterationMapsTargetUnreachableToFail(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()

	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode
	releaseCalled := false

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-unreachable",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				return domain.Task{
					ID:            gotTaskID,
					AccountID:     accountID,
					TargetProfile: "target-unreachable",
					Status:        finalStatus,
					Attempt:       1,
					ClaimedBy:     claimedBy,
					ErrorCode:     errorCode,
					ResultReason:  resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-target-unreachable",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  1,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/1.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeTargetUnreachable, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    1,
					ExecutionDurationMS: 2,
				}, nil
			},
			releaseFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
				releaseCalled = true
				return nil
			},
		},
	)

	loop.runIteration(context.Background())

	if gotFinalStatus != domain.TaskStatusFail {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusFail, gotFinalStatus)
	}
	if gotErrorCode != domain.ErrorCodeFollowTargetUnreachable {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowTargetUnreachable, gotErrorCode)
	}
	if !releaseCalled {
		t.Fatal("expected ReleaseExecutionContext to be called")
	}
}

func TestRunIterationMapsFollowTransientErrorToRetry(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()

	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode
	releaseCalled := false

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-transient",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				return domain.Task{
					ID:            gotTaskID,
					AccountID:     accountID,
					TargetProfile: "target-transient",
					Status:        finalStatus,
					Attempt:       1,
					ClaimedBy:     claimedBy,
					ErrorCode:     errorCode,
					ResultReason:  resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-transient",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  1,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/1.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return "", domain.FollowFlowDiagnostics{
						Engine:              "mock",
						WarmupDurationMS:    2,
						ExecutionDurationMS: 7,
					}, domain.NewDomainError(
						domain.ErrorCodeFollowNavigationFailed,
						"playwright timeout",
					)
			},
			releaseFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
				releaseCalled = true
				return nil
			},
		},
	)

	loop.runIteration(context.Background())

	if gotFinalStatus != domain.TaskStatusRetry {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusRetry, gotFinalStatus)
	}
	if gotErrorCode != domain.ErrorCodeFollowNavigationFailed {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowNavigationFailed, gotErrorCode)
	}
	if !releaseCalled {
		t.Fatal("expected ReleaseExecutionContext to be called")
	}
}

func TestRunIterationReleasesContextEvenWhenParentContextIsCanceled(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	releaseCalled := false

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-release-cancelled-parent",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				return domain.Task{
					ID:            gotTaskID,
					AccountID:     accountID,
					TargetProfile: "target-release-cancelled-parent",
					Status:        finalStatus,
					Attempt:       1,
					ClaimedBy:     claimedBy,
					ErrorCode:     errorCode,
					ResultReason:  resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-release-cancelled-parent",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  1,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/1.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupCompleted:     true,
					WarmupDurationMS:    1,
					ExecutionDurationMS: 1,
				}, nil
			},
			releaseFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
				releaseCalled = true
				if err := ctx.Err(); err != nil {
					t.Fatalf("expected release context to not be canceled, got %v", err)
				}
				if _, hasDeadline := ctx.Deadline(); !hasDeadline {
					t.Fatal("expected release context deadline to be set")
				}
				return nil
			},
		},
	)

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()

	loop.runIteration(parentCtx)

	if !releaseCalled {
		t.Fatal("expected ReleaseExecutionContext to be called")
	}
}

func TestRunIterationClassifiesFastExecutionErrorAsExecutionStageWhenWarmupCompleted(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	metrics := &mockClaimLoopMetrics{}

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-fast-exec-error",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				return domain.Task{
					ID:            gotTaskID,
					AccountID:     accountID,
					TargetProfile: "target-fast-exec-error",
					Status:        finalStatus,
					Attempt:       1,
					ClaimedBy:     claimedBy,
					ErrorCode:     errorCode,
					ResultReason:  resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-fast-exec-error",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  1,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/1.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return "", domain.FollowFlowDiagnostics{
						Engine:              "mock",
						WarmupCompleted:     true,
						WarmupDurationMS:    1,
						ExecutionDurationMS: 0,
					}, domain.NewDomainError(
						domain.ErrorCodeFollowNavigationFailed,
						"fast follow action failure",
					)
			},
		},
	)

	loop.runIteration(context.Background())

	if metrics.errorByStage["follow.warmup"] != 0 {
		t.Fatalf("expected no warmup error metric, got %d", metrics.errorByStage["follow.warmup"])
	}
	if metrics.errorByStage["follow.execution"] != 1 {
		t.Fatalf("expected execution error metric = 1, got %d", metrics.errorByStage["follow.execution"])
	}
}

func TestRunIterationMapsVerifyErrorToRetryAndFinalizesWithSyntheticVerification(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	metrics := &mockClaimLoopMetrics{}

	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode
	var gotResultReason string
	finalizeCalled := false

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-verify-error",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				gotResultReason = resultReason
				return domain.Task{
					ID:           gotTaskID,
					AccountID:    accountID,
					Status:       finalStatus,
					Attempt:      1,
					ClaimedBy:    claimedBy,
					ErrorCode:    errorCode,
					ResultReason: resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-verify-error",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  1,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/1.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    2,
					ExecutionDurationMS: 3,
				}, nil
			},
			verifyFn: func(
				ctx context.Context,
				input domain.FollowVerificationInput,
			) (domain.FollowVerificationResult, error) {
				return domain.FollowVerificationResult{}, domain.NewDomainError(
					domain.ErrorCodeFollowVerifyFailed,
					"verify transport timeout",
				)
			},
			finalizeFn: func(
				ctx context.Context,
				input domain.FollowExecutionFinalizationInput,
			) (domain.FollowResult, error) {
				finalizeCalled = true
				if input.Verification.Verified {
					t.Fatal("expected synthetic verification to be unverified when verify fails")
				}
				if input.Verification.ErrorCode != domain.ErrorCodeFollowVerifyFailed {
					t.Fatalf("expected verification error code %s, got %s", domain.ErrorCodeFollowVerifyFailed, input.Verification.ErrorCode)
				}
				return domain.FollowResult{
					TaskID:             input.TaskID,
					AccountID:          input.AccountID,
					TargetProfile:      input.TargetProfile,
					Attempt:            input.Attempt,
					Outcome:            input.FollowOutcome,
					Verified:           false,
					VerificationSignal: input.Verification.Signal,
					ErrorCode:          input.Verification.ErrorCode,
					ScreenshotObjectKey: "accounts/" + input.AccountID.String() + "/tasks/" +
						input.TaskID.String() + "/attempts/1/screenshots/follow.png",
					ArtifactObjectKeys: []string{
						"accounts/" + input.AccountID.String() + "/tasks/" + input.TaskID.String() + "/attempts/1/artifacts/execution.json",
					},
				}, nil
			},
		},
	)

	loop.runIteration(context.Background())

	if !finalizeCalled {
		t.Fatal("expected finalize to be called even when verify fails")
	}
	if gotFinalStatus != domain.TaskStatusRetry {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusRetry, gotFinalStatus)
	}
	if gotErrorCode != domain.ErrorCodeFollowVerifyFailed {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowVerifyFailed, gotErrorCode)
	}
	if !strings.Contains(gotResultReason, "status=retry") {
		t.Fatalf("expected deterministic status marker in result reason, got %q", gotResultReason)
	}
	if !strings.Contains(gotResultReason, "error_code=follow_verify_failed") {
		t.Fatalf("expected deterministic error_code marker in result reason, got %q", gotResultReason)
	}
	if metrics.errorByStage["follow.verify"] != 1 {
		t.Fatalf("expected verify error metric = 1, got %d", metrics.errorByStage["follow.verify"])
	}
}

func TestRunIterationMapsUnverifiedVerifyFailedResultToDeterministicErrorCode(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	metrics := &mockClaimLoopMetrics{}

	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode
	var gotResultReason string
	finalizeCalled := false

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-verify-unverified",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				gotResultReason = resultReason
				return domain.Task{
					ID:           gotTaskID,
					AccountID:    accountID,
					Status:       finalStatus,
					Attempt:      1,
					ClaimedBy:    claimedBy,
					ErrorCode:    errorCode,
					ResultReason: resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-verify-unverified",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  1,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/1.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    2,
					ExecutionDurationMS: 3,
				}, nil
			},
			verifyFn: func(
				ctx context.Context,
				input domain.FollowVerificationInput,
			) (domain.FollowVerificationResult, error) {
				return domain.FollowVerificationResult{
					Verified:          false,
					Signal:            domain.FollowVerificationSignalVerifyFailed,
					Reason:            "verify ui did not confirm follow state",
					ErrorCode:         domain.ErrorCodeFollowVerifyFailed,
					SessionChanged:    false,
					ScreenshotPayload: []byte("fake-png"),
				}, nil
			},
			finalizeFn: func(
				ctx context.Context,
				input domain.FollowExecutionFinalizationInput,
			) (domain.FollowResult, error) {
				finalizeCalled = true
				return domain.FollowResult{
					TaskID:              input.TaskID,
					AccountID:           input.AccountID,
					TargetProfile:       input.TargetProfile,
					Attempt:             input.Attempt,
					Outcome:             input.FollowOutcome,
					Verified:            input.Verification.Verified,
					VerificationSignal:  input.Verification.Signal,
					VerificationReason:  input.Verification.Reason,
					ErrorCode:           input.Verification.ErrorCode,
					ScreenshotObjectKey: "accounts/" + input.AccountID.String() + "/tasks/" + input.TaskID.String() + "/attempts/1/screenshots/follow.png",
					ArtifactObjectKeys: []string{
						"accounts/" + input.AccountID.String() + "/tasks/" + input.TaskID.String() + "/attempts/1/artifacts/execution.json",
					},
				}, nil
			},
		},
	)

	loop.runIteration(context.Background())

	if !finalizeCalled {
		t.Fatal("expected finalize to be called for unverified verify result")
	}
	if gotFinalStatus != domain.TaskStatusRetry {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusRetry, gotFinalStatus)
	}
	if gotErrorCode != domain.ErrorCodeFollowVerifyFailed {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowVerifyFailed, gotErrorCode)
	}
	if !strings.Contains(gotResultReason, "status=retry") {
		t.Fatalf("expected deterministic status marker in result reason, got %q", gotResultReason)
	}
	if !strings.Contains(gotResultReason, "error_code=follow_verify_failed") {
		t.Fatalf("expected deterministic error_code marker in result reason, got %q", gotResultReason)
	}
	if metrics.errorByStage["follow.verify"] != 1 {
		t.Fatalf("expected verify error metric = 1, got %d", metrics.errorByStage["follow.verify"])
	}
}

func TestRunIterationUsesVerificationPayloadForFinalization(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	preparedPayload := []byte(`{"cookies":[{"name":"sid","value":"prepared"}]}`)
	updatedPayload := []byte(`{"cookies":[{"name":"sid","value":"updated"}]}`)

	var gotFinalizationPayload []byte

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-session-updated",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				return domain.Task{
					ID:        gotTaskID,
					AccountID: accountID,
					Status:    finalStatus,
					Attempt:   1,
					ClaimedBy: claimedBy,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-session-updated",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  1,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/1.json",
					},
					SessionPayload:     preparedPayload,
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    1,
					ExecutionDurationMS: 2,
				}, nil
			},
			verifyFn: func(
				ctx context.Context,
				input domain.FollowVerificationInput,
			) (domain.FollowVerificationResult, error) {
				return domain.FollowVerificationResult{
					Verified:          true,
					Signal:            domain.FollowVerificationSignalFollowConfirmed,
					Reason:            "verified with updated session",
					SessionChanged:    true,
					SessionPayload:    updatedPayload,
					ScreenshotPayload: []byte("fake-png"),
				}, nil
			},
			finalizeFn: func(
				ctx context.Context,
				input domain.FollowExecutionFinalizationInput,
			) (domain.FollowResult, error) {
				gotFinalizationPayload = append([]byte(nil), input.SessionPayload...)
				return domain.FollowResult{
					TaskID:              input.TaskID,
					AccountID:           input.AccountID,
					TargetProfile:       input.TargetProfile,
					Attempt:             input.Attempt,
					Outcome:             input.FollowOutcome,
					Verified:            input.Verification.Verified,
					VerificationSignal:  input.Verification.Signal,
					VerificationReason:  input.Verification.Reason,
					ScreenshotObjectKey: "accounts/" + input.AccountID.String() + "/tasks/" + input.TaskID.String() + "/attempts/1/screenshots/follow.png",
					ArtifactObjectKeys: []string{
						"accounts/" + input.AccountID.String() + "/tasks/" + input.TaskID.String() + "/attempts/1/artifacts/execution.json",
					},
				}, nil
			},
		},
	)

	loop.runIteration(context.Background())

	if !bytes.Equal(gotFinalizationPayload, updatedPayload) {
		t.Fatalf("expected updated payload %q, got %q", string(updatedPayload), string(gotFinalizationPayload))
	}
	if bytes.Equal(gotFinalizationPayload, preparedPayload) {
		t.Fatal("expected finalization payload to differ from prepared payload")
	}
}

func TestVerificationFromErrorUsesVerifyFailedSignal(t *testing.T) {
	t.Parallel()

	result := verificationFromError(
		domain.NewDomainError(
			domain.ErrorCodeFollowVerifyFailed,
			"verify transport timeout",
		),
	)

	if result.Signal != domain.FollowVerificationSignalVerifyFailed {
		t.Fatalf("expected signal %s, got %s", domain.FollowVerificationSignalVerifyFailed, result.Signal)
	}
	if result.ErrorCode != domain.ErrorCodeFollowVerifyFailed {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowVerifyFailed, result.ErrorCode)
	}
	if result.SessionChanged {
		t.Fatal("expected session_changed=false for synthetic verify error result")
	}
}

func TestRunIterationPassesPreparedSessionRevisionToFinalizationInput(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	const preparedRevision int64 = 41
	var gotSessionRevision int64

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-finalization-revision",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				return domain.Task{
					ID:           gotTaskID,
					AccountID:    accountID,
					Status:       finalStatus,
					Attempt:      1,
					ClaimedBy:    claimedBy,
					ErrorCode:    errorCode,
					ResultReason: resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-session-revision",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  preparedRevision,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/41.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    1,
					ExecutionDurationMS: 2,
				}, nil
			},
			finalizeFn: func(
				ctx context.Context,
				input domain.FollowExecutionFinalizationInput,
			) (domain.FollowResult, error) {
				gotSessionRevision = input.SessionRevision
				return domain.FollowResult{
					TaskID:              input.TaskID,
					AccountID:           input.AccountID,
					TargetProfile:       input.TargetProfile,
					Attempt:             input.Attempt,
					Outcome:             input.FollowOutcome,
					Verified:            input.Verification.Verified,
					VerificationSignal:  input.Verification.Signal,
					VerificationReason:  input.Verification.Reason,
					ScreenshotObjectKey: "accounts/" + input.AccountID.String() + "/tasks/" + input.TaskID.String() + "/attempts/1/screenshots/follow.png",
					ArtifactObjectKeys: []string{
						"accounts/" + input.AccountID.String() + "/tasks/" + input.TaskID.String() + "/attempts/1/artifacts/execution.json",
					},
					SessionRevision: input.SessionRevision,
				}, nil
			},
		},
	)

	loop.runIteration(context.Background())

	if gotSessionRevision != preparedRevision {
		t.Fatalf("expected session revision %d to be forwarded to finalization, got %d", preparedRevision, gotSessionRevision)
	}
}

func TestRunIterationMapsSessionSaveFailedWithInvalidPayloadSourceToFail(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode
	var gotResultReason string

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-session-save-fail",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				gotResultReason = resultReason
				return domain.Task{
					ID:           gotTaskID,
					AccountID:    accountID,
					Status:       finalStatus,
					Attempt:      1,
					ClaimedBy:    claimedBy,
					ErrorCode:    errorCode,
					ResultReason: resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-session-save-fail",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  2,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/2.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    1,
					ExecutionDurationMS: 2,
				}, nil
			},
			finalizeFn: func(
				ctx context.Context,
				input domain.FollowExecutionFinalizationInput,
			) (domain.FollowResult, error) {
				return domain.FollowResult{}, domain.NewDomainError(
					domain.ErrorCodeSessionSaveFailed,
					"session payload persistence failed (source_error_code=session_payload_invalid)",
				)
			},
		},
	)

	loop.runIteration(context.Background())

	if gotFinalStatus != domain.TaskStatusFail {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusFail, gotFinalStatus)
	}
	if gotErrorCode != domain.ErrorCodeSessionSaveFailed {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeSessionSaveFailed, gotErrorCode)
	}
	if !strings.Contains(gotResultReason, "source_error_code=session_payload_invalid") {
		t.Fatalf("expected result reason to include source_error_code, got %q", gotResultReason)
	}
}

func TestRunIterationMapsSessionSaveFailedWithInternalSourceToRetry(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-session-save-retry",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				return domain.Task{
					ID:           gotTaskID,
					AccountID:    accountID,
					Status:       finalStatus,
					Attempt:      1,
					ClaimedBy:    claimedBy,
					ErrorCode:    errorCode,
					ResultReason: resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-session-save-retry",
		time.Second,
		testClaimLoopLogger(),
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  2,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/2.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    1,
					ExecutionDurationMS: 2,
				}, nil
			},
			finalizeFn: func(
				ctx context.Context,
				input domain.FollowExecutionFinalizationInput,
			) (domain.FollowResult, error) {
				return domain.FollowResult{}, domain.NewDomainError(
					domain.ErrorCodeSessionSaveFailed,
					"session payload persistence failed (source_error_code=internal_error)",
				)
			},
		},
	)

	loop.runIteration(context.Background())

	if gotFinalStatus != domain.TaskStatusRetry {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusRetry, gotFinalStatus)
	}
	if gotErrorCode != domain.ErrorCodeSessionSaveFailed {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeSessionSaveFailed, gotErrorCode)
	}
}

func TestRunIterationMapsFinalizationErrorToRetry(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	metrics := &mockClaimLoopMetrics{}
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))

	var gotFinalStatus domain.TaskStatus
	var gotErrorCode domain.ErrorCode
	releaseCalled := false

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-finalize-error",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
			completeFn: func(
				ctx context.Context,
				gotTaskID uuid.UUID,
				claimedBy string,
				finalStatus domain.TaskStatus,
				errorCode domain.ErrorCode,
				resultReason string,
			) (domain.Task, error) {
				gotFinalStatus = finalStatus
				gotErrorCode = errorCode
				return domain.Task{
					ID:           gotTaskID,
					AccountID:    accountID,
					Status:       finalStatus,
					Attempt:      1,
					ClaimedBy:    claimedBy,
					ErrorCode:    errorCode,
					ResultReason: resultReason,
				}, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		metrics,
		"worker-finalize-error",
		time.Second,
		logger,
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{
					AccountWithProxy: domain.AccountWithProxy{
						Account: domain.Account{ID: task.AccountID},
					},
					SessionMetadata: domain.SessionMetadata{
						AccountID: task.AccountID,
						Revision:  1,
						Status:    domain.SessionStatusValid,
						ObjectKey: "accounts/" + task.AccountID.String() + "/sessions/1.json",
					},
					SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
					ExecutionContextID: task.ClaimedBy,
					ReadyForFollowFlow: true,
				}, nil
			},
			runFollowFn: func(
				ctx context.Context,
				input domain.FollowFlowInput,
			) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
				return domain.FollowFlowOutcomeCompleted, domain.FollowFlowDiagnostics{
					Engine:              "mock",
					WarmupDurationMS:    1,
					ExecutionDurationMS: 2,
				}, nil
			},
			finalizeFn: func(
				ctx context.Context,
				input domain.FollowExecutionFinalizationInput,
			) (domain.FollowResult, error) {
				return domain.FollowResult{}, domain.NewDomainError(
					domain.ErrorCodeFollowResultPersistFailed,
					"postgres is temporarily unavailable",
				)
			},
			releaseFn: func(ctx context.Context, gotAccountID uuid.UUID, executionContextID string) error {
				releaseCalled = true
				return nil
			},
		},
	)

	loop.runIteration(context.Background())

	if gotFinalStatus != domain.TaskStatusRetry {
		t.Fatalf("expected final status %s, got %s", domain.TaskStatusRetry, gotFinalStatus)
	}
	if gotErrorCode != domain.ErrorCodeFollowResultPersistFailed {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowResultPersistFailed, gotErrorCode)
	}
	if metrics.errorByStage["follow.finalize"] != 1 {
		t.Fatalf("expected finalization error metric = 1, got %d", metrics.errorByStage["follow.finalize"])
	}
	if !releaseCalled {
		t.Fatal("expected ReleaseExecutionContext to be called")
	}
	output := buffer.String()
	if !strings.Contains(output, "follow.finalize.failed") {
		t.Fatalf("expected finalize failure event, got %q", output)
	}
	if !strings.Contains(output, "diagnostic_message=") {
		t.Fatalf("expected diagnostic_message field, got %q", output)
	}
}

func TestClaimOnceLogsLifecycleContractFields(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-log-contract",
					Status:        domain.TaskStatusRunning,
					Attempt:       5,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-log-contract",
		time.Second,
		logger,
	)

	_, claimed, err := loop.ClaimOnce(context.Background())
	if err != nil {
		t.Fatalf("ClaimOnce() error = %v", err)
	}
	if !claimed {
		t.Fatal("expected claimed=true")
	}

	output := buffer.String()
	if !strings.Contains(output, "task.claimed") {
		t.Fatalf("expected task.claimed event, got %q", output)
	}
	if !strings.Contains(output, "task.started") {
		t.Fatalf("expected task.started event, got %q", output)
	}
	requiredTokens := []string{
		"component=worker.claim_loop",
		"task_id=" + taskID.String(),
		"account_id=" + accountID.String(),
		"attempt=5",
		"error_code=eligible",
		"duration_ms=0",
	}
	for _, token := range requiredTokens {
		if !strings.Contains(output, token) {
			t.Fatalf("expected log output to contain %q, got %q", token, output)
		}
	}
}

func TestRunIterationLogsDiagnosticMessageForPreparationFailure(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))

	loop := NewClaimLoop(
		&mockClaimLoopRepository{
			claimNextQueuedFn: func(ctx context.Context, claimedBy string) (domain.Task, bool, error) {
				return domain.Task{
					ID:            taskID,
					AccountID:     accountID,
					TargetProfile: "target-prepare-failure-log",
					Status:        domain.TaskStatusRunning,
					Attempt:       1,
					ClaimedBy:     claimedBy,
				}, true, nil
			},
		},
		&mockClaimLoopHealth{status: observability.StatusReady},
		&mockClaimLoopMetrics{},
		"worker-log-fail",
		time.Second,
		logger,
		&mockClaimLoopExecutionService{
			prepareFn: func(ctx context.Context, task domain.Task) (PreparedExecutionContext, error) {
				return PreparedExecutionContext{}, domain.NewDomainError(
					domain.ErrorCodeInternal,
					"prepare failed with credentials=top-secret",
				)
			},
		},
	)

	loop.runIteration(context.Background())
	output := buffer.String()
	if !strings.Contains(output, "task.execution_context_prepare_failed") {
		t.Fatalf("expected preparation failure event, got %q", output)
	}
	if !strings.Contains(output, "diagnostic_message=") {
		t.Fatalf("expected diagnostic_message field, got %q", output)
	}
	if strings.Contains(output, "top-secret") {
		t.Fatalf("expected sensitive value to be redacted, got %q", output)
	}
}

func testClaimLoopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
