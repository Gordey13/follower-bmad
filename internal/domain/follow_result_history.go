package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type FollowResultsHistoryQuery struct {
	AccountID     uuid.UUID
	TargetProfile TargetProfileDescriptor
	Outcome       FollowFlowOutcome
	TaskStatus    TaskStatus
	From          *time.Time
	To            *time.Time
	Limit         int
	Offset        int
}

func (query FollowResultsHistoryQuery) Validate() error {
	rawTarget := string(query.TargetProfile)
	if rawTarget != "" && strings.TrimSpace(rawTarget) == "" {
		return NewDomainError(
			ErrorCodeFollowTargetProfile,
			"target profile filter must not be blank when provided",
		)
	}
	if strings.TrimSpace(rawTarget) != "" {
		if err := query.TargetProfile.Validate(); err != nil {
			return err
		}
	}
	if query.Outcome != "" && !query.Outcome.IsValid() {
		return NewDomainError(
			ErrorCodeFollowResultPersistFailed,
			fmt.Sprintf("invalid follow outcome filter: %s", query.Outcome),
		)
	}
	if query.TaskStatus != "" && !query.TaskStatus.IsValid() {
		return NewDomainError(
			ErrorCodeInvalidTaskStatus,
			fmt.Sprintf("invalid task status filter: %s", query.TaskStatus),
		)
	}
	if query.From != nil && query.To != nil && query.From.After(*query.To) {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			"history time range is invalid: from must be <= to",
		)
	}
	if query.Limit <= 0 {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("history limit must be > 0, got %d", query.Limit),
		)
	}
	if query.Offset < 0 {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("history offset must be >= 0, got %d", query.Offset),
		)
	}

	return nil
}

type FollowResultHistoryEntry struct {
	TaskID             uuid.UUID
	AccountID          uuid.UUID
	TargetProfile      TargetProfileDescriptor
	Attempt            int
	TaskStatus         TaskStatus
	FollowOutcome      FollowFlowOutcome
	Verified           bool
	VerificationSignal FollowVerificationSignal
	ErrorCode          ErrorCode
	ResultReason       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
