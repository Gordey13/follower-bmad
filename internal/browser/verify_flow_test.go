package browser

import (
	"context"
	"errors"
	"testing"

	"follower/internal/domain"

	"github.com/google/uuid"
)

func TestMockVerifyFlowRunnerMapsFollowOutcomeToVerification(t *testing.T) {
	t.Parallel()

	runner := NewMockVerifyFlowRunner(nil, nil)
	result, err := runner.VerifyFollowResult(
		context.Background(),
		testFollowVerificationInput(domain.FollowFlowOutcomeCompleted),
	)
	if err != nil {
		t.Fatalf("VerifyFollowResult() error = %v", err)
	}
	if !result.Verified {
		t.Fatal("expected verified=true for completed outcome")
	}
	if result.Signal != domain.FollowVerificationSignalFollowConfirmed {
		t.Fatalf("expected signal %s, got %s", domain.FollowVerificationSignalFollowConfirmed, result.Signal)
	}
	if len(result.SessionPayload) == 0 {
		t.Fatal("expected session payload to be captured when session_changed=true")
	}
	if len(result.ScreenshotPayload) == 0 {
		t.Fatal("expected screenshot payload to be generated")
	}
}

func TestMockVerifyFlowRunnerMapsUnavailableOutcome(t *testing.T) {
	t.Parallel()

	runner := NewMockVerifyFlowRunner(nil, nil)
	result, err := runner.VerifyFollowResult(
		context.Background(),
		testFollowVerificationInput(domain.FollowFlowOutcomeActionUnavailable),
	)
	if err != nil {
		t.Fatalf("VerifyFollowResult() error = %v", err)
	}
	if result.Verified {
		t.Fatal("expected verified=false for action unavailable outcome")
	}
	if result.ErrorCode != domain.ErrorCodeFollowActionUnavailable {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowActionUnavailable, result.ErrorCode)
	}
}

func TestNewVerifyFlowRunnerRejectsUnsupportedEngine(t *testing.T) {
	t.Parallel()

	_, err := NewVerifyFlowRunner("selenium", nil)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidOperationalState) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeInvalidOperationalState, err)
	}
}

func TestPlaywrightVerifyFlowRunnerConfirmsCompletedOutcomeByUISignal(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightVerifyFlowRunner(nil, &stubPlaywrightVerifyAdapter{
		detection: playwrightVerifyDetection{
			State:             playwrightVerifyStateFollowConfirmed,
			ScreenshotPayload: []byte{0x89, 0x50, 0x4e, 0x47},
			Reason:            "verify ui confirms followed state",
		},
	})
	result, err := runner.VerifyFollowResult(
		context.Background(),
		testFollowVerificationInput(domain.FollowFlowOutcomeCompleted),
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !result.Verified {
		t.Fatal("expected verified=true when UI confirms follow state")
	}
	if result.Signal != domain.FollowVerificationSignalFollowConfirmed {
		t.Fatalf("expected signal %s, got %s", domain.FollowVerificationSignalFollowConfirmed, result.Signal)
	}
	if len(result.ScreenshotPayload) == 0 {
		t.Fatal("expected screenshot payload to be captured")
	}
}

func TestPlaywrightVerifyFlowRunnerReturnsVerifyFailedWhenUISignalDoesNotConfirmSuccess(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightVerifyFlowRunner(nil, &stubPlaywrightVerifyAdapter{
		detection: playwrightVerifyDetection{
			State:             playwrightVerifyStateUnknown,
			ScreenshotPayload: []byte{0x89, 0x50, 0x4e, 0x47},
			Reason:            "verify ui did not match supported follow state",
		},
	})
	result, err := runner.VerifyFollowResult(
		context.Background(),
		testFollowVerificationInput(domain.FollowFlowOutcomeAlreadyDone),
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result.Verified {
		t.Fatal("expected verified=false for unconfirmed verify UI state")
	}
	if result.Signal != domain.FollowVerificationSignalVerifyFailed {
		t.Fatalf("expected signal %s, got %s", domain.FollowVerificationSignalVerifyFailed, result.Signal)
	}
	if result.ErrorCode != domain.ErrorCodeFollowVerifyFailed {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowVerifyFailed, result.ErrorCode)
	}
}

func testFollowVerificationInput(outcome domain.FollowFlowOutcome) domain.FollowVerificationInput {
	return domain.FollowVerificationInput{
		TaskID:             uuid.New(),
		AccountID:          uuid.New(),
		Attempt:            1,
		ExecutionContextID: "worker-verify-test",
		TargetProfile:      "https://oskelly.ru/profile/100010",
		Outcome:            outcome,
		SessionPayload:     []byte(`{"cookies":[{"name":"sid","value":"seed"}]}`),
	}
}

type stubPlaywrightVerifyAdapter struct {
	detection playwrightVerifyDetection
	err       error
}

func (s *stubPlaywrightVerifyAdapter) InspectFollowState(
	ctx context.Context,
	input domain.FollowVerificationInput,
) (playwrightVerifyDetection, error) {
	if s.err != nil {
		return playwrightVerifyDetection{}, s.err
	}
	return s.detection, nil
}

func TestPlaywrightVerifyFlowRunnerPreservesActionUnavailableSignal(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightVerifyFlowRunner(nil, &stubPlaywrightVerifyAdapter{
		detection: playwrightVerifyDetection{
			State:             playwrightVerifyStateActionUnavailable,
			ScreenshotPayload: []byte{0x89, 0x50, 0x4e, 0x47},
			Reason:            "follow control is unavailable",
		},
	})
	result, err := runner.VerifyFollowResult(
		context.Background(),
		testFollowVerificationInput(domain.FollowFlowOutcomeActionUnavailable),
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result.Verified {
		t.Fatal("expected verified=false for action unavailable outcome")
	}
	if result.Signal != domain.FollowVerificationSignalActionUnavailable {
		t.Fatalf("expected signal %s, got %s", domain.FollowVerificationSignalActionUnavailable, result.Signal)
	}
	if result.ErrorCode != domain.ErrorCodeFollowActionUnavailable {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowActionUnavailable, result.ErrorCode)
	}
}

func TestPlaywrightVerifyFlowRunnerPreservesTargetUnreachableSignal(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightVerifyFlowRunner(nil, &stubPlaywrightVerifyAdapter{
		detection: playwrightVerifyDetection{
			State:             playwrightVerifyStateTargetUnreachable,
			ScreenshotPayload: []byte{0x89, 0x50, 0x4e, 0x47},
			Reason:            "target profile is unreachable",
		},
	})
	result, err := runner.VerifyFollowResult(
		context.Background(),
		testFollowVerificationInput(domain.FollowFlowOutcomeTargetUnreachable),
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result.Verified {
		t.Fatal("expected verified=false for target unreachable outcome")
	}
	if result.Signal != domain.FollowVerificationSignalTargetUnreachable {
		t.Fatalf("expected signal %s, got %s", domain.FollowVerificationSignalTargetUnreachable, result.Signal)
	}
	if result.ErrorCode != domain.ErrorCodeFollowTargetUnreachable {
		t.Fatalf("expected error code %s, got %s", domain.ErrorCodeFollowTargetUnreachable, result.ErrorCode)
	}
}

func TestPlaywrightVerifyFlowRunnerReturnsErrorWhenAdapterFails(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightVerifyFlowRunner(nil, &stubPlaywrightVerifyAdapter{
		err: errors.New("verify inspection failed"),
	})
	_, err := runner.VerifyFollowResult(
		context.Background(),
		testFollowVerificationInput(domain.FollowFlowOutcomeAlreadyDone),
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowVerifyFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowVerifyFailed, err)
	}
}

func TestPlaywrightVerifyFlowRunnerHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runner := NewPlaywrightVerifyFlowRunner(nil, &stubPlaywrightVerifyAdapter{})
	_, err := runner.VerifyFollowResult(
		ctx,
		testFollowVerificationInput(domain.FollowFlowOutcomeCompleted),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}
