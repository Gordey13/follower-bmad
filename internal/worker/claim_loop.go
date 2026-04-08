package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"follower/internal/domain"
	"follower/internal/observability"

	"github.com/google/uuid"
)

type claimLoopTaskRepository interface {
	ClaimNextQueued(ctx context.Context, claimedBy string) (domain.Task, bool, error)
	Complete(
		ctx context.Context,
		taskID uuid.UUID,
		claimedBy string,
		finalStatus domain.TaskStatus,
		errorCode domain.ErrorCode,
		resultReason string,
	) (domain.Task, error)
}

type claimLoopHealthSnapshotter interface {
	Snapshot(ctx context.Context) observability.HealthStatus
}

type claimLoopMetrics interface {
	RecordClaimed()
	RecordStarted()
	RecordCompleted(status string)
	RecordError(stage string)
}

type claimLoopExtendedMetrics interface {
	RecordErrorCode(stage string, errorCode string)
	RecordExecutionOutcome(outcome string)
}

type claimLoopExecutionService interface {
	PrepareClaimedTaskContext(ctx context.Context, task domain.Task) (PreparedExecutionContext, error)
	ResolveBootstrapForClaimedTask(
		ctx context.Context,
		task domain.Task,
		prepared PreparedExecutionContext,
	) (PreparedExecutionContext, error)
	RunFollowFlow(
		ctx context.Context,
		input domain.FollowFlowInput,
	) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error)
	VerifyFollowResult(
		ctx context.Context,
		input domain.FollowVerificationInput,
	) (domain.FollowVerificationResult, error)
	FinalizeFollowExecution(
		ctx context.Context,
		input domain.FollowExecutionFinalizationInput,
	) (domain.FollowResult, error)
	ReleaseExecutionContext(ctx context.Context, accountID uuid.UUID, executionContextID string) error
}

type ClaimLoop struct {
	repository   claimLoopTaskRepository
	health       claimLoopHealthSnapshotter
	metrics      claimLoopMetrics
	execution    claimLoopExecutionService
	workerID     string
	loopInterval time.Duration
	logger       *slog.Logger
}

func NewClaimLoop(
	repository claimLoopTaskRepository,
	health claimLoopHealthSnapshotter,
	metrics claimLoopMetrics,
	workerID string,
	loopInterval time.Duration,
	logger *slog.Logger,
	executionServices ...claimLoopExecutionService,
) *ClaimLoop {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if loopInterval <= 0 {
		loopInterval = time.Second
	}

	var execution claimLoopExecutionService
	if len(executionServices) > 0 {
		execution = executionServices[0]
	}

	return &ClaimLoop{
		repository:   repository,
		health:       health,
		metrics:      metrics,
		execution:    execution,
		workerID:     workerID,
		loopInterval: loopInterval,
		logger:       logger,
	}
}

func (l *ClaimLoop) Run(ctx context.Context) {
	// Claim once immediately so the loop doesn't wait for the first tick.
	l.runIteration(ctx)

	ticker := time.NewTicker(l.loopInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.runIteration(ctx)
		}
	}
}

func (l *ClaimLoop) ClaimOnce(ctx context.Context) (domain.Task, bool, error) {
	if l.health != nil {
		snapshot := l.health.Snapshot(ctx)
		if snapshot.Status != observability.StatusReady {
			l.logger.Info("task.claim.skipped_not_ready", "component", "worker.claim_loop", "worker_id", l.workerID)
			return domain.Task{}, false, nil
		}
	}

	task, claimed, err := l.repository.ClaimNextQueued(ctx, l.workerID)
	if err != nil {
		l.recordErrorMetric("claim", claimLoopErrorCode(err))
		return domain.Task{}, false, err
	}
	if !claimed {
		return domain.Task{}, false, nil
	}

	if l.metrics != nil {
		l.metrics.RecordClaimed()
		l.metrics.RecordStarted()
	}

	l.logger.Info(
		observability.EventTaskClaimed,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.claim_loop",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: 0,
			},
			"claimed_by", task.ClaimedBy,
		)...,
	)
	l.logger.Info(
		observability.EventTaskStarted,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.claim_loop",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: 0,
			},
		)...,
	)

	return task, true, nil
}

func (l *ClaimLoop) Complete(
	ctx context.Context,
	taskID uuid.UUID,
	finalStatus domain.TaskStatus,
	errorCode domain.ErrorCode,
	resultReason string,
) (domain.Task, error) {
	task, err := l.repository.Complete(ctx, taskID, l.workerID, finalStatus, errorCode, resultReason)
	if err != nil {
		l.recordErrorMetric("complete", claimLoopErrorCode(err))
		return domain.Task{}, err
	}

	if l.metrics != nil {
		l.metrics.RecordCompleted(string(task.Status))
	}

	eventName := observability.EventTaskFailed
	if task.Status == domain.TaskStatusSuccess {
		eventName = observability.EventTaskSucceeded
	}
	eventErrorCode := string(task.ErrorCode)
	if strings.TrimSpace(eventErrorCode) == "" {
		eventErrorCode = string(domain.ErrorCodeEligible)
	}
	l.logger.Info(
		eventName,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.claim_loop",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  eventErrorCode,
				DurationMS: 0,
			},
			"status", task.Status,
		)...,
	)

	return task, nil
}

func (l *ClaimLoop) runIteration(ctx context.Context) {
	task, claimed, err := l.ClaimOnce(ctx)
	if err != nil {
		l.logger.Warn(
			observability.EventTaskClaimFailed,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     "n/a",
					AccountID:  "n/a",
					Attempt:    0,
					ErrorCode:  string(claimLoopErrorCode(err)),
					DurationMS: 0,
				},
				"task claim failed",
				"worker_id", l.workerID,
			)...,
		)
		return
	}
	if !claimed {
		return
	}

	l.executeClaimedTask(ctx, task)
}

func (l *ClaimLoop) executeClaimedTask(ctx context.Context, task domain.Task) {
	if l.execution == nil {
		l.completeAfterExecutionError(
			ctx,
			task,
			"execution_service_not_configured",
			domain.NewDomainError(domain.ErrorCodeInternal, "execution service is not configured"),
		)
		return
	}

	prepared, err := l.execution.PrepareClaimedTaskContext(ctx, task)
	if err != nil {
		l.logger.Warn(
			observability.EventTaskExecutionContextPrepareFail,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(claimLoopErrorCode(err)),
					DurationMS: 0,
				},
				"execution context preparation failed",
				"worker_id", l.workerID,
			)...,
		)
		return
	}

	executionContextID := prepared.ExecutionContextID
	if executionContextID == "" {
		executionContextID = task.ClaimedBy
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		l.releaseExecutionContext(releaseCtx, task, executionContextID)
	}()

	prepared, err = l.execution.ResolveBootstrapForClaimedTask(ctx, task, prepared)
	if err != nil {
		l.completeAfterExecutionError(
			ctx,
			task,
			"bootstrap_login_failed",
			err,
		)
		return
	}

	if !prepared.ReadyForFollowFlow {
		l.completeAfterExecutionError(
			ctx,
			task,
			"follow_prerequisites_not_ready",
			domain.NewDomainError(
				domain.ErrorCodeInternal,
				"prepared execution context is not ready for follow flow",
			),
		)
		return
	}

	followInput := domain.FollowFlowInput{
		TaskID:             task.ID,
		AccountID:          task.AccountID,
		Attempt:            task.Attempt,
		ExecutionContextID: executionContextID,
		SessionMetadata:    prepared.SessionMetadata,
		SessionPayload:     prepared.SessionPayload,
		TargetProfile:      task.TargetProfile,
	}

	outcome, diagnostics, followErr := l.runFollowStage(ctx, task, followInput)

	resolvedOutcome := normalizeFollowOutcome(outcome, followErr)
	l.recordExecutionOutcomeMetric(resolvedOutcome)
	verifyInput := domain.FollowVerificationInput{
		TaskID:             task.ID,
		AccountID:          task.AccountID,
		Attempt:            task.Attempt,
		ExecutionContextID: executionContextID,
		TargetProfile:      task.TargetProfile,
		Outcome:            resolvedOutcome,
		SessionPayload:     prepared.SessionPayload,
	}
	verification, verifyErr := l.runVerifyStage(ctx, task, verifyInput)
	if verifyErr != nil {
		verification = verificationFromError(verifyErr)
	}

	finalizeInput := domain.FollowExecutionFinalizationInput{
		TaskID:            task.ID,
		AccountID:         task.AccountID,
		TargetProfile:     task.TargetProfile,
		Attempt:           task.Attempt,
		SessionRevision:   prepared.SessionMetadata.Revision,
		FollowOutcome:     resolvedOutcome,
		FollowDiagnostics: diagnostics,
		Verification:      verification,
		SessionPayload:    sessionPayloadForFinalization(verification),
	}
	finalizeStartedAt := time.Now()
	if _, finalizeErr := l.execution.FinalizeFollowExecution(ctx, finalizeInput); finalizeErr != nil {
		l.recordErrorMetric("follow.finalize", claimLoopErrorCode(finalizeErr))
		finalStatus, errorCode, resultReason := classifyFinalizationError(finalizeErr)
		l.logger.Warn(
			observability.EventFollowFinalizeFailed,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(errorCode),
					DurationMS: time.Since(finalizeStartedAt).Milliseconds(),
				},
				resultReason,
				"worker_id", l.workerID,
				"final_status", finalStatus,
				"follow_outcome", resolvedOutcome,
				"warmup_duration_ms", diagnostics.WarmupDurationMS,
				"execution_duration_ms", diagnostics.ExecutionDurationMS,
			)...,
		)
		if _, completeErr := l.Complete(ctx, task.ID, finalStatus, errorCode, resultReason); completeErr != nil {
			l.logger.Warn(
				"task.complete_failed",
				observability.ErrorLifecycleAttrs(
					observability.LifecycleContext{
						Component:  "worker.claim_loop",
						TaskID:     task.ID.String(),
						AccountID:  task.AccountID.String(),
						Attempt:    task.Attempt,
						ErrorCode:  string(claimLoopErrorCode(completeErr)),
						DurationMS: diagnostics.ExecutionDurationMS,
					},
					"task completion after finalization failed",
					"worker_id", l.workerID,
					"final_status", finalStatus,
					"follow_outcome", resolvedOutcome,
					"warmup_duration_ms", diagnostics.WarmupDurationMS,
				)...,
			)
		}
		return
	}

	finalStatus, errorCode, resultReason := classifyFollowCompletionResult(resolvedOutcome, followErr, verification, verifyErr)
	if _, completeErr := l.Complete(ctx, task.ID, finalStatus, errorCode, resultReason); completeErr != nil {
		l.logger.Warn(
			"task.complete_failed",
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(claimLoopErrorCode(completeErr)),
					DurationMS: diagnostics.ExecutionDurationMS,
				},
				"task completion failed",
				"worker_id", l.workerID,
				"final_status", finalStatus,
				"follow_outcome", resolvedOutcome,
				"warmup_duration_ms", diagnostics.WarmupDurationMS,
			)...,
		)
	}
}

func (l *ClaimLoop) releaseExecutionContext(
	ctx context.Context,
	task domain.Task,
	executionContextID string,
) {
	if err := l.execution.ReleaseExecutionContext(ctx, task.AccountID, executionContextID); err != nil {
		l.recordErrorMetric("release", claimLoopErrorCode(err))
		l.logger.Warn(
			observability.EventExecutionContextReleaseFail,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(claimLoopErrorCode(err)),
					DurationMS: 0,
				},
				"execution context release failed",
				"worker_id", l.workerID,
			)...,
		)
	}
}

func (l *ClaimLoop) runFollowStage(
	ctx context.Context,
	task domain.Task,
	input domain.FollowFlowInput,
) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
	l.logger.Info(
		observability.EventFollowWarmupStarted,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.claim_loop",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: 0,
			},
		)...,
	)

	outcome, diagnostics, err := l.execution.RunFollowFlow(ctx, input)
	if err != nil && !diagnostics.WarmupCompleted {
		l.recordErrorMetric("follow.warmup", claimLoopErrorCode(err))
		l.logger.Warn(
			observability.EventFollowWarmupFailed,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(claimLoopErrorCode(err)),
					DurationMS: diagnostics.WarmupDurationMS,
				},
				"follow warmup failed",
			)...,
		)
		return "", diagnostics, err
	}

	l.logger.Info(
		observability.EventFollowWarmupSucceeded,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.claim_loop",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: diagnostics.WarmupDurationMS,
			},
		)...,
	)
	l.logger.Info(
		observability.EventFollowExecutionStarted,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.claim_loop",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: 0,
			},
		)...,
	)

	if err != nil {
		l.recordErrorMetric("follow.execution", claimLoopErrorCode(err))
		l.logger.Warn(
			observability.EventFollowExecutionFailed,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(claimLoopErrorCode(err)),
					DurationMS: diagnostics.ExecutionDurationMS,
				},
				"follow execution failed",
			)...,
		)
		return "", diagnostics, err
	}

	if followOutcomeIsSuccess(outcome) {
		l.logger.Info(
			observability.EventFollowExecutionSucceeded,
			observability.LifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(domain.ErrorCodeEligible),
					DurationMS: diagnostics.ExecutionDurationMS,
				},
			)...,
		)
		if outcome == domain.FollowFlowOutcomeCompleted {
			l.logger.Info(
				observability.EventFollowActionClicked,
				observability.LifecycleAttrs(
					observability.LifecycleContext{
						Component:  "worker.claim_loop",
						TaskID:     task.ID.String(),
						AccountID:  task.AccountID.String(),
						Attempt:    task.Attempt,
						ErrorCode:  string(domain.ErrorCodeEligible),
						DurationMS: diagnostics.ExecutionDurationMS,
					},
					"follow_outcome", outcome,
				)...,
			)
		}
		return outcome, diagnostics, nil
	}

	if l.metrics != nil {
		l.recordErrorMetric("follow.execution", followOutcomeErrorCode(outcome))
	}
	l.logger.Warn(
		observability.EventFollowExecutionFailed,
		observability.ErrorLifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.claim_loop",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  string(followOutcomeErrorCode(outcome)),
				DurationMS: diagnostics.ExecutionDurationMS,
			},
			"follow execution returned non-success outcome",
		)...,
	)
	return outcome, diagnostics, nil
}

func (l *ClaimLoop) runVerifyStage(
	ctx context.Context,
	task domain.Task,
	input domain.FollowVerificationInput,
) (domain.FollowVerificationResult, error) {
	startedAt := time.Now()
	l.logger.Info(
		observability.EventFollowVerifyStarted,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.claim_loop",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: 0,
			},
		)...,
	)

	verification, err := l.execution.VerifyFollowResult(ctx, input)
	if err != nil {
		l.recordErrorMetric("follow.verify", claimLoopErrorCode(err))
		l.logger.Warn(
			observability.EventFollowVerifyFailed,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(claimLoopErrorCode(err)),
					DurationMS: time.Since(startedAt).Milliseconds(),
				},
				"follow verification failed",
			)...,
		)
		return domain.FollowVerificationResult{}, err
	}

	if !verification.Verified {
		l.recordErrorMetric("follow.verify", verification.ErrorCode)
		l.logger.Warn(
			observability.EventFollowVerifyFailed,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(verification.ErrorCode),
					DurationMS: time.Since(startedAt).Milliseconds(),
				},
				"follow verification returned unverified result",
			)...,
		)
		return verification, nil
	}

	l.logger.Info(
		observability.EventFollowVerifySucceeded,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.claim_loop",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: time.Since(startedAt).Milliseconds(),
			},
		)...,
	)

	return verification, nil
}

func (l *ClaimLoop) recordErrorMetric(stage string, errorCode domain.ErrorCode) {
	if l.metrics == nil {
		return
	}

	l.metrics.RecordError(stage)

	extendedMetrics, ok := l.metrics.(claimLoopExtendedMetrics)
	if !ok {
		return
	}

	extendedMetrics.RecordErrorCode(stage, string(errorCode))
}

func (l *ClaimLoop) recordExecutionOutcomeMetric(outcome domain.FollowFlowOutcome) {
	if l.metrics == nil {
		return
	}

	extendedMetrics, ok := l.metrics.(claimLoopExtendedMetrics)
	if !ok {
		return
	}

	extendedMetrics.RecordExecutionOutcome(string(outcome))
}

func classifyFollowCompletionResult(
	outcome domain.FollowFlowOutcome,
	followErr error,
	verification domain.FollowVerificationResult,
	verifyErr error,
) (domain.TaskStatus, domain.ErrorCode, string) {
	if verifyErr != nil {
		errorCode := claimLoopErrorCode(verifyErr)
		finalStatus := finalStatusForErrorCode(errorCode)
		return finalStatus, errorCode, fmt.Sprintf(
			"follow verification failed (status=%s, error_code=%s)",
			finalStatus,
			errorCode,
		)
	}

	if !verification.Verified {
		errorCode := verification.ErrorCode
		if strings.TrimSpace(string(errorCode)) == "" {
			errorCode = followOutcomeErrorCode(outcome)
		}
		finalStatus := finalStatusForErrorCode(errorCode)
		return finalStatus, errorCode, fmt.Sprintf(
			"follow verification returned unverified outcome (status=%s, error_code=%s, signal=%s)",
			finalStatus,
			errorCode,
			verification.Signal,
		)
	}

	if followErr != nil {
		errorCode := claimLoopErrorCode(followErr)
		finalStatus := finalStatusForErrorCode(errorCode)
		return finalStatus, errorCode, fmt.Sprintf(
			"follow flow execution failed (status=%s, error_code=%s)",
			finalStatus,
			errorCode,
		)
	}

	switch outcome {
	case domain.FollowFlowOutcomeCompleted, domain.FollowFlowOutcomeAlreadyDone:
		return domain.TaskStatusSuccess, "", ""
	case domain.FollowFlowOutcomeActionUnavailable:
		return domain.TaskStatusFail, domain.ErrorCodeFollowActionUnavailable, fmt.Sprintf(
			"follow flow returned outcome (status=%s, error_code=%s, outcome=%s)",
			domain.TaskStatusFail,
			domain.ErrorCodeFollowActionUnavailable,
			outcome,
		)
	case domain.FollowFlowOutcomeTargetUnreachable:
		return domain.TaskStatusFail, domain.ErrorCodeFollowTargetUnreachable, fmt.Sprintf(
			"follow flow returned outcome (status=%s, error_code=%s, outcome=%s)",
			domain.TaskStatusFail,
			domain.ErrorCodeFollowTargetUnreachable,
			outcome,
		)
	case domain.FollowFlowOutcomeNavigationFailed:
		return domain.TaskStatusRetry, domain.ErrorCodeFollowNavigationFailed, fmt.Sprintf(
			"follow flow returned outcome (status=%s, error_code=%s, outcome=%s)",
			domain.TaskStatusRetry,
			domain.ErrorCodeFollowNavigationFailed,
			outcome,
		)
	default:
		return domain.TaskStatusRetry, domain.ErrorCodeInternal, fmt.Sprintf(
			"follow flow returned unsupported outcome (status=%s, error_code=%s, outcome=%s)",
			domain.TaskStatusRetry,
			domain.ErrorCodeInternal,
			outcome,
		)
	}
}

func classifyFinalizationError(err error) (domain.TaskStatus, domain.ErrorCode, string) {
	errorCode := claimLoopErrorCode(err)
	sourceErrorCode := sessionSaveSourceErrorCode(err)
	finalStatus := finalStatusForErrorCode(errorCode)
	if errorCode == domain.ErrorCodeSessionSaveFailed {
		finalStatus = finalStatusForSessionSaveSource(sourceErrorCode)
	}
	if strings.TrimSpace(string(sourceErrorCode)) != "" {
		return finalStatus, errorCode, fmt.Sprintf(
			"follow execution finalization failed (status=%s, error_code=%s, source_error_code=%s)",
			finalStatus,
			errorCode,
			sourceErrorCode,
		)
	}
	return finalStatus, errorCode, fmt.Sprintf(
		"follow execution finalization failed (status=%s, error_code=%s)",
		finalStatus,
		errorCode,
	)
}

func normalizeFollowOutcome(outcome domain.FollowFlowOutcome, err error) domain.FollowFlowOutcome {
	if outcome.IsValid() {
		return outcome
	}

	if err == nil {
		return domain.FollowFlowOutcomeNavigationFailed
	}

	switch claimLoopErrorCode(err) {
	case domain.ErrorCodeFollowActionUnavailable:
		return domain.FollowFlowOutcomeActionUnavailable
	case domain.ErrorCodeFollowTargetUnreachable:
		return domain.FollowFlowOutcomeTargetUnreachable
	default:
		return domain.FollowFlowOutcomeNavigationFailed
	}
}

func verificationFromError(err error) domain.FollowVerificationResult {
	errorCode := claimLoopErrorCode(err)
	return domain.FollowVerificationResult{
		Verified:          false,
		Signal:            domain.FollowVerificationSignalVerifyFailed,
		Reason:            fmt.Sprintf("follow verification error: %s", errorCode),
		ErrorCode:         errorCode,
		SessionChanged:    false,
		ScreenshotPayload: []byte("verify-screenshot:error"),
	}
}

func sessionPayloadForFinalization(verification domain.FollowVerificationResult) []byte {
	if !verification.SessionChanged {
		return nil
	}
	return append([]byte(nil), verification.SessionPayload...)
}

func finalStatusForErrorCode(errorCode domain.ErrorCode) domain.TaskStatus {
	return deterministicStatusForErrorCode(errorCode)
}

func finalStatusForSessionSaveSource(sourceErrorCode domain.ErrorCode) domain.TaskStatus {
	return deterministicStatusForSessionSaveSource(sourceErrorCode)
}

func sessionSaveSourceErrorCode(err error) domain.ErrorCode {
	var domainErr *domain.DomainError
	if !errors.As(err, &domainErr) {
		return ""
	}
	if domainErr.Code != domain.ErrorCodeSessionSaveFailed {
		return ""
	}

	const marker = "source_error_code="
	message := strings.TrimSpace(domainErr.Message)
	index := strings.Index(message, marker)
	if index < 0 {
		return ""
	}

	value := strings.TrimSpace(message[index+len(marker):])
	if value == "" {
		return ""
	}
	if stop := strings.IndexAny(value, "), \t\n\r"); stop >= 0 {
		value = value[:stop]
	}

	return domain.ErrorCode(strings.TrimSpace(value))
}

func followOutcomeIsSuccess(outcome domain.FollowFlowOutcome) bool {
	return outcome == domain.FollowFlowOutcomeCompleted ||
		outcome == domain.FollowFlowOutcomeAlreadyDone
}

func followOutcomeErrorCode(outcome domain.FollowFlowOutcome) domain.ErrorCode {
	switch outcome {
	case domain.FollowFlowOutcomeActionUnavailable:
		return domain.ErrorCodeFollowActionUnavailable
	case domain.FollowFlowOutcomeTargetUnreachable:
		return domain.ErrorCodeFollowTargetUnreachable
	case domain.FollowFlowOutcomeNavigationFailed:
		return domain.ErrorCodeFollowNavigationFailed
	default:
		return domain.ErrorCodeInternal
	}
}

func (l *ClaimLoop) completeAfterExecutionError(
	ctx context.Context,
	task domain.Task,
	resultReason string,
	err error,
) {
	finalStatus, errorCode := classifyExecutionFailure(err)
	reasonPrefix := strings.TrimSpace(resultReason)
	if reasonPrefix == "" {
		reasonPrefix = "execution_failed"
	}
	completionReason := fmt.Sprintf(
		"%s (status=%s, error_code=%s)",
		reasonPrefix,
		finalStatus,
		errorCode,
	)
	if _, completeErr := l.Complete(ctx, task.ID, finalStatus, errorCode, completionReason); completeErr != nil {
		l.logger.Warn(
			"task.complete_failed",
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.claim_loop",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(claimLoopErrorCode(completeErr)),
					DurationMS: 0,
				},
				"task completion failed after execution error",
				"worker_id", l.workerID,
				"final_status", finalStatus,
			)...,
		)
	}
}

func classifyExecutionFailure(err error) (domain.TaskStatus, domain.ErrorCode) {
	return classifyLifecycleExecutionFailure(err)
}

func claimLoopErrorCode(err error) domain.ErrorCode {
	return lifecycleErrorCode(err)
}
