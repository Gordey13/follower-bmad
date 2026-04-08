package worker

import (
	"errors"
	"fmt"

	"follower/internal/domain"
)

var deterministicTerminalErrorCodes = map[domain.ErrorCode]struct{}{
	domain.ErrorCodeFollowActionUnavailable:  {},
	domain.ErrorCodeFollowTargetUnreachable:  {},
	domain.ErrorCodeFollowTargetProfile:      {},
	domain.ErrorCodeSessionOwnershipMismatch: {},
	domain.ErrorCodeSessionPayloadInvalid:    {},
	domain.ErrorCodeSessionPayloadCorrupted:  {},
	domain.ErrorCodeAccountContextMismatch:   {},
	domain.ErrorCodeAccountNotFound:          {},
	domain.ErrorCodeAccountInactive:          {},
	domain.ErrorCodeAccountQuarantined:       {},
	domain.ErrorCodeAccountRestricted:        {},
	domain.ErrorCodeAccountMissingProxy:      {},
	domain.ErrorCodeAccountProxyInactive:     {},
	domain.ErrorCodeAccountLimitReached:      {},
	domain.ErrorCodeAuthInvalidCredentials:   {},
	domain.ErrorCodeAuthChallengeBlocked:     {},
	domain.ErrorCodeAuthBootstrapDisabled:    {},
}

func deterministicStatusForErrorCode(errorCode domain.ErrorCode) domain.TaskStatus {
	if _, ok := deterministicTerminalErrorCodes[errorCode]; ok {
		return domain.TaskStatusFail
	}

	// Safe deterministic fallback for unknown/unsupported codes.
	return domain.TaskStatusRetry
}

func deterministicStatusForSessionSaveSource(sourceErrorCode domain.ErrorCode) domain.TaskStatus {
	if sourceErrorCode == "" {
		return domain.TaskStatusRetry
	}
	return deterministicStatusForErrorCode(sourceErrorCode)
}

func classifyLifecycleExecutionFailure(err error) (domain.TaskStatus, domain.ErrorCode) {
	errorCode := lifecycleErrorCode(err)
	return deterministicStatusForErrorCode(errorCode), errorCode
}

func classifyLifecyclePreparationFailure(err error) (domain.TaskStatus, domain.ErrorCode, string) {
	errorCode := lifecycleErrorCode(err)
	finalStatus := deterministicStatusForErrorCode(errorCode)
	return finalStatus, errorCode, fmt.Sprintf(
		"execution context preparation failed (status=%s, error_code=%s)",
		finalStatus,
		errorCode,
	)
}

func lifecycleErrorCode(err error) domain.ErrorCode {
	var domainErr *domain.DomainError
	if errors.As(err, &domainErr) {
		return domainErr.Code
	}

	return domain.ErrorCodeInternal
}
