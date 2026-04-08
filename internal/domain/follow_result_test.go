package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestFollowVerificationResultValidate(t *testing.T) {
	t.Parallel()

	valid := FollowVerificationResult{
		Verified:          true,
		Signal:            FollowVerificationSignalFollowConfirmed,
		Reason:            "ui signal confirms followed state",
		SessionChanged:    true,
		SessionPayload:    []byte(`{"cookies":[{"name":"sid","value":"updated"}]}`),
		ScreenshotPayload: []byte("fake-png"),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid verification result, got %v", err)
	}

	invalidSignal := valid
	invalidSignal.Signal = "unexpected"
	if err := invalidSignal.Validate(); !IsDomainErrorCode(err, ErrorCodeFollowVerifyFailed) {
		t.Fatalf("expected %s, got %v", ErrorCodeFollowVerifyFailed, err)
	}

	missingErrorCode := FollowVerificationResult{
		Verified:          false,
		Signal:            FollowVerificationSignalVerifyFailed,
		Reason:            "verify failed without machine-readable code",
		ScreenshotPayload: []byte("fake-png"),
	}
	if err := missingErrorCode.Validate(); !IsDomainErrorCode(err, ErrorCodeFollowVerifyFailed) {
		t.Fatalf("expected %s, got %v", ErrorCodeFollowVerifyFailed, err)
	}

	missingSessionPayload := valid
	missingSessionPayload.SessionPayload = nil
	if err := missingSessionPayload.Validate(); !IsDomainErrorCode(err, ErrorCodeSessionPayloadMissing) {
		t.Fatalf("expected %s, got %v", ErrorCodeSessionPayloadMissing, err)
	}
}

func TestFollowResultValidate(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	result := FollowResult{
		TaskID:              taskID,
		AccountID:           accountID,
		TargetProfile:       "target-user",
		Attempt:             1,
		Outcome:             FollowFlowOutcomeCompleted,
		Verified:            true,
		VerificationSignal:  FollowVerificationSignalFollowConfirmed,
		ScreenshotObjectKey: "accounts/" + accountID.String() + "/tasks/" + taskID.String() + "/attempts/1/screenshots/follow.png",
		ArtifactObjectKeys: []string{
			"accounts/" + accountID.String() + "/tasks/" + taskID.String() + "/attempts/1/artifacts/execution.json",
		},
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("expected valid follow result, got %v", err)
	}

	result.Attempt = 0
	if err := result.Validate(); !IsDomainErrorCode(err, ErrorCodeInvalidTaskTransition) {
		t.Fatalf("expected %s, got %v", ErrorCodeInvalidTaskTransition, err)
	}
}

func TestFollowExecutionFinalizationInputValidate(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	input := FollowExecutionFinalizationInput{
		TaskID:          taskID,
		AccountID:       accountID,
		TargetProfile:   "target-user",
		Attempt:         1,
		SessionRevision: 3,
		FollowOutcome:   FollowFlowOutcomeCompleted,
		FollowDiagnostics: FollowFlowDiagnostics{
			Engine:              "mock",
			WarmupDurationMS:    3,
			ExecutionDurationMS: 7,
		},
		Verification: FollowVerificationResult{
			Verified:          true,
			Signal:            FollowVerificationSignalFollowConfirmed,
			Reason:            "verified",
			SessionChanged:    true,
			SessionPayload:    []byte(`{"cookies":[{"name":"sid","value":"updated"}]}`),
			ScreenshotPayload: []byte("fake-png"),
		},
		SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("expected valid finalization input, got %v", err)
	}

	input.SessionPayload = nil
	if err := input.Validate(); !IsDomainErrorCode(err, ErrorCodeSessionPayloadMissing) {
		t.Fatalf("expected %s, got %v", ErrorCodeSessionPayloadMissing, err)
	}

	input.SessionPayload = []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`)
	input.SessionRevision = -1
	if err := input.Validate(); !IsDomainErrorCode(err, ErrorCodeInvalidSessionRevision) {
		t.Fatalf("expected %s, got %v", ErrorCodeInvalidSessionRevision, err)
	}
}
