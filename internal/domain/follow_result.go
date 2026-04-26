package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type FollowVerificationSignal string

const (
	FollowVerificationSignalFollowConfirmed   FollowVerificationSignal = "follow_confirmed"
	FollowVerificationSignalAlreadyDone       FollowVerificationSignal = "follow_already_done"
	FollowVerificationSignalActionUnavailable FollowVerificationSignal = "follow_action_unavailable"
	FollowVerificationSignalTargetUnreachable FollowVerificationSignal = "follow_target_unreachable"
	FollowVerificationSignalNavigationFailed  FollowVerificationSignal = "follow_navigation_failed"
	FollowVerificationSignalVerifyFailed      FollowVerificationSignal = "follow_verify_failed"
)

func (signal FollowVerificationSignal) IsValid() bool {
	switch signal {
	case FollowVerificationSignalFollowConfirmed,
		FollowVerificationSignalAlreadyDone,
		FollowVerificationSignalActionUnavailable,
		FollowVerificationSignalTargetUnreachable,
		FollowVerificationSignalNavigationFailed,
		FollowVerificationSignalVerifyFailed:
		return true
	default:
		return false
	}
}

type FollowVerificationInput struct {
	TaskID             uuid.UUID
	AccountID          uuid.UUID
	Attempt            int
	ExecutionContextID string
	TargetProfile      TargetProfileDescriptor
	Outcome            FollowFlowOutcome
	SessionPayload     []byte
}

func (input FollowVerificationInput) Validate() error {
	if input.TaskID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidTaskIdentifier,
			"follow verification input task id must not be empty",
		)
	}
	if input.AccountID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidAccountIdentifier,
			"follow verification input account id must not be empty",
		)
	}
	if input.Attempt <= 0 {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("follow verification input attempt must be > 0, got %d", input.Attempt),
		)
	}
	if strings.TrimSpace(input.ExecutionContextID) == "" {
		return NewDomainError(
			ErrorCodeInvalidExecutionContext,
			"follow verification input execution context id must not be empty",
		)
	}
	if err := input.TargetProfile.Validate(); err != nil {
		return err
	}
	if !input.Outcome.IsValid() {
		return NewDomainError(
			ErrorCodeFollowVerifyFailed,
			fmt.Sprintf("follow verification input outcome is invalid: %s", input.Outcome),
		)
	}

	return nil
}

type FollowVerificationResult struct {
	Verified          bool
	Signal            FollowVerificationSignal
	Reason            string
	ErrorCode         ErrorCode
	SessionChanged    bool
	ScreenshotPayload []byte
	SessionPayload    []byte `json:"-"`
}

func (result FollowVerificationResult) Validate() error {
	if !result.Signal.IsValid() {
		return NewDomainError(
			ErrorCodeFollowVerifyFailed,
			fmt.Sprintf("follow verification signal is invalid: %s", result.Signal),
		)
	}
	if len(result.ScreenshotPayload) == 0 {
		return NewDomainError(
			ErrorCodeFollowVerifyFailed,
			"follow verification screenshot payload must not be empty",
		)
	}
	if !result.Verified && strings.TrimSpace(string(result.ErrorCode)) == "" {
		return NewDomainError(
			ErrorCodeFollowVerifyFailed,
			"unverified follow result requires machine-readable error code",
		)
	}
	if result.Verified && result.ErrorCode != "" {
		return NewDomainError(
			ErrorCodeFollowVerifyFailed,
			"verified follow result must not contain error code",
		)
	}
	if result.SessionChanged && len(result.SessionPayload) == 0 {
		return NewDomainError(
			ErrorCodeSessionPayloadMissing,
			"follow verification marked session as changed but session payload is empty",
		)
	}

	return nil
}

type FollowResult struct {
	TaskID              uuid.UUID
	AccountID           uuid.UUID
	TargetProfile       TargetProfileDescriptor
	Attempt             int
	Outcome             FollowFlowOutcome
	Verified            bool
	VerificationSignal  FollowVerificationSignal
	VerificationReason  string
	ErrorCode           ErrorCode
	ScreenshotObjectKey string
	ArtifactObjectKeys  []string
	SessionRevision     int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (result FollowResult) Validate() error {
	if result.TaskID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidTaskIdentifier,
			"follow result task id must not be empty",
		)
	}
	if result.AccountID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidAccountIdentifier,
			"follow result account id must not be empty",
		)
	}
	if err := result.TargetProfile.Validate(); err != nil {
		return err
	}
	if result.Attempt <= 0 {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("follow result attempt must be > 0, got %d", result.Attempt),
		)
	}
	if !result.Outcome.IsValid() {
		return NewDomainError(
			ErrorCodeFollowResultPersistFailed,
			fmt.Sprintf("follow result outcome is invalid: %s", result.Outcome),
		)
	}
	if !result.VerificationSignal.IsValid() {
		return NewDomainError(
			ErrorCodeFollowResultPersistFailed,
			fmt.Sprintf("follow verification signal is invalid: %s", result.VerificationSignal),
		)
	}
	if strings.TrimSpace(result.ScreenshotObjectKey) == "" {
		return NewDomainError(
			ErrorCodeFollowResultPersistFailed,
			"follow result screenshot object key must not be empty",
		)
	}
	if len(result.ArtifactObjectKeys) == 0 {
		return NewDomainError(
			ErrorCodeFollowResultPersistFailed,
			"follow result must contain at least one artifact object key",
		)
	}
	if result.Verified && strings.TrimSpace(string(result.ErrorCode)) != "" {
		return NewDomainError(
			ErrorCodeFollowResultPersistFailed,
			"verified follow result must not contain error code",
		)
	}
	if !result.Verified && strings.TrimSpace(string(result.ErrorCode)) == "" {
		return NewDomainError(
			ErrorCodeFollowResultPersistFailed,
			"unverified follow result requires machine-readable error code",
		)
	}
	if result.SessionRevision < 0 {
		return NewDomainError(
			ErrorCodeFollowResultPersistFailed,
			fmt.Sprintf("session revision must be >= 0, got %d", result.SessionRevision),
		)
	}

	return nil
}

type FollowExecutionFinalizationInput struct {
	TaskID            uuid.UUID
	AccountID         uuid.UUID
	AccountLogin      string
	TargetProfile     TargetProfileDescriptor
	Attempt           int
	SessionRevision   int64
	FollowOutcome     FollowFlowOutcome
	FollowDiagnostics FollowFlowDiagnostics
	Verification      FollowVerificationResult
	SessionPayload    []byte
}

func (input FollowExecutionFinalizationInput) Validate() error {
	if input.TaskID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidTaskIdentifier,
			"follow finalization input task id must not be empty",
		)
	}
	if input.AccountID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidAccountIdentifier,
			"follow finalization input account id must not be empty",
		)
	}
	if err := input.TargetProfile.Validate(); err != nil {
		return err
	}
	if input.Attempt <= 0 {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("follow finalization input attempt must be > 0, got %d", input.Attempt),
		)
	}
	if input.SessionRevision < 0 {
		return NewDomainError(
			ErrorCodeInvalidSessionRevision,
			fmt.Sprintf("follow finalization input session revision must be >= 0, got %d", input.SessionRevision),
		)
	}
	if !input.FollowOutcome.IsValid() {
		return NewDomainError(
			ErrorCodeFollowVerifyFailed,
			fmt.Sprintf("follow finalization input outcome is invalid: %s", input.FollowOutcome),
		)
	}
	if err := input.Verification.Validate(); err != nil {
		return err
	}
	if input.Verification.SessionChanged && len(input.SessionPayload) == 0 {
		return NewDomainError(
			ErrorCodeSessionPayloadMissing,
			"follow finalization input requires session payload when session_changed=true",
		)
	}

	return nil
}
