package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Task struct {
	ID            uuid.UUID
	AccountID     uuid.UUID
	TargetProfile TargetProfileDescriptor
	Status        TaskStatus
	Attempt       int
	ClaimedBy     string
	ClaimedAt     *time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	ErrorCode     ErrorCode
	ResultReason  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (task Task) Validate() error {
	if task.ID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidTaskIdentifier,
			"task id must not be empty",
		)
	}
	if task.AccountID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidAccountIdentifier,
			"task account id must not be empty",
		)
	}
	if err := task.TargetProfile.Validate(); err != nil {
		return err
	}
	if !task.Status.IsValid() {
		return NewDomainError(
			ErrorCodeInvalidTaskStatus,
			fmt.Sprintf("invalid task status: %s", task.Status),
		)
	}
	if task.Attempt < 0 {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("task attempt must be >= 0, got %d", task.Attempt),
		)
	}

	return nil
}

func (task Task) ValidateTransition(nextStatus TaskStatus) error {
	if !task.Status.IsValid() || !nextStatus.IsValid() {
		return NewDomainError(
			ErrorCodeInvalidTaskStatus,
			fmt.Sprintf("invalid task transition %s -> %s", task.Status, nextStatus),
		)
	}
	if !task.Status.CanTransitionTo(nextStatus) {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("transition %s -> %s is not allowed", task.Status, nextStatus),
		)
	}

	return nil
}

func ValidateTaskCompletion(
	currentStatus TaskStatus,
	nextStatus TaskStatus,
	errorCode ErrorCode,
	resultReason string,
) error {
	if currentStatus != TaskStatusRunning {
		return NewDomainError(
			ErrorCodeTaskNotRunning,
			fmt.Sprintf("task must be in running status, got %s", currentStatus),
		)
	}
	if !currentStatus.CanTransitionTo(nextStatus) {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("transition %s -> %s is not allowed", currentStatus, nextStatus),
		)
	}
	if (nextStatus == TaskStatusRetry || nextStatus == TaskStatusFail) &&
		strings.TrimSpace(string(errorCode)) == "" &&
		strings.TrimSpace(resultReason) == "" {
		return NewDomainError(
			ErrorCodeTaskCompletionReason,
			"retry/fail completion requires error_code or result_reason",
		)
	}

	return nil
}
