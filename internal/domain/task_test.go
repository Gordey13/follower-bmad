package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestTaskStatusValidationAndTransitions(t *testing.T) {
	t.Parallel()

	if !TaskStatusQueued.IsValid() || !TaskStatusRunning.IsValid() || !TaskStatusSuccess.IsValid() || !TaskStatusRetry.IsValid() || !TaskStatusFail.IsValid() {
		t.Fatal("expected all documented task statuses to be valid")
	}
	if TaskStatus("unknown").IsValid() {
		t.Fatal("expected unknown task status to be invalid")
	}

	if !TaskStatusQueued.CanTransitionTo(TaskStatusRunning) {
		t.Fatal("expected queued -> running to be allowed")
	}
	if !TaskStatusRunning.CanTransitionTo(TaskStatusSuccess) {
		t.Fatal("expected running -> success to be allowed")
	}
	if !TaskStatusRunning.CanTransitionTo(TaskStatusRetry) {
		t.Fatal("expected running -> retry to be allowed")
	}
	if !TaskStatusRunning.CanTransitionTo(TaskStatusFail) {
		t.Fatal("expected running -> fail to be allowed")
	}
	if TaskStatusRunning.CanTransitionTo(TaskStatusQueued) {
		t.Fatal("expected running -> queued to be rejected")
	}
	if TaskStatusSuccess.CanTransitionTo(TaskStatusRunning) {
		t.Fatal("expected terminal -> running transition to be rejected")
	}
}

func TestTaskValidate(t *testing.T) {
	t.Parallel()

	valid := Task{
		ID:            uuid.New(),
		AccountID:     uuid.New(),
		TargetProfile: "target-validation-user",
		Status:        TaskStatusQueued,
		Attempt:       0,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid task, got error = %v", err)
	}

	invalidID := valid
	invalidID.ID = uuid.Nil
	if err := invalidID.Validate(); !IsDomainErrorCode(err, ErrorCodeInvalidTaskIdentifier) {
		t.Fatalf("expected %s, got %v", ErrorCodeInvalidTaskIdentifier, err)
	}

	invalidStatus := valid
	invalidStatus.Status = TaskStatus("scheduled")
	if err := invalidStatus.Validate(); !IsDomainErrorCode(err, ErrorCodeInvalidTaskStatus) {
		t.Fatalf("expected %s, got %v", ErrorCodeInvalidTaskStatus, err)
	}

	missingTargetProfile := valid
	missingTargetProfile.TargetProfile = ""
	if err := missingTargetProfile.Validate(); !IsDomainErrorCode(err, ErrorCodeFollowTargetProfile) {
		t.Fatalf("expected %s, got %v", ErrorCodeFollowTargetProfile, err)
	}
}

func TestValidateTaskCompletion(t *testing.T) {
	t.Parallel()

	if err := ValidateTaskCompletion(TaskStatusRunning, TaskStatusSuccess, "", ""); err != nil {
		t.Fatalf("expected success completion without reason to pass, got %v", err)
	}
	if err := ValidateTaskCompletion(TaskStatusRunning, TaskStatusRetry, ErrorCodeInternal, ""); err != nil {
		t.Fatalf("expected retry completion with error code to pass, got %v", err)
	}
	if err := ValidateTaskCompletion(TaskStatusRunning, TaskStatusFail, "", "hard-blocked"); err != nil {
		t.Fatalf("expected fail completion with reason to pass, got %v", err)
	}

	err := ValidateTaskCompletion(TaskStatusQueued, TaskStatusSuccess, "", "")
	if !IsDomainErrorCode(err, ErrorCodeTaskNotRunning) {
		t.Fatalf("expected %s, got %v", ErrorCodeTaskNotRunning, err)
	}

	err = ValidateTaskCompletion(TaskStatusRunning, TaskStatusQueued, "", "")
	if !IsDomainErrorCode(err, ErrorCodeInvalidTaskTransition) {
		t.Fatalf("expected %s, got %v", ErrorCodeInvalidTaskTransition, err)
	}

	err = ValidateTaskCompletion(TaskStatusRunning, TaskStatusRetry, "", "")
	if !IsDomainErrorCode(err, ErrorCodeTaskCompletionReason) {
		t.Fatalf("expected %s, got %v", ErrorCodeTaskCompletionReason, err)
	}
}
