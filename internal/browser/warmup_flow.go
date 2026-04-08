package browser

import (
	"context"
	"encoding/json"
	"errors"

	"follower/internal/domain"

	"github.com/playwright-community/playwright-go"
)

type playwrightWarmupAdapter interface {
	Warmup(ctx context.Context, input domain.FollowFlowInput) error
}

type playwrightFollowAdapter interface {
	playwrightWarmupAdapter
	RunFollowAction(
		ctx context.Context,
		input domain.FollowFlowInput,
	) (domain.FollowFlowOutcome, error)
}

func runMockWarmupFlow(ctx context.Context, input domain.FollowFlowInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := input.TargetProfile.Validate(); err != nil {
		return err
	}
	return nil
}

func runPlaywrightWarmupFlow(
	ctx context.Context,
	input domain.FollowFlowInput,
	adapter ...playwrightWarmupAdapter,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := input.TargetProfile.Validate(); err != nil {
		return err
	}
	if _, err := normalizeOskellyTargetProfileURL(input.TargetProfile); err != nil {
		return err
	}
	if _, err := parsePlaywrightStorageStatePayload(input.SessionPayload); err != nil {
		return err
	}

	selectedAdapter := playwrightWarmupAdapter(&defaultPlaywrightFollowAdapter{})
	if len(adapter) > 0 && adapter[0] != nil {
		selectedAdapter = adapter[0]
	}
	if err := selectedAdapter.Warmup(ctx, input); err != nil {
		return normalizePlaywrightFollowError(err)
	}

	return nil
}

func normalizeWarmupError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return domain.NewDomainError(
			domain.ErrorCodeFollowNavigationFailed,
			"follow warmup interrupted by context timeout/cancel",
		)
	}
	return err
}

func parsePlaywrightStorageStatePayload(payload []byte) (*playwright.OptionalStorageState, error) {
	var storageState playwright.StorageState
	if err := json.Unmarshal(payload, &storageState); err != nil {
		return nil, domain.NewDomainError(
			domain.ErrorCodeSessionPayloadInvalid,
			"session payload is not a valid playwright storage state",
		)
	}

	return storageState.ToOptionalStorageState(), nil
}
