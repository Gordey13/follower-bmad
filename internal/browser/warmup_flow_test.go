package browser

import (
	"context"
	"errors"
	"testing"

	"follower/internal/domain"
)

func TestRunPlaywrightWarmupFlowRequiresValidOskellyProfilePath(t *testing.T) {
	t.Parallel()

	err := runPlaywrightWarmupFlow(
		context.Background(),
		testFollowFlowInput("invalid-target"),
		&stubPlaywrightFollowAdapter{},
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowTargetProfile) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowTargetProfile, err)
	}
}

func TestRunPlaywrightWarmupFlowReturnsMachineReadableTargetUnreachableError(t *testing.T) {
	t.Parallel()

	err := runPlaywrightWarmupFlow(
		context.Background(),
		testFollowFlowInput("https://oskelly.ru/profile/100004"),
		&stubPlaywrightFollowAdapter{
			warmupErr: domain.NewDomainError(
				domain.ErrorCodeFollowTargetUnreachable,
				"target is unavailable",
			),
		},
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowTargetUnreachable) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowTargetUnreachable, err)
	}
}

func TestRunPlaywrightWarmupFlowMapsUnknownErrorsToNavigationFailed(t *testing.T) {
	t.Parallel()

	err := runPlaywrightWarmupFlow(
		context.Background(),
		testFollowFlowInput("https://oskelly.ru/profile/100005"),
		&stubPlaywrightFollowAdapter{
			warmupErr: errors.New("temporary transport failure"),
		},
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowNavigationFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowNavigationFailed, err)
	}
}

func TestRunPlaywrightWarmupFlowMapsSessionPayloadDecodeError(t *testing.T) {
	t.Parallel()

	input := testFollowFlowInput("https://oskelly.ru/profile/100006")
	input.SessionPayload = []byte("{")
	err := runPlaywrightWarmupFlow(
		context.Background(),
		input,
		&stubPlaywrightFollowAdapter{},
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadInvalid) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionPayloadInvalid, err)
	}
}
