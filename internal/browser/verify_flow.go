package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"follower/internal/domain"
	"follower/internal/stackerr"
)

type VerifyFlowRunner interface {
	VerifyFollowResult(
		ctx context.Context,
		input domain.FollowVerificationInput,
	) (domain.FollowVerificationResult, error)
}

func NewVerifyFlowRunner(engine string, logger *slog.Logger) (VerifyFlowRunner, error) {
	switch engine {
	case "mock":
		return NewMockVerifyFlowRunner(nil, logger), nil
	case "playwright":
		return NewPlaywrightVerifyFlowRunner(logger), nil
	default:
		return nil, domain.NewDomainError(
			domain.ErrorCodeInvalidOperationalState,
			fmt.Sprintf("unsupported browser engine for verify runner: %s", engine),
		)
	}
}

type MockVerifyFlowRunner struct {
	overrides map[domain.FollowFlowOutcome]domain.FollowVerificationResult
	logger    *slog.Logger
}

func NewMockVerifyFlowRunner(
	overrides map[domain.FollowFlowOutcome]domain.FollowVerificationResult,
	logger *slog.Logger,
) *MockVerifyFlowRunner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	clone := map[domain.FollowFlowOutcome]domain.FollowVerificationResult{}
	for outcome, result := range overrides {
		clone[outcome] = result
	}

	return &MockVerifyFlowRunner{
		overrides: clone,
		logger:    logger,
	}
}

func (r *MockVerifyFlowRunner) VerifyFollowResult(
	ctx context.Context,
	input domain.FollowVerificationInput,
) (domain.FollowVerificationResult, error) {
	return runVerifyFlow(ctx, input, "mock", r.overrides, r.logger)
}

type PlaywrightVerifyFlowRunner struct {
	logger  *slog.Logger
	adapter playwrightVerifyAdapter
}

type playwrightVerifyAdapter interface {
	InspectFollowState(
		ctx context.Context,
		input domain.FollowVerificationInput,
	) (playwrightVerifyDetection, error)
}

type playwrightVerifyState string

const (
	playwrightVerifyStateFollowConfirmed   playwrightVerifyState = "follow_confirmed"
	playwrightVerifyStateActionUnavailable playwrightVerifyState = "action_unavailable"
	playwrightVerifyStateTargetUnreachable playwrightVerifyState = "target_unreachable"
	playwrightVerifyStateAuthRequired      playwrightVerifyState = "auth_required"
	playwrightVerifyStateUnknown           playwrightVerifyState = "unknown"
)

type playwrightVerifyDetection struct {
	State             playwrightVerifyState
	ScreenshotPayload []byte
	Reason            string
}

func NewPlaywrightVerifyFlowRunner(
	logger *slog.Logger,
	adapter ...playwrightVerifyAdapter,
) *PlaywrightVerifyFlowRunner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	var selectedAdapter playwrightVerifyAdapter = &defaultPlaywrightVerifyAdapter{}
	if len(adapter) > 0 && adapter[0] != nil {
		selectedAdapter = adapter[0]
	}
	return &PlaywrightVerifyFlowRunner{
		logger:  logger,
		adapter: selectedAdapter,
	}
}

func (r *PlaywrightVerifyFlowRunner) VerifyFollowResult(
	ctx context.Context,
	input domain.FollowVerificationInput,
) (domain.FollowVerificationResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.FollowVerificationResult{}, stackerr.WithStack(err)
	}
	if err := input.Validate(); err != nil {
		return domain.FollowVerificationResult{}, stackerr.WithStack(err)
	}
	if r.adapter == nil {
		return domain.FollowVerificationResult{}, domain.NewDomainError(
			domain.ErrorCodeFollowVerifyFailed,
			"playwright verify adapter is not configured",
		)
	}

	detection, err := r.adapter.InspectFollowState(ctx, input)
	if err != nil {
		return domain.FollowVerificationResult{}, stackerr.WithStack(normalizePlaywrightVerifyError(err))
	}
	detection.ScreenshotPayload = ensureVerifyScreenshotPayload(detection.ScreenshotPayload, input.Outcome)

	result := mapPlaywrightVerificationResult(input, detection)
	if err := result.Validate(); err != nil {
		return domain.FollowVerificationResult{}, stackerr.WithStack(err)
	}

	if r.logger != nil {
		r.logger.Debug("follow.verify.finished",
			"component", "browser.verify_flow",
			"task_id", input.TaskID.String(),
			"account_id", input.AccountID.String(),
			"attempt", input.Attempt,
			"outcome", input.Outcome,
			"verified", result.Verified,
			"signal", result.Signal,
			"engine", "playwright",
		)
	}

	return result, nil
}

func mapPlaywrightVerificationResult(
	input domain.FollowVerificationInput,
	detection playwrightVerifyDetection,
) domain.FollowVerificationResult {
	reason := normalizeVerifyReason(detection.Reason)
	if detection.State == playwrightVerifyStateAuthRequired {
		return verifyAuthRequiredResult(reason, detection.ScreenshotPayload)
	}

	switch input.Outcome {
	case domain.FollowFlowOutcomeCompleted:
		if detection.State == playwrightVerifyStateFollowConfirmed {
			return domain.FollowVerificationResult{
				Verified:          true,
				Signal:            domain.FollowVerificationSignalFollowConfirmed,
				Reason:            reason,
				SessionChanged:    true,
				SessionPayload:    clonePayload(input.SessionPayload),
				ScreenshotPayload: detection.ScreenshotPayload,
			}
		}
		return verifyFailedResult(reason, detection.ScreenshotPayload)
	case domain.FollowFlowOutcomeAlreadyDone:
		if detection.State == playwrightVerifyStateFollowConfirmed {
			return domain.FollowVerificationResult{
				Verified:          true,
				Signal:            domain.FollowVerificationSignalAlreadyDone,
				Reason:            reason,
				SessionChanged:    false,
				ScreenshotPayload: detection.ScreenshotPayload,
			}
		}
		return verifyFailedResult(reason, detection.ScreenshotPayload)
	case domain.FollowFlowOutcomeActionUnavailable:
		return domain.FollowVerificationResult{
			Verified:          false,
			Signal:            domain.FollowVerificationSignalActionUnavailable,
			Reason:            reason,
			ErrorCode:         domain.ErrorCodeFollowActionUnavailable,
			SessionChanged:    false,
			ScreenshotPayload: detection.ScreenshotPayload,
		}
	case domain.FollowFlowOutcomeTargetUnreachable:
		return domain.FollowVerificationResult{
			Verified:          false,
			Signal:            domain.FollowVerificationSignalTargetUnreachable,
			Reason:            reason,
			ErrorCode:         domain.ErrorCodeFollowTargetUnreachable,
			SessionChanged:    false,
			ScreenshotPayload: detection.ScreenshotPayload,
		}
	default:
		if detection.State == playwrightVerifyStateTargetUnreachable {
			return domain.FollowVerificationResult{
				Verified:          false,
				Signal:            domain.FollowVerificationSignalTargetUnreachable,
				Reason:            reason,
				ErrorCode:         domain.ErrorCodeFollowTargetUnreachable,
				SessionChanged:    false,
				ScreenshotPayload: detection.ScreenshotPayload,
			}
		}
		if detection.State == playwrightVerifyStateActionUnavailable {
			return domain.FollowVerificationResult{
				Verified:          false,
				Signal:            domain.FollowVerificationSignalActionUnavailable,
				Reason:            reason,
				ErrorCode:         domain.ErrorCodeFollowActionUnavailable,
				SessionChanged:    false,
				ScreenshotPayload: detection.ScreenshotPayload,
			}
		}
		return verifyFailedResult(reason, detection.ScreenshotPayload)
	}
}

func verifyAuthRequiredResult(reason string, screenshotPayload []byte) domain.FollowVerificationResult {
	return domain.FollowVerificationResult{
		Verified:          false,
		Signal:            domain.FollowVerificationSignalVerifyFailed,
		Reason:            reason,
		ErrorCode:         domain.ErrorCodeAuthBootstrapRequired,
		SessionChanged:    false,
		ScreenshotPayload: screenshotPayload,
	}
}

func verifyFailedResult(reason string, screenshotPayload []byte) domain.FollowVerificationResult {
	return domain.FollowVerificationResult{
		Verified:          false,
		Signal:            domain.FollowVerificationSignalVerifyFailed,
		Reason:            reason,
		ErrorCode:         domain.ErrorCodeFollowVerifyFailed,
		SessionChanged:    false,
		ScreenshotPayload: screenshotPayload,
	}
}

func ensureVerifyScreenshotPayload(
	payload []byte,
	outcome domain.FollowFlowOutcome,
) []byte {
	if len(payload) > 0 {
		return append([]byte(nil), payload...)
	}
	return []byte("verify-screenshot:" + string(outcome))
}

func normalizeVerifyReason(reason string) string {
	normalized := strings.TrimSpace(reason)
	if normalized == "" {
		return "verify UI could not determine a supported follow state"
	}
	return normalized
}

func normalizePlaywrightVerifyError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return domain.NewDomainError(
			domain.ErrorCodeFollowVerifyFailed,
			"playwright verify was interrupted by context cancellation/timeout",
		)
	}

	var domainErr *domain.DomainError
	if errors.As(err, &domainErr) {
		switch domainErr.Code {
		case domain.ErrorCodeFollowVerifyFailed,
			domain.ErrorCodeFollowTargetProfile,
			domain.ErrorCodeSessionPayloadInvalid,
			domain.ErrorCodeAuthBootstrapRequired:
			return domainErr
		}
	}

	return domain.NewDomainError(
		domain.ErrorCodeFollowVerifyFailed,
		"playwright verify runtime failure",
	)
}

func runVerifyFlow(
	ctx context.Context,
	input domain.FollowVerificationInput,
	engine string,
	overrides map[domain.FollowFlowOutcome]domain.FollowVerificationResult,
	logger *slog.Logger,
) (domain.FollowVerificationResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.FollowVerificationResult{}, stackerr.WithStack(err)
	}
	if err := input.Validate(); err != nil {
		return domain.FollowVerificationResult{}, stackerr.WithStack(err)
	}

	if override, ok := overrides[input.Outcome]; ok {
		if len(override.ScreenshotPayload) == 0 {
			override.ScreenshotPayload = []byte("verify-screenshot:" + input.TaskID.String())
		}
		if override.SessionChanged && len(override.SessionPayload) == 0 {
			override.SessionPayload = clonePayload(input.SessionPayload)
		}
		if err := override.Validate(); err != nil {
			return domain.FollowVerificationResult{}, stackerr.WithStack(err)
		}
		return override, nil
	}

	result := defaultVerificationResult(input)
	if err := result.Validate(); err != nil {
		return domain.FollowVerificationResult{}, stackerr.WithStack(err)
	}

	if logger != nil {
		logger.Debug("follow.verify.finished",
			"component", "browser.verify_flow",
			"task_id", input.TaskID.String(),
			"account_id", input.AccountID.String(),
			"attempt", input.Attempt,
			"outcome", input.Outcome,
			"verified", result.Verified,
			"signal", result.Signal,
			"engine", engine,
		)
	}

	return result, nil
}

func defaultVerificationResult(input domain.FollowVerificationInput) domain.FollowVerificationResult {
	switch input.Outcome {
	case domain.FollowFlowOutcomeCompleted:
		return domain.FollowVerificationResult{
			Verified:          true,
			Signal:            domain.FollowVerificationSignalFollowConfirmed,
			Reason:            "target profile reflects follow success",
			SessionChanged:    true,
			SessionPayload:    clonePayload(input.SessionPayload),
			ScreenshotPayload: []byte("verify-screenshot:completed"),
		}
	case domain.FollowFlowOutcomeAlreadyDone:
		return domain.FollowVerificationResult{
			Verified:          true,
			Signal:            domain.FollowVerificationSignalAlreadyDone,
			Reason:            "target profile already in followed state",
			SessionChanged:    false,
			ScreenshotPayload: []byte("verify-screenshot:already-done"),
		}
	case domain.FollowFlowOutcomeActionUnavailable:
		return domain.FollowVerificationResult{
			Verified:          false,
			Signal:            domain.FollowVerificationSignalActionUnavailable,
			Reason:            "follow action is unavailable for target profile",
			ErrorCode:         domain.ErrorCodeFollowActionUnavailable,
			SessionChanged:    false,
			ScreenshotPayload: []byte("verify-screenshot:action-unavailable"),
		}
	case domain.FollowFlowOutcomeTargetUnreachable:
		return domain.FollowVerificationResult{
			Verified:          false,
			Signal:            domain.FollowVerificationSignalTargetUnreachable,
			Reason:            "target profile is unreachable",
			ErrorCode:         domain.ErrorCodeFollowTargetUnreachable,
			SessionChanged:    false,
			ScreenshotPayload: []byte("verify-screenshot:target-unreachable"),
		}
	default:
		return domain.FollowVerificationResult{
			Verified:          false,
			Signal:            domain.FollowVerificationSignalNavigationFailed,
			Reason:            "follow verification failed due to navigation/runtime error",
			ErrorCode:         domain.ErrorCodeFollowNavigationFailed,
			SessionChanged:    false,
			ScreenshotPayload: []byte("verify-screenshot:navigation-failed"),
		}
	}
}

func clonePayload(payload []byte) []byte {
	if len(payload) == 0 {
		return nil
	}
	return append([]byte(nil), payload...)
}
