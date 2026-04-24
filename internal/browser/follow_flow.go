package browser

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"follower/internal/domain"
	"follower/internal/stackerr"
)

type FollowFlowRunner interface {
	RunFollowFlow(
		ctx context.Context,
		input domain.FollowFlowInput,
	) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error)
}

func NewFollowFlowRunner(
	engine string,
	logger *slog.Logger,
) (FollowFlowRunner, error) {
	switch engine {
	case "mock":
		return NewMockFollowFlowRunner(nil, nil, logger), nil
	case "playwright":
		return NewPlaywrightFollowFlowRunner(logger), nil
	default:
		return nil, domain.NewDomainError(
			domain.ErrorCodeInvalidOperationalState,
			fmt.Sprintf("unsupported browser engine: %s", engine),
		)
	}
}

type MockFollowFlowRunner struct {
	outcomesByTarget map[domain.TargetProfileDescriptor]domain.FollowFlowOutcome
	errorsByTarget   map[domain.TargetProfileDescriptor]error
	logger           *slog.Logger
}

func NewMockFollowFlowRunner(
	outcomesByTarget map[domain.TargetProfileDescriptor]domain.FollowFlowOutcome,
	errorsByTarget map[domain.TargetProfileDescriptor]error,
	logger *slog.Logger,
) *MockFollowFlowRunner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &MockFollowFlowRunner{
		outcomesByTarget: cloneOutcomeMap(outcomesByTarget),
		errorsByTarget:   cloneErrorMap(errorsByTarget),
		logger:           logger,
	}
}

func (r *MockFollowFlowRunner) RunFollowFlow(
	ctx context.Context,
	input domain.FollowFlowInput,
) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
	return runFollowFlow(
		ctx,
		input,
		"mock",
		runMockWarmupFlow,
		r.runFollowAction,
		r.logger,
	)
}

func (r *MockFollowFlowRunner) runFollowAction(
	ctx context.Context,
	input domain.FollowFlowInput,
) (domain.FollowFlowOutcome, error) {
	if err := ctx.Err(); err != nil {
		return "", stackerr.WithStack(err)
	}

	if followErr, ok := r.errorsByTarget[input.TargetProfile]; ok && followErr != nil {
		return "", followErr
	}

	if outcome, ok := r.outcomesByTarget[input.TargetProfile]; ok {
		return outcome, nil
	}

	return domain.FollowFlowOutcomeCompleted, nil
}

type PlaywrightFollowFlowRunner struct {
	logger  *slog.Logger
	adapter playwrightFollowAdapter
}

func NewPlaywrightFollowFlowRunner(
	logger *slog.Logger,
	adapter ...playwrightFollowAdapter,
) *PlaywrightFollowFlowRunner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	selectedAdapter := playwrightFollowAdapter(&defaultPlaywrightFollowAdapter{})
	if len(adapter) > 0 && adapter[0] != nil {
		selectedAdapter = adapter[0]
	}
	return &PlaywrightFollowFlowRunner{
		logger:  logger,
		adapter: selectedAdapter,
	}
}

func (r *PlaywrightFollowFlowRunner) RunFollowFlow(
	ctx context.Context,
	input domain.FollowFlowInput,
) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
	return runFollowFlow(
		ctx,
		input,
		"playwright",
		r.runWarmup,
		r.runFollowAction,
		r.logger,
	)
}

func (r *PlaywrightFollowFlowRunner) runWarmup(
	ctx context.Context,
	input domain.FollowFlowInput,
) error {
	return runPlaywrightWarmupFlow(ctx, input, r.adapter)
}

func (r *PlaywrightFollowFlowRunner) runFollowAction(
	ctx context.Context,
	input domain.FollowFlowInput,
) (domain.FollowFlowOutcome, error) {
	if err := ctx.Err(); err != nil {
		return "", stackerr.WithStack(err)
	}
	if err := input.TargetProfile.Validate(); err != nil {
		return "", stackerr.WithStack(err)
	}
	if r.adapter == nil {
		return "", domain.NewDomainError(
			domain.ErrorCodeFollowNavigationFailed,
			"playwright follow adapter is not configured",
		)
	}

	outcome, err := r.adapter.RunFollowAction(ctx, input)
	if err != nil {
		return "", stackerr.WithStack(normalizePlaywrightFollowError(err))
	}
	if !outcome.IsValid() {
		return "", domain.NewDomainError(
			domain.ErrorCodeFollowNavigationFailed,
			"playwright follow action returned invalid outcome",
		)
	}

	return outcome, nil
}

type warmupStep func(ctx context.Context, input domain.FollowFlowInput) error
type followStep func(ctx context.Context, input domain.FollowFlowInput) (domain.FollowFlowOutcome, error)

func runFollowFlow(
	ctx context.Context,
	input domain.FollowFlowInput,
	engine string,
	warmup warmupStep,
	follow followStep,
	logger *slog.Logger,
) (domain.FollowFlowOutcome, domain.FollowFlowDiagnostics, error) {
	if err := input.Validate(); err != nil {
		return "", domain.FollowFlowDiagnostics{Engine: engine}, stackerr.WithStack(err)
	}

	diagnostics := domain.FollowFlowDiagnostics{Engine: engine}

	warmupStarted := time.Now()
	if err := normalizeWarmupError(warmup(ctx, input)); err != nil {
		diagnostics.WarmupDurationMS = time.Since(warmupStarted).Milliseconds()
		return "", diagnostics, stackerr.WithStack(err)
	}
	diagnostics.WarmupCompleted = true
	diagnostics.WarmupDurationMS = time.Since(warmupStarted).Milliseconds()

	executionStarted := time.Now()
	outcome, err := follow(ctx, input)
	diagnostics.ExecutionDurationMS = time.Since(executionStarted).Milliseconds()
	if err != nil {
		return "", diagnostics, stackerr.WithStack(err)
	}
	if !outcome.IsValid() {
		return "", diagnostics, domain.NewDomainError(
			domain.ErrorCodeInternal,
			fmt.Sprintf("invalid follow flow outcome: %s", outcome),
		)
	}

	if logger != nil {
		logger.Debug("follow.flow.finished",
			"component", "browser.follow_flow",
			"task_id", input.TaskID.String(),
			"account_id", input.AccountID.String(),
			"attempt", input.Attempt,
			"outcome", outcome,
			"engine", engine,
		)
	}

	return outcome, diagnostics, nil
}

func cloneOutcomeMap(
	input map[domain.TargetProfileDescriptor]domain.FollowFlowOutcome,
) map[domain.TargetProfileDescriptor]domain.FollowFlowOutcome {
	if len(input) == 0 {
		return map[domain.TargetProfileDescriptor]domain.FollowFlowOutcome{}
	}
	clone := make(map[domain.TargetProfileDescriptor]domain.FollowFlowOutcome, len(input))
	for key, value := range input {
		clone[key] = value
	}
	return clone
}

func cloneErrorMap(
	input map[domain.TargetProfileDescriptor]error,
) map[domain.TargetProfileDescriptor]error {
	if len(input) == 0 {
		return map[domain.TargetProfileDescriptor]error{}
	}
	clone := make(map[domain.TargetProfileDescriptor]error, len(input))
	for key, value := range input {
		clone[key] = value
	}
	return clone
}
