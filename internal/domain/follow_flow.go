package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type TargetProfileDescriptor string

func (descriptor TargetProfileDescriptor) Validate() error {
	if strings.TrimSpace(string(descriptor)) == "" {
		return NewDomainError(
			ErrorCodeFollowTargetProfile,
			"target profile descriptor must not be empty",
		)
	}

	return nil
}

type FollowFlowOutcome string

const (
	FollowFlowOutcomeCompleted         FollowFlowOutcome = "follow_completed"
	FollowFlowOutcomeAlreadyDone       FollowFlowOutcome = "follow_already_done"
	FollowFlowOutcomeActionUnavailable FollowFlowOutcome = "follow_action_unavailable"
	FollowFlowOutcomeTargetUnreachable FollowFlowOutcome = "follow_target_unreachable"
	FollowFlowOutcomeNavigationFailed  FollowFlowOutcome = "follow_navigation_failed"
)

func (outcome FollowFlowOutcome) IsValid() bool {
	switch outcome {
	case FollowFlowOutcomeCompleted,
		FollowFlowOutcomeAlreadyDone,
		FollowFlowOutcomeActionUnavailable,
		FollowFlowOutcomeTargetUnreachable,
		FollowFlowOutcomeNavigationFailed:
		return true
	default:
		return false
	}
}

type FollowFlowInput struct {
	TaskID             uuid.UUID
	AccountID          uuid.UUID
	Attempt            int
	ExecutionContextID string
	SessionMetadata    SessionMetadata
	SessionPayload     []byte
	TargetProfile      TargetProfileDescriptor
}

func (input FollowFlowInput) Validate() error {
	if input.TaskID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidTaskIdentifier,
			"follow flow input task id must not be empty",
		)
	}
	if input.AccountID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidAccountIdentifier,
			"follow flow input account id must not be empty",
		)
	}
	if strings.TrimSpace(input.ExecutionContextID) == "" {
		return NewDomainError(
			ErrorCodeInvalidExecutionContext,
			"follow flow input execution context id must not be empty",
		)
	}
	if input.Attempt <= 0 {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("follow flow input attempt must be > 0, got %d", input.Attempt),
		)
	}
	if len(input.SessionPayload) == 0 {
		return NewDomainError(
			ErrorCodeSessionPayloadMissing,
			"follow flow input session payload must not be empty",
		)
	}
	return input.TargetProfile.Validate()
}

type FollowFlowDiagnostics struct {
	Engine              string
	WarmupCompleted     bool
	WarmupDurationMS    int64
	ExecutionDurationMS int64
}
