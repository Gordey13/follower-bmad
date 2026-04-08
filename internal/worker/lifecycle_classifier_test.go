package worker

import (
	"strings"
	"testing"

	"follower/internal/domain"
)

func TestFinalStatusForErrorCodeDeterministicMatrix(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		errorCode domain.ErrorCode
		expect    domain.TaskStatus
	}{
		{name: "follow action unavailable is terminal", errorCode: domain.ErrorCodeFollowActionUnavailable, expect: domain.TaskStatusFail},
		{name: "follow target unreachable is terminal", errorCode: domain.ErrorCodeFollowTargetUnreachable, expect: domain.TaskStatusFail},
		{name: "session ownership mismatch is terminal", errorCode: domain.ErrorCodeSessionOwnershipMismatch, expect: domain.TaskStatusFail},
		{name: "auth invalid credentials is terminal", errorCode: domain.ErrorCodeAuthInvalidCredentials, expect: domain.TaskStatusFail},
		{name: "auth challenge blocked is terminal", errorCode: domain.ErrorCodeAuthChallengeBlocked, expect: domain.TaskStatusFail},
		{name: "auth bootstrap disabled is terminal", errorCode: domain.ErrorCodeAuthBootstrapDisabled, expect: domain.TaskStatusFail},
		{name: "account context mismatch is terminal", errorCode: domain.ErrorCodeAccountContextMismatch, expect: domain.TaskStatusFail},
		{name: "account quarantined is terminal", errorCode: domain.ErrorCodeAccountQuarantined, expect: domain.TaskStatusFail},
		{name: "follow navigation failed is transient", errorCode: domain.ErrorCodeFollowNavigationFailed, expect: domain.TaskStatusRetry},
		{name: "internal error is transient", errorCode: domain.ErrorCodeInternal, expect: domain.TaskStatusRetry},
		{name: "unknown code falls back to retry", errorCode: domain.ErrorCodeFollowResultPersistFailed, expect: domain.TaskStatusRetry},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := finalStatusForErrorCode(tc.errorCode)
			if got != tc.expect {
				t.Fatalf("expected status %s, got %s for error_code=%s", tc.expect, got, tc.errorCode)
			}
		})
	}
}

func TestClassifyExecutionFailureUnknownCodeFallsBackToRetry(t *testing.T) {
	t.Parallel()

	status, errorCode := classifyExecutionFailure(domain.NewDomainError(
		domain.ErrorCodeFollowResultPersistFailed,
		"temporary persistence failure",
	))
	if status != domain.TaskStatusRetry {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusRetry, status)
	}
	if errorCode != domain.ErrorCodeFollowResultPersistFailed {
		t.Fatalf("expected error_code %s, got %s", domain.ErrorCodeFollowResultPersistFailed, errorCode)
	}
}

func TestClassifyFollowCompletionResultDeterministicMatrix(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		outcome       domain.FollowFlowOutcome
		followErr     error
		verification  domain.FollowVerificationResult
		verifyErr     error
		expectStatus  domain.TaskStatus
		expectCode    domain.ErrorCode
		expectReason  bool
		expectSuccess bool
	}{
		{
			name:    "verified completed is success",
			outcome: domain.FollowFlowOutcomeCompleted,
			verification: domain.FollowVerificationResult{
				Verified: true,
				Signal:   domain.FollowVerificationSignalFollowConfirmed,
			},
			expectStatus:  domain.TaskStatusSuccess,
			expectCode:    "",
			expectReason:  false,
			expectSuccess: true,
		},
		{
			name:    "verify error is retry",
			outcome: domain.FollowFlowOutcomeCompleted,
			verification: domain.FollowVerificationResult{
				Verified: true,
				Signal:   domain.FollowVerificationSignalFollowConfirmed,
			},
			verifyErr:    domain.NewDomainError(domain.ErrorCodeFollowVerifyFailed, "verify transport timeout"),
			expectStatus: domain.TaskStatusRetry,
			expectCode:   domain.ErrorCodeFollowVerifyFailed,
			expectReason: true,
		},
		{
			name:    "unverified maps to deterministic error code",
			outcome: domain.FollowFlowOutcomeCompleted,
			verification: domain.FollowVerificationResult{
				Verified:  false,
				Signal:    domain.FollowVerificationSignalActionUnavailable,
				ErrorCode: domain.ErrorCodeFollowActionUnavailable,
			},
			expectStatus: domain.TaskStatusFail,
			expectCode:   domain.ErrorCodeFollowActionUnavailable,
			expectReason: true,
		},
		{
			name:    "unverified without error code falls back to outcome code",
			outcome: domain.FollowFlowOutcomeTargetUnreachable,
			verification: domain.FollowVerificationResult{
				Verified: false,
				Signal:   domain.FollowVerificationSignalTargetUnreachable,
			},
			expectStatus: domain.TaskStatusFail,
			expectCode:   domain.ErrorCodeFollowTargetUnreachable,
			expectReason: true,
		},
		{
			name:      "follow error uses deterministic mapping",
			outcome:   domain.FollowFlowOutcomeCompleted,
			followErr: domain.NewDomainError(domain.ErrorCodeFollowNavigationFailed, "playwright timeout"),
			verification: domain.FollowVerificationResult{
				Verified: true,
				Signal:   domain.FollowVerificationSignalFollowConfirmed,
			},
			expectStatus: domain.TaskStatusRetry,
			expectCode:   domain.ErrorCodeFollowNavigationFailed,
			expectReason: true,
		},
		{
			name:    "action unavailable outcome is fail",
			outcome: domain.FollowFlowOutcomeActionUnavailable,
			verification: domain.FollowVerificationResult{
				Verified: true,
				Signal:   domain.FollowVerificationSignalFollowConfirmed,
			},
			expectStatus: domain.TaskStatusFail,
			expectCode:   domain.ErrorCodeFollowActionUnavailable,
			expectReason: true,
		},
		{
			name:    "navigation failed outcome is retry",
			outcome: domain.FollowFlowOutcomeNavigationFailed,
			verification: domain.FollowVerificationResult{
				Verified: true,
				Signal:   domain.FollowVerificationSignalFollowConfirmed,
			},
			expectStatus: domain.TaskStatusRetry,
			expectCode:   domain.ErrorCodeFollowNavigationFailed,
			expectReason: true,
		},
		{
			name:    "unsupported outcome falls back to retry internal",
			outcome: domain.FollowFlowOutcome("follow_unsupported"),
			verification: domain.FollowVerificationResult{
				Verified: true,
				Signal:   domain.FollowVerificationSignalFollowConfirmed,
			},
			expectStatus: domain.TaskStatusRetry,
			expectCode:   domain.ErrorCodeInternal,
			expectReason: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, errorCode, reason := classifyFollowCompletionResult(
				tc.outcome,
				tc.followErr,
				tc.verification,
				tc.verifyErr,
			)
			if status != tc.expectStatus {
				t.Fatalf("expected status %s, got %s", tc.expectStatus, status)
			}
			if errorCode != tc.expectCode {
				t.Fatalf("expected error_code %s, got %s", tc.expectCode, errorCode)
			}

			gotReason := strings.TrimSpace(reason)
			if tc.expectSuccess {
				if gotReason != "" {
					t.Fatalf("expected empty reason for success, got %q", reason)
				}
				return
			}
			if tc.expectReason && gotReason == "" {
				t.Fatal("expected non-empty deterministic reason")
			}
			if tc.expectReason && !strings.Contains(gotReason, "status=") {
				t.Fatalf("expected reason to include status marker, got %q", reason)
			}
			if tc.expectReason && !strings.Contains(gotReason, "error_code=") {
				t.Fatalf("expected reason to include error_code marker, got %q", reason)
			}
		})
	}
}

func TestClassifyFinalizationErrorSessionSaveSourceDeterministicMapping(t *testing.T) {
	t.Parallel()

	failStatus, failCode, failReason := classifyFinalizationError(domain.NewDomainError(
		domain.ErrorCodeSessionSaveFailed,
		"session payload persistence failed (source_error_code=session_payload_invalid)",
	))
	if failStatus != domain.TaskStatusFail {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusFail, failStatus)
	}
	if failCode != domain.ErrorCodeSessionSaveFailed {
		t.Fatalf("expected error_code %s, got %s", domain.ErrorCodeSessionSaveFailed, failCode)
	}
	if !strings.Contains(failReason, "source_error_code=session_payload_invalid") {
		t.Fatalf("expected source error_code in reason, got %q", failReason)
	}

	retryStatus, retryCode, retryReason := classifyFinalizationError(domain.NewDomainError(
		domain.ErrorCodeSessionSaveFailed,
		"session payload persistence failed (source_error_code=internal_error)",
	))
	if retryStatus != domain.TaskStatusRetry {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusRetry, retryStatus)
	}
	if retryCode != domain.ErrorCodeSessionSaveFailed {
		t.Fatalf("expected error_code %s, got %s", domain.ErrorCodeSessionSaveFailed, retryCode)
	}
	if !strings.Contains(retryReason, "source_error_code=internal_error") {
		t.Fatalf("expected source error_code in reason, got %q", retryReason)
	}
}

func TestMapPreparationFailureOutcomeUsesDeterministicPolicy(t *testing.T) {
	t.Parallel()

	failStatus, failCode, failReason := mapPreparationFailureOutcome(domain.NewDomainError(
		domain.ErrorCodeSessionOwnershipMismatch,
		"ownership mismatch",
	))
	if failStatus != domain.TaskStatusFail {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusFail, failStatus)
	}
	if failCode != domain.ErrorCodeSessionOwnershipMismatch {
		t.Fatalf("expected error_code %s, got %s", domain.ErrorCodeSessionOwnershipMismatch, failCode)
	}
	if !strings.Contains(failReason, "error_code="+string(domain.ErrorCodeSessionOwnershipMismatch)) {
		t.Fatalf("expected machine-readable error code in reason, got %q", failReason)
	}

	retryStatus, retryCode, retryReason := mapPreparationFailureOutcome(domain.NewDomainError(
		domain.ErrorCodeSessionPayloadMissing,
		"payload missing",
	))
	if retryStatus != domain.TaskStatusRetry {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusRetry, retryStatus)
	}
	if retryCode != domain.ErrorCodeSessionPayloadMissing {
		t.Fatalf("expected error_code %s, got %s", domain.ErrorCodeSessionPayloadMissing, retryCode)
	}
	if !strings.Contains(retryReason, "error_code="+string(domain.ErrorCodeSessionPayloadMissing)) {
		t.Fatalf("expected machine-readable error code in reason, got %q", retryReason)
	}
}
