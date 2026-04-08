package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestSessionStatusIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status SessionStatus
		valid  bool
	}{
		{name: "valid", status: SessionStatusValid, valid: true},
		{name: "invalid", status: SessionStatusInvalid, valid: true},
		{name: "unavailable", status: SessionStatusUnavailable, valid: true},
		{name: "unknown", status: SessionStatus("stale"), valid: false},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := testCase.status.IsValid(); got != testCase.valid {
				t.Fatalf("expected validity %v, got %v", testCase.valid, got)
			}
		})
	}
}

func TestSessionMetadataValidate(t *testing.T) {
	t.Parallel()

	validMetadata := SessionMetadata{
		AccountID: uuid.New(),
		Revision:  1,
		Status:    SessionStatusValid,
		ObjectKey: "accounts/account-1/sessions/1.json",
	}
	if err := validMetadata.Validate(); err != nil {
		t.Fatalf("expected metadata to be valid, got %v", err)
	}

	invalidStatus := validMetadata
	invalidStatus.Status = SessionStatus("stale")
	if err := invalidStatus.Validate(); !IsDomainErrorCode(err, ErrorCodeInvalidSessionStatus) {
		t.Fatalf("expected %s, got %v", ErrorCodeInvalidSessionStatus, err)
	}

	invalidRevision := validMetadata
	invalidRevision.Revision = 0
	if err := invalidRevision.Validate(); !IsDomainErrorCode(err, ErrorCodeInvalidSessionRevision) {
		t.Fatalf("expected %s, got %v", ErrorCodeInvalidSessionRevision, err)
	}

	missingObjectKey := validMetadata
	missingObjectKey.ObjectKey = ""
	if err := missingObjectKey.Validate(); !IsDomainErrorCode(err, ErrorCodeInvalidSessionObjectKey) {
		t.Fatalf("expected %s, got %v", ErrorCodeInvalidSessionObjectKey, err)
	}

	validWithErrorCode := validMetadata
	validWithErrorCode.ErrorCode = ErrorCodeSessionPayloadCorrupted
	if err := validWithErrorCode.Validate(); !IsDomainErrorCode(err, ErrorCodeInvalidSessionStatus) {
		t.Fatalf("expected %s, got %v", ErrorCodeInvalidSessionStatus, err)
	}
}
