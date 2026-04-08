package worker

import (
	"context"
	"testing"

	"follower/internal/domain"

	"github.com/google/uuid"
)

func TestQuotaServiceApplyDecisionPreservesSignalsForLimitReached(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()

	var gotIsReady bool
	var gotIsQuarantined bool
	var gotIsRestricted bool
	var gotLimitReached bool

	repository := &mockAccountRepository{
		getWithProxyFn: func(ctx context.Context, accountID uuid.UUID) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{
				Account: domain.Account{
					ID:               accountID,
					OperationalState: domain.AccountStateActive,
					IsReady:          false,
					IsRestricted:     true,
					LimitReached:     false,
				},
			}, nil
		},
		updateStateFn: func(
			ctx context.Context,
			accountID uuid.UUID,
			state domain.AccountOperationalState,
			isReady bool,
			isQuarantined bool,
			isRestricted bool,
			limitReached bool,
		) error {
			if state != domain.AccountStateQuarantined {
				t.Fatalf("expected state %s, got %s", domain.AccountStateQuarantined, state)
			}
			gotIsReady = isReady
			gotIsQuarantined = isQuarantined
			gotIsRestricted = isRestricted
			gotLimitReached = limitReached
			return nil
		},
	}

	service := NewQuotaService(repository, domain.DefaultRuntimeGuardrails())
	err := service.ApplyDecision(context.Background(), accountID, domain.EligibilityDecision{
		Eligible:   false,
		Outcome:    domain.EligibilityOutcomeExcluded,
		ReasonCode: domain.ErrorCodeAccountLimitReached,
	})
	if err != nil {
		t.Fatalf("ApplyDecision() error = %v", err)
	}

	if gotIsReady {
		t.Fatal("expected isReady to be preserved as false")
	}
	if !gotIsQuarantined {
		t.Fatal("expected isQuarantined=true")
	}
	if !gotIsRestricted {
		t.Fatal("expected isRestricted to be preserved as true")
	}
	if !gotLimitReached {
		t.Fatal("expected limitReached=true")
	}
}

func TestQuotaServiceApplyDecisionPreservesSignalsForRestricted(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()

	var gotIsReady bool
	var gotIsQuarantined bool
	var gotIsRestricted bool
	var gotLimitReached bool

	repository := &mockAccountRepository{
		getWithProxyFn: func(ctx context.Context, accountID uuid.UUID) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{
				Account: domain.Account{
					ID:               accountID,
					OperationalState: domain.AccountStateActive,
					IsReady:          false,
					IsQuarantined:    false,
					LimitReached:     true,
				},
			}, nil
		},
		updateStateFn: func(
			ctx context.Context,
			accountID uuid.UUID,
			state domain.AccountOperationalState,
			isReady bool,
			isQuarantined bool,
			isRestricted bool,
			limitReached bool,
		) error {
			if state != domain.AccountStateRestricted {
				t.Fatalf("expected state %s, got %s", domain.AccountStateRestricted, state)
			}
			gotIsReady = isReady
			gotIsQuarantined = isQuarantined
			gotIsRestricted = isRestricted
			gotLimitReached = limitReached
			return nil
		},
	}

	service := NewQuotaService(repository, domain.DefaultRuntimeGuardrails())
	err := service.ApplyDecision(context.Background(), accountID, domain.EligibilityDecision{
		Eligible:   false,
		Outcome:    domain.EligibilityOutcomeRestricted,
		ReasonCode: domain.ErrorCodeAccountRestricted,
	})
	if err != nil {
		t.Fatalf("ApplyDecision() error = %v", err)
	}

	if gotIsReady {
		t.Fatal("expected isReady to be preserved as false")
	}
	if gotIsQuarantined {
		t.Fatal("expected isQuarantined to remain false")
	}
	if !gotIsRestricted {
		t.Fatal("expected isRestricted=true")
	}
	if !gotLimitReached {
		t.Fatal("expected limitReached to be preserved as true")
	}
}
