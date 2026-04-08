package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type ErrorCode string

const (
	ErrorCodeEligible                  ErrorCode = "eligible"
	ErrorCodeInternal                  ErrorCode = "internal_error"
	ErrorCodeAccountNotFound           ErrorCode = "account_not_found"
	ErrorCodeAccountBusy               ErrorCode = "account_busy"
	ErrorCodeAccountInactive           ErrorCode = "account_inactive"
	ErrorCodeAccountNotReady           ErrorCode = "account_not_ready"
	ErrorCodeAccountQuarantined        ErrorCode = "account_quarantined"
	ErrorCodeAccountRestricted         ErrorCode = "account_restricted"
	ErrorCodeAccountMissingProxy       ErrorCode = "account_missing_proxy"
	ErrorCodeAccountProxyInactive      ErrorCode = "account_proxy_inactive"
	ErrorCodeAccountLimitReached       ErrorCode = "account_limit_reached"
	ErrorCodeAccountContextMismatch    ErrorCode = "account_context_mismatch"
	ErrorCodeInvalidOperationalState   ErrorCode = "invalid_operational_state"
	ErrorCodeInvalidExecutionContext   ErrorCode = "invalid_execution_context"
	ErrorCodeInvalidAccountIdentifier  ErrorCode = "invalid_account_identifier"
	ErrorCodeSessionMetadataNotFound   ErrorCode = "session_metadata_not_found"
	ErrorCodeSessionPayloadMissing     ErrorCode = "session_payload_missing"
	ErrorCodeSessionPayloadCorrupted   ErrorCode = "session_payload_corrupted"
	ErrorCodeSessionOwnershipMismatch  ErrorCode = "session_ownership_mismatch"
	ErrorCodeSessionPayloadInvalid     ErrorCode = "session_payload_invalid"
	ErrorCodeInvalidSessionStatus      ErrorCode = "invalid_session_status"
	ErrorCodeInvalidSessionRevision    ErrorCode = "invalid_session_revision"
	ErrorCodeInvalidSessionObjectKey   ErrorCode = "invalid_session_object_key"
	ErrorCodeTaskNotFound              ErrorCode = "task_not_found"
	ErrorCodeTaskNotRunning            ErrorCode = "task_not_running"
	ErrorCodeTaskClaimOwnerMismatch    ErrorCode = "task_claim_owner_mismatch"
	ErrorCodeInvalidTaskIdentifier     ErrorCode = "invalid_task_identifier"
	ErrorCodeInvalidTaskStatus         ErrorCode = "invalid_task_status"
	ErrorCodeInvalidTaskTransition     ErrorCode = "invalid_task_transition"
	ErrorCodeInvalidTaskClaimedBy      ErrorCode = "invalid_task_claimed_by"
	ErrorCodeTaskCompletionReason      ErrorCode = "task_completion_reason_required"
	ErrorCodeFollowTargetProfile       ErrorCode = "follow_target_profile_required"
	ErrorCodeFollowActionUnavailable   ErrorCode = "follow_action_unavailable"
	ErrorCodeFollowTargetUnreachable   ErrorCode = "follow_target_unreachable"
	ErrorCodeFollowNavigationFailed    ErrorCode = "follow_navigation_failed"
	ErrorCodeFollowVerifyFailed        ErrorCode = "follow_verify_failed"
	ErrorCodeArtifactPersistFailed     ErrorCode = "artifact_persist_failed"
	ErrorCodeFollowResultPersistFailed ErrorCode = "follow_result_persist_failed"
	ErrorCodeFollowResultNotFound      ErrorCode = "follow_result_not_found"
	ErrorCodeSessionSaveFailed         ErrorCode = "session_save_failed"
	ErrorCodeAuthBootstrapRequired     ErrorCode = "auth_bootstrap_required"
	ErrorCodeAuthBootstrapFailed       ErrorCode = "auth_bootstrap_failed"
	ErrorCodeAuthInvalidCredentials    ErrorCode = "auth_invalid_credentials"
	ErrorCodeAuthChallengeBlocked      ErrorCode = "auth_challenge_blocked"
	ErrorCodeAuthBootstrapDisabled     ErrorCode = "auth_bootstrap_disabled"
)

type DomainError struct {
	Code    ErrorCode
	Message string
}

func (e *DomainError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewDomainError(code ErrorCode, message string) *DomainError {
	return &DomainError{
		Code:    code,
		Message: message,
	}
}

func IsDomainErrorCode(err error, code ErrorCode) bool {
	var domainErr *DomainError
	if !errors.As(err, &domainErr) {
		return false
	}
	return domainErr.Code == code
}

type EligibilityDecision struct {
	Eligible   bool
	Outcome    EligibilityOutcome
	ReasonCode ErrorCode
}

func EvaluateAccountEligibility(account Account, proxy *Proxy) EligibilityDecision {
	return EvaluateAccountEligibilityWithGuardrails(account, proxy, DefaultRuntimeGuardrails())
}

// Decision precedence:
// 1) account/proxy suitability
// 2) session validity
// 3) explicit quarantine
// 4) hard limits
// 5) restrictive thresholds
// 6) active execution ownership
func EvaluateAccountEligibilityWithGuardrails(
	account Account,
	proxy *Proxy,
	guardrails RuntimeGuardrails,
) EligibilityDecision {
	guardrails = guardrails.Normalized()

	if account.ID == uuid.Nil {
		return ineligibleDecision(EligibilityOutcomeExcluded, ErrorCodeInvalidAccountIdentifier)
	}
	if !account.OperationalState.IsValid() {
		return ineligibleDecision(EligibilityOutcomeExcluded, ErrorCodeInvalidOperationalState)
	}
	if guardrails.RequireProxyBinding {
		if account.ProxyID == uuid.Nil || proxy == nil {
			return ineligibleDecision(EligibilityOutcomeExcluded, ErrorCodeAccountMissingProxy)
		}
		if !proxy.IsActive {
			return ineligibleDecision(EligibilityOutcomeExcluded, ErrorCodeAccountProxyInactive)
		}
	}
	if !account.IsActive {
		return ineligibleDecision(EligibilityOutcomeExcluded, ErrorCodeAccountInactive)
	}
	if !account.IsReady || account.OperationalState == AccountStateInvalidSession {
		return ineligibleDecision(EligibilityOutcomeExcluded, ErrorCodeAccountNotReady)
	}
	if account.IsQuarantined || account.OperationalState == AccountStateQuarantined {
		return ineligibleDecision(EligibilityOutcomeExcluded, ErrorCodeAccountQuarantined)
	}

	limits := account.Limits()
	if limits.LimitReached && guardrails.ExcludeWhenLimitReached {
		return ineligibleDecision(EligibilityOutcomeExcluded, ErrorCodeAccountLimitReached)
	}
	if limits.RestrictiveThresholdRaised {
		if guardrails.RestrictWhenThresholdReached {
			return ineligibleDecision(EligibilityOutcomeRestricted, ErrorCodeAccountRestricted)
		}
		return ineligibleDecision(EligibilityOutcomeExcluded, ErrorCodeAccountRestricted)
	}
	if account.ActiveExecutionContextID != "" {
		return ineligibleDecision(EligibilityOutcomeExcluded, ErrorCodeAccountBusy)
	}

	return EligibilityDecision{
		Eligible:   true,
		Outcome:    EligibilityOutcomeEligible,
		ReasonCode: ErrorCodeEligible,
	}
}

func EnsureSingleActiveExecution(account Account, executionContextID string) error {
	if executionContextID == "" {
		return NewDomainError(ErrorCodeInvalidExecutionContext, "execution context id must not be empty")
	}
	if account.ActiveExecutionContextID != "" && account.ActiveExecutionContextID != executionContextID {
		return NewDomainError(
			ErrorCodeAccountBusy,
			fmt.Sprintf("account %s is already claimed by another execution context", account.ID.String()),
		)
	}
	return nil
}

func ineligibleDecision(outcome EligibilityOutcome, reasonCode ErrorCode) EligibilityDecision {
	return EligibilityDecision{
		Eligible:   false,
		Outcome:    outcome,
		ReasonCode: reasonCode,
	}
}
