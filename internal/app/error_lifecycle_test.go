package app

import (
	"errors"
	"testing"

	"follower/internal/domain"
)

func TestAppLifecycleErrorCode(t *testing.T) {
	t.Parallel()

	if got := appLifecycleErrorCode(nil); got != string(domain.ErrorCodeEligible) {
		t.Fatalf("expected eligible for nil error, got %q", got)
	}

	domainErr := domain.NewDomainError(domain.ErrorCodeFollowResultPersistFailed, "persist failed")
	if got := appLifecycleErrorCode(domainErr); got != string(domain.ErrorCodeFollowResultPersistFailed) {
		t.Fatalf("expected domain error code, got %q", got)
	}

	if got := appLifecycleErrorCode(errors.New("boom")); got != string(domain.ErrorCodeInternal) {
		t.Fatalf("expected internal for non-domain error, got %q", got)
	}
}
