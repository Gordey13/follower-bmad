package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"follower/internal/browser"
	"follower/internal/domain"
	"follower/internal/observability"

	"github.com/google/uuid"
)

type executionContextGuard interface {
	Acquire(
		ctx context.Context,
		accountID uuid.UUID,
		executionContextID string,
	) (domain.AccountWithProxy, error)
	Release(ctx context.Context, accountID uuid.UUID, executionContextID string) error
}

type executionSessionRestorer interface {
	Restore(
		ctx context.Context,
		accountID uuid.UUID,
	) (domain.SessionMetadata, []byte, error)
}

type executionTaskCompleter interface {
	Complete(
		ctx context.Context,
		taskID uuid.UUID,
		claimedBy string,
		finalStatus domain.TaskStatus,
		errorCode domain.ErrorCode,
		resultReason string,
	) (domain.Task, error)
}

type executionFollowFlowRunner interface {
	RunFollowFlow(
		ctx context.Context,
		input domain.FollowFlowInput,
	) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error)
}

type executionVerifyFlowRunner interface {
	VerifyFollowResult(
		ctx context.Context,
		input domain.FollowVerificationInput,
	) (domain.FollowVerificationResult, error)
}

type executionBootstrapRunner interface {
	RunBootstrapLogin(
		ctx context.Context,
		input domain.BootstrapLoginInput,
	) (domain.BootstrapLoginResult, error)
}

type executionResultRepository interface {
	Upsert(ctx context.Context, result domain.FollowResult) (domain.FollowResult, error)
	ListHistory(ctx context.Context, query domain.FollowResultsHistoryQuery) ([]domain.FollowResultHistoryEntry, error)
}

type executionSessionSaver interface {
	Save(ctx context.Context, accountID uuid.UUID, payload []byte) (domain.SessionMetadata, error)
}

type executionScreenshotStore interface {
	Save(
		ctx context.Context,
		accountID uuid.UUID,
		taskID uuid.UUID,
		attempt int,
		payload []byte,
	) (string, error)
	Delete(ctx context.Context, objectKey string) error
}

type executionArtifactStore interface {
	Save(
		ctx context.Context,
		accountID uuid.UUID,
		taskID uuid.UUID,
		attempt int,
		artifactName string,
		payload []byte,
	) (string, error)
	Delete(ctx context.Context, objectKey string) error
}

type executionPreparationTrace struct {
	TaskID  uuid.UUID
	Attempt int
}

type PreparedExecutionContext struct {
	AccountWithProxy   domain.AccountWithProxy
	SessionMetadata    domain.SessionMetadata
	SessionPayload     []byte
	TaskID             uuid.UUID
	Attempt            int
	ExecutionContextID string
	ReadyForFollowFlow bool
	BootstrapRequired  bool
	BootstrapReason    domain.ErrorCode
	BootstrapSource    domain.ErrorCode
}

type SessionBootstrapPolicy struct {
	BootstrapLoginEnabled         bool
	AllowMissingPayloadOnFirstRun bool
}

type ExecutionService struct {
	guard            executionContextGuard
	restorer         executionSessionRestorer
	sessionSaver     executionSessionSaver
	completer        executionTaskCompleter
	bootstrapRunner  executionBootstrapRunner
	followRunner     executionFollowFlowRunner
	verifyRunner     executionVerifyFlowRunner
	resultRepository executionResultRepository
	screenshotStore  executionScreenshotStore
	artifactStore    executionArtifactStore
	bootstrapPolicy  SessionBootstrapPolicy
	logger           *slog.Logger
}

func NewExecutionService(
	guard executionContextGuard,
	restorer executionSessionRestorer,
	logger *slog.Logger,
	taskCompleter ...executionTaskCompleter,
) *ExecutionService {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	var completer executionTaskCompleter
	if len(taskCompleter) > 0 {
		completer = taskCompleter[0]
	}

	var sessionSaver executionSessionSaver
	if saver, ok := any(restorer).(executionSessionSaver); ok {
		sessionSaver = saver
	}

	return &ExecutionService{
		guard:        guard,
		restorer:     restorer,
		sessionSaver: sessionSaver,
		completer:    completer,
		bootstrapPolicy: SessionBootstrapPolicy{
			BootstrapLoginEnabled:         false,
			AllowMissingPayloadOnFirstRun: false,
		},
		logger: logger,
	}
}

func (s *ExecutionService) WithFollowFlowRunner(runner executionFollowFlowRunner) *ExecutionService {
	s.followRunner = runner
	return s
}

func (s *ExecutionService) WithVerifyFlowRunner(runner executionVerifyFlowRunner) *ExecutionService {
	s.verifyRunner = runner
	return s
}

func (s *ExecutionService) WithResultRepository(repository executionResultRepository) *ExecutionService {
	s.resultRepository = repository
	return s
}

func (s *ExecutionService) WithScreenshotStore(store executionScreenshotStore) *ExecutionService {
	s.screenshotStore = store
	return s
}

func (s *ExecutionService) WithArtifactStore(store executionArtifactStore) *ExecutionService {
	s.artifactStore = store
	return s
}

func (s *ExecutionService) WithSessionSaver(saver executionSessionSaver) *ExecutionService {
	s.sessionSaver = saver
	return s
}

func (s *ExecutionService) WithBootstrapLoginRunner(runner executionBootstrapRunner) *ExecutionService {
	s.bootstrapRunner = runner
	return s
}

func (s *ExecutionService) WithSessionBootstrapPolicy(policy SessionBootstrapPolicy) *ExecutionService {
	s.bootstrapPolicy = policy
	return s
}

func (s *ExecutionService) PrepareExecutionContext(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
) (PreparedExecutionContext, error) {
	return s.prepareExecutionContext(
		ctx,
		accountID,
		executionContextID,
		executionPreparationTrace{},
	)
}

func (s *ExecutionService) PrepareClaimedTaskContext(
	ctx context.Context,
	task domain.Task,
) (PreparedExecutionContext, error) {
	if err := validateClaimedTaskForPreparation(task); err != nil {
		return PreparedExecutionContext{}, err
	}

	prepared, err := s.prepareExecutionContext(
		ctx,
		task.AccountID,
		task.ClaimedBy,
		executionPreparationTrace{
			TaskID:  task.ID,
			Attempt: task.Attempt,
		},
	)
	if err != nil {
		return PreparedExecutionContext{}, s.completePreparationFailure(ctx, task, err)
	}

	return prepared, nil
}

func (s *ExecutionService) ResolveBootstrapForClaimedTask(
	ctx context.Context,
	task domain.Task,
	prepared PreparedExecutionContext,
) (PreparedExecutionContext, error) {
	if !prepared.BootstrapRequired {
		return prepared, nil
	}
	if !s.bootstrapPolicy.BootstrapLoginEnabled {
		return PreparedExecutionContext{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapDisabled,
			"bootstrap login is disabled by session.bootstrap_login_enabled policy",
		)
	}
	if s.bootstrapRunner == nil {
		return PreparedExecutionContext{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"bootstrap login runner is not configured",
		)
	}

	credentialSource := domain.NormalizeCredentialSource(prepared.AccountWithProxy.Account.CredentialSource)
	credentialRef := strings.TrimSpace(prepared.AccountWithProxy.Account.CredentialRef)
	if credentialRef == "" {
		return PreparedExecutionContext{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"bootstrap credential reference is missing",
		)
	}

	startedAt := time.Now()
	s.logger.Info(
		observability.EventBootstrapLoginStarted,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.execution_service",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: 0,
			},
			"credential_source", credentialSource,
		)...,
	)

	result, err := s.bootstrapRunner.RunBootstrapLogin(ctx, domain.BootstrapLoginInput{
		TaskID:             task.ID,
		AccountID:          task.AccountID,
		Attempt:            task.Attempt,
		ExecutionContextID: prepared.ExecutionContextID,
		CredentialSource:   credentialSource,
		CredentialRef:      credentialRef,
	})
	if err != nil {
		errorCode := executionErrorCode(err)
		s.logger.Warn(
			observability.EventBootstrapLoginFailed,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.execution_service",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(errorCode),
					DurationMS: time.Since(startedAt).Milliseconds(),
				},
				"bootstrap login runner failed",
			)...,
		)
		return PreparedExecutionContext{}, err
	}
	if err := result.Validate(); err != nil {
		return PreparedExecutionContext{}, err
	}

	if result.Outcome != domain.BootstrapLoginOutcomeSuccess {
		errorCode := bootstrapErrorCodeForOutcome(result.Outcome)
		s.logger.Warn(
			observability.EventBootstrapLoginFailed,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.execution_service",
					TaskID:     task.ID.String(),
					AccountID:  task.AccountID.String(),
					Attempt:    task.Attempt,
					ErrorCode:  string(errorCode),
					DurationMS: time.Since(startedAt).Milliseconds(),
				},
				fmt.Sprintf("bootstrap login returned outcome %s", result.Outcome),
			)...,
		)
		return PreparedExecutionContext{}, domain.NewDomainError(
			errorCode,
			fmt.Sprintf("bootstrap login failed with outcome %s", result.Outcome),
		)
	}
	if s.sessionSaver == nil {
		return PreparedExecutionContext{}, domain.NewDomainError(
			domain.ErrorCodeSessionSaveFailed,
			"session saver is not configured",
		)
	}

	metadata, saveErr := s.saveSessionPayload(
		ctx,
		task.ID,
		task.AccountID,
		task.Attempt,
		result.SessionPayload,
		"bootstrap",
	)
	if saveErr != nil {
		return PreparedExecutionContext{}, saveErr
	}

	prepared.SessionMetadata = metadata
	prepared.SessionPayload = append([]byte(nil), result.SessionPayload...)
	prepared.ReadyForFollowFlow = true
	prepared.BootstrapRequired = false
	prepared.BootstrapReason = ""
	prepared.BootstrapSource = ""

	s.logger.Info(
		observability.EventBootstrapLoginSucceeded,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.execution_service",
				TaskID:     task.ID.String(),
				AccountID:  task.AccountID.String(),
				Attempt:    task.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: time.Since(startedAt).Milliseconds(),
			},
			"session_revision", metadata.Revision,
		)...,
	)

	return prepared, nil
}

func (s *ExecutionService) prepareExecutionContext(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
	trace executionPreparationTrace,
) (PreparedExecutionContext, error) {
	if accountID == uuid.Nil {
		return PreparedExecutionContext{}, domain.NewDomainError(
			domain.ErrorCodeInvalidAccountIdentifier,
			"account id must not be empty",
		)
	}
	if executionContextID == "" {
		return PreparedExecutionContext{}, domain.NewDomainError(
			domain.ErrorCodeInvalidExecutionContext,
			"execution context id must not be empty",
		)
	}

	startedAt := time.Now()

	accountWithProxy, err := s.guard.Acquire(ctx, accountID, executionContextID)
	if err != nil {
		return PreparedExecutionContext{}, err
	}

	restoreCtx := ctx
	if trace.TaskID != uuid.Nil && trace.Attempt > 0 {
		restoreCtx = observability.WithRestoreLifecycleContext(
			ctx,
			trace.TaskID.String(),
			trace.Attempt,
		)
	}

	metadata, payload, err := s.restorer.Restore(restoreCtx, accountID)
	if err != nil {
		if prepared, handled := s.bootstrapPreparationContextFromRestoreError(
			err,
			accountWithProxy,
			executionContextID,
			trace,
		); handled {
			return prepared, nil
		}

		releaseErr := s.guard.Release(ctx, accountID, executionContextID)
		if releaseErr != nil {
			err = errors.Join(
				err,
				fmt.Errorf("failed to release account after restore error: %w", releaseErr),
			)
		}

		s.logger.Warn(
			observability.EventExecutionContextPrepareFail,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "worker.execution_service",
					TaskID:     trace.TaskID.String(),
					AccountID:  accountID.String(),
					Attempt:    trace.Attempt,
					ErrorCode:  string(executionErrorCode(err)),
					DurationMS: time.Since(startedAt).Milliseconds(),
				},
				"execution context preparation failed",
				"execution_context_id", executionContextID,
			)...,
		)

		return PreparedExecutionContext{}, err
	}

	prepared := PreparedExecutionContext{
		AccountWithProxy:   accountWithProxy,
		SessionMetadata:    metadata,
		SessionPayload:     payload,
		TaskID:             trace.TaskID,
		Attempt:            trace.Attempt,
		ExecutionContextID: executionContextID,
		ReadyForFollowFlow: true,
	}

	s.logger.Info(
		observability.EventExecutionContextPrepared,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.execution_service",
				TaskID:     trace.TaskID.String(),
				AccountID:  accountID.String(),
				Attempt:    trace.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: time.Since(startedAt).Milliseconds(),
			},
			"proxy_bound", accountWithProxy.Account.ProxyID != uuid.Nil,
			"proxy_id", executionProxyIDForLog(accountWithProxy),
			"session_revision", metadata.Revision,
		)...,
	)

	return prepared, nil
}

func executionProxyIDForLog(accountWithProxy domain.AccountWithProxy) string {
	if accountWithProxy.Account.ProxyID == uuid.Nil {
		return ""
	}

	return accountWithProxy.Account.ProxyID.String()
}

func (s *ExecutionService) ReleaseExecutionContext(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
) error {
	return s.guard.Release(ctx, accountID, executionContextID)
}

func (s *ExecutionService) RunFollowFlow(
	ctx context.Context,
	input domain.FollowFlowInput,
) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
	if s.followRunner == nil {
		return "", domain.FollowFlowDiagnostics{}, domain.NewDomainError(
			domain.ErrorCodeInternal,
			"follow flow runner is not configured",
		)
	}

	return s.followRunner.RunFollowFlow(ctx, input)
}

func (s *ExecutionService) VerifyFollowResult(
	ctx context.Context,
	input domain.FollowVerificationInput,
) (domain.FollowVerificationResult, error) {
	if s.verifyRunner == nil {
		return domain.FollowVerificationResult{}, domain.NewDomainError(
			domain.ErrorCodeFollowVerifyFailed,
			"verify flow runner is not configured",
		)
	}

	result, err := s.verifyRunner.VerifyFollowResult(ctx, input)
	if err != nil {
		return domain.FollowVerificationResult{}, err
	}
	if err := result.Validate(); err != nil {
		return domain.FollowVerificationResult{}, err
	}

	return result, nil
}

func (s *ExecutionService) GetFollowResultsHistory(
	ctx context.Context,
	query domain.FollowResultsHistoryQuery,
) ([]domain.FollowResultHistoryEntry, error) {
	startedAt := time.Now()

	if err := query.Validate(); err != nil {
		return nil, err
	}
	if s.resultRepository == nil {
		return nil, domain.NewDomainError(
			domain.ErrorCodeFollowResultPersistFailed,
			"follow result repository is not configured",
		)
	}

	history, err := s.resultRepository.ListHistory(ctx, query)
	if err != nil {
		return nil, err
	}

	accountID := ""
	if query.AccountID != uuid.Nil {
		accountID = query.AccountID.String()
	}

	s.logger.Info(
		observability.EventFollowHistoryRead,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.execution_service",
				TaskID:     "n/a",
				AccountID:  accountID,
				Attempt:    0,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: time.Since(startedAt).Milliseconds(),
			},
			"target_profile", query.TargetProfile,
			"result_count", len(history),
		)...,
	)

	return history, nil
}

func (s *ExecutionService) FinalizeFollowExecution(
	ctx context.Context,
	input domain.FollowExecutionFinalizationInput,
) (domain.FollowResult, error) {
	startedAt := time.Now()

	if err := input.Validate(); err != nil {
		return domain.FollowResult{}, err
	}
	if s.screenshotStore == nil || s.artifactStore == nil {
		return domain.FollowResult{}, domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			"artifact stores are not configured",
		)
	}
	if s.resultRepository == nil {
		return domain.FollowResult{}, domain.NewDomainError(
			domain.ErrorCodeFollowResultPersistFailed,
			"follow result repository is not configured",
		)
	}

	screenshotObjectKey, artifactObjectKeys, err := s.persistFollowArtifacts(ctx, input)
	if err != nil {
		return domain.FollowResult{}, err
	}

	sessionRevision := input.SessionRevision
	if input.Verification.SessionChanged {
		metadata, saveErr := s.saveSessionPayload(
			ctx,
			input.TaskID,
			input.AccountID,
			input.Attempt,
			input.SessionPayload,
			"follow_finalization",
		)
		if saveErr != nil {
			cleanupErr := s.cleanupFollowArtifacts(ctx, screenshotObjectKey, artifactObjectKeys)
			err = saveErr
			if cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
			return domain.FollowResult{}, err
		}
		sessionRevision = metadata.Revision
	}

	result := domain.FollowResult{
		TaskID:              input.TaskID,
		AccountID:           input.AccountID,
		TargetProfile:       input.TargetProfile,
		Attempt:             input.Attempt,
		Outcome:             input.FollowOutcome,
		Verified:            input.Verification.Verified,
		VerificationSignal:  input.Verification.Signal,
		VerificationReason:  input.Verification.Reason,
		ErrorCode:           input.Verification.ErrorCode,
		ScreenshotObjectKey: screenshotObjectKey,
		ArtifactObjectKeys:  artifactObjectKeys,
		SessionRevision:     sessionRevision,
	}

	stored, err := s.resultRepository.Upsert(ctx, result)
	if err != nil {
		cleanupErr := s.cleanupFollowArtifacts(ctx, screenshotObjectKey, artifactObjectKeys)
		persistErr := domain.NewDomainError(
			domain.ErrorCodeFollowResultPersistFailed,
			fmt.Sprintf("persist follow result failed: %v", err),
		)
		var persistedErr error = persistErr
		if cleanupErr != nil {
			persistedErr = errors.Join(persistedErr, cleanupErr)
		}
		return domain.FollowResult{}, persistedErr
	}

	s.logger.Info(
		observability.EventFollowResultPersisted,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.execution_service",
				TaskID:     input.TaskID.String(),
				AccountID:  input.AccountID.String(),
				Attempt:    input.Attempt,
				ErrorCode:  string(followResultErrorCode(input.Verification.ErrorCode)),
				DurationMS: time.Since(startedAt).Milliseconds(),
			},
			"outcome", input.FollowOutcome,
			"verified", input.Verification.Verified,
			"session_revision", sessionRevision,
		)...,
	)

	return stored, nil
}

func (s *ExecutionService) completePreparationFailure(
	ctx context.Context,
	task domain.Task,
	prepareErr error,
) error {
	if s.completer == nil {
		return prepareErr
	}
	if task.ID == uuid.Nil || strings.TrimSpace(task.ClaimedBy) == "" {
		return prepareErr
	}

	finalStatus, errorCode, resultReason := mapPreparationFailureOutcome(prepareErr)
	_, completionErr := s.completer.Complete(
		ctx,
		task.ID,
		task.ClaimedBy,
		finalStatus,
		errorCode,
		resultReason,
	)
	if completionErr != nil {
		return errors.Join(
			prepareErr,
			fmt.Errorf("failed to record preparation failure outcome: %w", completionErr),
		)
	}

	return prepareErr
}

func mapPreparationFailureOutcome(err error) (domain.TaskStatus, domain.ErrorCode, string) {
	return classifyLifecyclePreparationFailure(err)
}

func bootstrapErrorCodeForOutcome(outcome domain.BootstrapLoginOutcome) domain.ErrorCode {
	switch outcome {
	case domain.BootstrapLoginOutcomeAuthInvalidCredentials:
		return domain.ErrorCodeAuthInvalidCredentials
	case domain.BootstrapLoginOutcomeAuthChallenge:
		return domain.ErrorCodeAuthChallengeBlocked
	case domain.BootstrapLoginOutcomeAuthRuntimeError:
		return domain.ErrorCodeAuthBootstrapFailed
	default:
		return domain.ErrorCodeAuthBootstrapFailed
	}
}

func (s *ExecutionService) bootstrapPreparationContextFromRestoreError(
	restoreErr error,
	accountWithProxy domain.AccountWithProxy,
	executionContextID string,
	trace executionPreparationTrace,
) (PreparedExecutionContext, bool) {
	bootstrapReason, bootstrapCandidate := browser.BootstrapReasonForRestoreError(restoreErr)
	if !bootstrapCandidate {
		return PreparedExecutionContext{}, false
	}

	sourceCode := executionErrorCode(restoreErr)
	if !s.shouldCreateBootstrapDecisionForSource(sourceCode) {
		return PreparedExecutionContext{}, false
	}
	if strings.TrimSpace(string(bootstrapReason)) == "" {
		bootstrapReason = domain.ErrorCodeAuthBootstrapRequired
	}

	s.logger.Info(
		observability.EventExecutionContextPrepared,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.execution_service",
				TaskID:     trace.TaskID.String(),
				AccountID:  accountWithProxy.Account.ID.String(),
				Attempt:    trace.Attempt,
				ErrorCode:  string(bootstrapReason),
				DurationMS: 0,
			},
			"execution_context_id", executionContextID,
			"bootstrap_required", true,
			"bootstrap_reason", bootstrapReason,
			"bootstrap_source_error_code", sourceCode,
		)...,
	)

	return PreparedExecutionContext{
		AccountWithProxy:   accountWithProxy,
		TaskID:             trace.TaskID,
		Attempt:            trace.Attempt,
		ExecutionContextID: executionContextID,
		ReadyForFollowFlow: false,
		BootstrapRequired:  true,
		BootstrapReason:    bootstrapReason,
		BootstrapSource:    sourceCode,
	}, true
}

func (s *ExecutionService) shouldCreateBootstrapDecisionForSource(sourceCode domain.ErrorCode) bool {
	switch sourceCode {
	case domain.ErrorCodeSessionMetadataNotFound:
		return true
	case domain.ErrorCodeSessionPayloadMissing:
		return s.bootstrapPolicy.AllowMissingPayloadOnFirstRun
	default:
		return false
	}
}

func validateClaimedTaskForPreparation(task domain.Task) error {
	if task.ID == uuid.Nil {
		return domain.NewDomainError(
			domain.ErrorCodeInvalidTaskIdentifier,
			"claimed task id must not be empty",
		)
	}
	if task.AccountID == uuid.Nil {
		return domain.NewDomainError(
			domain.ErrorCodeInvalidAccountIdentifier,
			"claimed task account id must not be empty",
		)
	}
	if task.Status != domain.TaskStatusRunning {
		return domain.NewDomainError(
			domain.ErrorCodeTaskNotRunning,
			fmt.Sprintf("claimed task must be in running status, got %s", task.Status),
		)
	}
	if strings.TrimSpace(task.ClaimedBy) == "" {
		return domain.NewDomainError(
			domain.ErrorCodeInvalidTaskClaimedBy,
			"claimed task claimed_by must not be empty",
		)
	}

	return nil
}

func (s *ExecutionService) persistFollowArtifacts(
	ctx context.Context,
	input domain.FollowExecutionFinalizationInput,
) (string, []string, error) {
	screenshotPayload, err := normalizeScreenshotPayload(input.Verification.ScreenshotPayload)
	if err != nil {
		return "", nil, domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			fmt.Sprintf("normalize screenshot payload failed: %v", err),
		)
	}

	screenshotStartedAt := time.Now()
	screenshotObjectKey, err := s.screenshotStore.Save(
		ctx,
		input.AccountID,
		input.TaskID,
		input.Attempt,
		screenshotPayload,
	)
	if err != nil {
		return "", nil, domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			fmt.Sprintf("persist screenshot failed: %v", err),
		)
	}

	s.logger.Info(
		observability.EventArtifactSaved,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.execution_service",
				TaskID:     input.TaskID.String(),
				AccountID:  input.AccountID.String(),
				Attempt:    input.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: time.Since(screenshotStartedAt).Milliseconds(),
			},
			"artifact_type", "screenshot",
			"object_key", screenshotObjectKey,
		)...,
	)

	artifactPayload, err := json.Marshal(struct {
		Diagnostics  domain.FollowFlowDiagnostics    `json:"diagnostics"`
		Verification domain.FollowVerificationResult `json:"verification"`
	}{
		Diagnostics:  input.FollowDiagnostics,
		Verification: input.Verification,
	})
	if err != nil {
		cleanupErr := s.screenshotStore.Delete(ctx, screenshotObjectKey)
		artifactErr := domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			fmt.Sprintf("serialize execution artifact payload failed: %v", err),
		)
		var combinedErr error = artifactErr
		if cleanupErr != nil {
			combinedErr = errors.Join(combinedErr, cleanupErr)
		}
		return "", nil, combinedErr
	}

	artifactStartedAt := time.Now()
	artifactObjectKey, err := s.artifactStore.Save(
		ctx,
		input.AccountID,
		input.TaskID,
		input.Attempt,
		"execution.json",
		artifactPayload,
	)
	if err != nil {
		cleanupErr := s.screenshotStore.Delete(ctx, screenshotObjectKey)
		artifactErr := domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			fmt.Sprintf("persist execution artifact failed: %v", err),
		)
		var combinedErr error = artifactErr
		if cleanupErr != nil {
			combinedErr = errors.Join(combinedErr, cleanupErr)
		}
		return "", nil, combinedErr
	}

	s.logger.Info(
		observability.EventArtifactSaved,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.execution_service",
				TaskID:     input.TaskID.String(),
				AccountID:  input.AccountID.String(),
				Attempt:    input.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: time.Since(artifactStartedAt).Milliseconds(),
			},
			"artifact_type", "execution",
			"object_key", artifactObjectKey,
		)...,
	)

	return screenshotObjectKey, []string{artifactObjectKey}, nil
}

func (s *ExecutionService) cleanupFollowArtifacts(
	ctx context.Context,
	screenshotObjectKey string,
	artifactObjectKeys []string,
) error {
	var cleanupErr error

	if strings.TrimSpace(screenshotObjectKey) != "" {
		if err := s.screenshotStore.Delete(ctx, screenshotObjectKey); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	for _, objectKey := range artifactObjectKeys {
		if strings.TrimSpace(objectKey) == "" {
			continue
		}
		if err := s.artifactStore.Delete(ctx, objectKey); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}

	return cleanupErr
}

func executionErrorCode(err error) domain.ErrorCode {
	return lifecycleErrorCode(err)
}

func followResultErrorCode(errorCode domain.ErrorCode) domain.ErrorCode {
	if strings.TrimSpace(string(errorCode)) == "" {
		return domain.ErrorCodeEligible
	}
	return errorCode
}

func (s *ExecutionService) saveSessionPayload(
	ctx context.Context,
	taskID uuid.UUID,
	accountID uuid.UUID,
	attempt int,
	payload []byte,
	saveSource string,
) (domain.SessionMetadata, error) {
	if s.sessionSaver == nil {
		return domain.SessionMetadata{}, newSessionSaveFailedError(
			"session saver is not configured",
			domain.ErrorCodeInternal,
		)
	}

	startedAt := time.Now()
	metadata, err := s.sessionSaver.Save(ctx, accountID, payload)
	if err != nil {
		return domain.SessionMetadata{}, newSessionSaveFailedError(
			"session payload persistence failed",
			executionErrorCode(err),
		)
	}

	s.logger.Info(
		observability.EventSessionSaved,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "worker.execution_service",
				TaskID:     taskID.String(),
				AccountID:  accountID.String(),
				Attempt:    attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: time.Since(startedAt).Milliseconds(),
			},
			"session_revision", metadata.Revision,
			"object_key", metadata.ObjectKey,
			"save_source", strings.TrimSpace(saveSource),
		)...,
	)

	return metadata, nil
}

func newSessionSaveFailedError(message string, sourceCode domain.ErrorCode) error {
	if strings.TrimSpace(string(sourceCode)) == "" {
		sourceCode = domain.ErrorCodeInternal
	}

	return domain.NewDomainError(
		domain.ErrorCodeSessionSaveFailed,
		fmt.Sprintf("%s (source_error_code=%s)", strings.TrimSpace(message), sourceCode),
	)
}
