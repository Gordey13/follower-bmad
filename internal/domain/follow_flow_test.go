package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestTargetProfileDescriptorValidate(t *testing.T) {
	t.Parallel()

	if err := TargetProfileDescriptor("target-user").Validate(); err != nil {
		t.Fatalf("expected valid descriptor, got %v", err)
	}

	if err := TargetProfileDescriptor("").Validate(); !IsDomainErrorCode(err, ErrorCodeFollowTargetProfile) {
		t.Fatalf("expected %s, got %v", ErrorCodeFollowTargetProfile, err)
	}
}

func TestFollowFlowInputValidate(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	input := FollowFlowInput{
		TaskID:             uuid.New(),
		AccountID:          accountID,
		Attempt:            1,
		ExecutionContextID: "worker-follow-test",
		SessionMetadata: SessionMetadata{
			AccountID: accountID,
			Revision:  1,
			Status:    SessionStatusValid,
			ObjectKey: "accounts/" + accountID.String() + "/sessions/1.json",
		},
		SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		TargetProfile:  "target-user",
	}

	if err := input.Validate(); err != nil {
		t.Fatalf("expected valid input, got %v", err)
	}

	invalidInput := input
	invalidInput.TargetProfile = ""
	if err := invalidInput.Validate(); !IsDomainErrorCode(err, ErrorCodeFollowTargetProfile) {
		t.Fatalf("expected %s, got %v", ErrorCodeFollowTargetProfile, err)
	}
}
