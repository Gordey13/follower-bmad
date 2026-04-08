package repository

import (
	"context"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type AccountRepository interface {
	CreateProxy(ctx context.Context, proxy domain.Proxy) error
	CreateAccount(ctx context.Context, account domain.Account) error
	GetAccountWithProxy(ctx context.Context, accountID uuid.UUID) (domain.AccountWithProxy, error)
	OperationalStateSnapshot(ctx context.Context) (map[domain.AccountOperationalState]int64, error)
	UpdateAccountState(
		ctx context.Context,
		accountID uuid.UUID,
		state domain.AccountOperationalState,
		isReady bool,
		isQuarantined bool,
		isRestricted bool,
		limitReached bool,
	) error
	CheckEligibility(ctx context.Context, accountID uuid.UUID) (domain.EligibilityDecision, error)
	ClaimAccount(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.Account, error)
	ReleaseAccount(ctx context.Context, accountID uuid.UUID, executionContextID string) error
}
