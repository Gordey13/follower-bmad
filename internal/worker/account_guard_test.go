package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type mockAccountRepository struct {
	checkEligibilityFn func(ctx context.Context, accountID uuid.UUID) (domain.EligibilityDecision, error)
	claimAccountFn     func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.Account, error)
	getWithProxyFn     func(ctx context.Context, accountID uuid.UUID) (domain.AccountWithProxy, error)
	releaseAccountFn   func(ctx context.Context, accountID uuid.UUID, executionContextID string) error
	updateStateFn      func(
		ctx context.Context,
		accountID uuid.UUID,
		state domain.AccountOperationalState,
		isReady bool,
		isQuarantined bool,
		isRestricted bool,
		limitReached bool,
	) error
}

func (m *mockAccountRepository) CreateProxy(ctx context.Context, proxy domain.Proxy) error {
	return nil
}

func (m *mockAccountRepository) CreateAccount(ctx context.Context, account domain.Account) error {
	return nil
}

func (m *mockAccountRepository) GetAccountWithProxy(ctx context.Context, accountID uuid.UUID) (domain.AccountWithProxy, error) {
	if m.getWithProxyFn != nil {
		return m.getWithProxyFn(ctx, accountID)
	}
	return domain.AccountWithProxy{}, nil
}

func (m *mockAccountRepository) OperationalStateSnapshot(
	ctx context.Context,
) (map[domain.AccountOperationalState]int64, error) {
	return map[domain.AccountOperationalState]int64{}, nil
}

func (m *mockAccountRepository) UpdateAccountState(
	ctx context.Context,
	accountID uuid.UUID,
	state domain.AccountOperationalState,
	isReady bool,
	isQuarantined bool,
	isRestricted bool,
	limitReached bool,
) error {
	if m.updateStateFn != nil {
		return m.updateStateFn(ctx, accountID, state, isReady, isQuarantined, isRestricted, limitReached)
	}
	return nil
}

func (m *mockAccountRepository) CheckEligibility(ctx context.Context, accountID uuid.UUID) (domain.EligibilityDecision, error) {
	if m.checkEligibilityFn != nil {
		return m.checkEligibilityFn(ctx, accountID)
	}
	return domain.EligibilityDecision{}, nil
}

func (m *mockAccountRepository) ClaimAccount(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.Account, error) {
	if m.claimAccountFn != nil {
		return m.claimAccountFn(ctx, accountID, executionContextID)
	}
	return domain.Account{}, nil
}

func (m *mockAccountRepository) ReleaseAccount(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
	if m.releaseAccountFn != nil {
		return m.releaseAccountFn(ctx, accountID, executionContextID)
	}
	return nil
}

func newTestGuard(repository *mockAccountRepository) *AccountGuard {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewAccountGuard(repository, domain.DefaultRuntimeGuardrails(), logger)
}

func TestAcquireReturnsCombinedErrorWhenRollbackFails(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	primaryErr := errors.New("load account with proxy failed")
	releaseErr := domain.NewDomainError(domain.ErrorCodeAccountContextMismatch, "release failed")

	repository := &mockAccountRepository{
		checkEligibilityFn: func(ctx context.Context, accountID uuid.UUID) (domain.EligibilityDecision, error) {
			return domain.EligibilityDecision{
				Eligible:   true,
				ReasonCode: domain.ErrorCodeEligible,
			}, nil
		},
		claimAccountFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.Account, error) {
			return domain.Account{ID: accountID}, nil
		},
		getWithProxyFn: func(ctx context.Context, accountID uuid.UUID) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{}, primaryErr
		},
		releaseAccountFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
			return releaseErr
		},
	}

	guard := newTestGuard(repository)
	_, err := guard.Acquire(context.Background(), accountID, "exec-1")
	if err == nil {
		t.Fatal("expected acquire error")
	}
	if !errors.Is(err, primaryErr) {
		t.Fatalf("expected joined error to include primary fetch error, got %v", err)
	}
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeAccountContextMismatch) {
		t.Fatalf("expected joined error to include release context mismatch code, got %v", err)
	}
}

func TestAcquireAllowsProxylessAccount(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	repository := &mockAccountRepository{
		checkEligibilityFn: func(ctx context.Context, accountID uuid.UUID) (domain.EligibilityDecision, error) {
			return domain.EligibilityDecision{
				Eligible:   true,
				Outcome:    domain.EligibilityOutcomeEligible,
				ReasonCode: domain.ErrorCodeEligible,
			}, nil
		},
		claimAccountFn: func(ctx context.Context, accountID uuid.UUID, executionContextID string) (domain.Account, error) {
			return domain.Account{
				ID:                       accountID,
				ActiveExecutionContextID: executionContextID,
			}, nil
		},
		getWithProxyFn: func(ctx context.Context, accountID uuid.UUID) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{
				Account: domain.Account{
					ID: accountID,
				},
			}, nil
		},
	}

	guard := newTestGuard(repository)
	acquired, err := guard.Acquire(context.Background(), accountID, "exec-proxy-off-01")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if acquired.Account.ID != accountID {
		t.Fatalf("expected account id %s, got %s", accountID.String(), acquired.Account.ID.String())
	}
	if acquired.Account.ProxyID != uuid.Nil {
		t.Fatalf("expected no proxy binding, got proxy_id=%s", acquired.Account.ProxyID.String())
	}
}

func TestMarkQuarantinedSetsOnlyQuarantineFlags(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()

	var gotState domain.AccountOperationalState
	var gotIsReady bool
	var gotIsQuarantined bool
	var gotIsRestricted bool
	var gotLimitReached bool

	repository := &mockAccountRepository{
		updateStateFn: func(
			ctx context.Context,
			accountID uuid.UUID,
			state domain.AccountOperationalState,
			isReady bool,
			isQuarantined bool,
			isRestricted bool,
			limitReached bool,
		) error {
			gotState = state
			gotIsReady = isReady
			gotIsQuarantined = isQuarantined
			gotIsRestricted = isRestricted
			gotLimitReached = limitReached
			return nil
		},
	}

	guard := newTestGuard(repository)
	if err := guard.MarkQuarantined(context.Background(), accountID); err != nil {
		t.Fatalf("MarkQuarantined() error = %v", err)
	}

	if gotState != domain.AccountStateQuarantined {
		t.Fatalf("expected state %s, got %s", domain.AccountStateQuarantined, gotState)
	}
	if gotIsReady {
		t.Fatal("expected isReady=false")
	}
	if !gotIsQuarantined {
		t.Fatal("expected isQuarantined=true")
	}
	if gotIsRestricted {
		t.Fatal("expected isRestricted=false")
	}
	if gotLimitReached {
		t.Fatal("expected limitReached=false")
	}
}

func TestResolveErrorCodeReturnsInternalForUnknownError(t *testing.T) {
	t.Parallel()

	if got := resolveErrorCode(errors.New("unexpected")); got != domain.ErrorCodeInternal {
		t.Fatalf("expected %s, got %s", domain.ErrorCodeInternal, got)
	}

	if got := resolveErrorCode(domain.NewDomainError(domain.ErrorCodeAccountBusy, "busy")); got != domain.ErrorCodeAccountBusy {
		t.Fatalf("expected %s, got %s", domain.ErrorCodeAccountBusy, got)
	}
}

func TestAcquireQuarantinesAccountWhenLimitReached(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	updateCalled := false

	repository := &mockAccountRepository{
		checkEligibilityFn: func(ctx context.Context, accountID uuid.UUID) (domain.EligibilityDecision, error) {
			return domain.EligibilityDecision{
				Eligible:   false,
				Outcome:    domain.EligibilityOutcomeExcluded,
				ReasonCode: domain.ErrorCodeAccountLimitReached,
			}, nil
		},
		updateStateFn: func(
			ctx context.Context,
			gotAccountID uuid.UUID,
			state domain.AccountOperationalState,
			isReady bool,
			isQuarantined bool,
			isRestricted bool,
			limitReached bool,
		) error {
			updateCalled = true
			if gotAccountID != accountID {
				t.Fatalf("expected account id %s, got %s", accountID.String(), gotAccountID.String())
			}
			if state != domain.AccountStateQuarantined {
				t.Fatalf("expected state %s, got %s", domain.AccountStateQuarantined, state)
			}
			if !isQuarantined {
				t.Fatal("expected isQuarantined=true")
			}
			if !limitReached {
				t.Fatal("expected limitReached=true")
			}
			return nil
		},
	}

	guard := newTestGuard(repository)
	_, err := guard.Acquire(context.Background(), accountID, "exec-limit-01")
	if err == nil {
		t.Fatal("expected Acquire() to reject limit-reached account")
	}
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeAccountLimitReached) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeAccountLimitReached, err)
	}
	if !updateCalled {
		t.Fatal("expected account state update for limit-reached account")
	}
}

func TestAcquireMarksAccountRestrictedOnThreshold(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	updateCalled := false

	repository := &mockAccountRepository{
		checkEligibilityFn: func(ctx context.Context, accountID uuid.UUID) (domain.EligibilityDecision, error) {
			return domain.EligibilityDecision{
				Eligible:   false,
				Outcome:    domain.EligibilityOutcomeRestricted,
				ReasonCode: domain.ErrorCodeAccountRestricted,
			}, nil
		},
		updateStateFn: func(
			ctx context.Context,
			gotAccountID uuid.UUID,
			state domain.AccountOperationalState,
			isReady bool,
			isQuarantined bool,
			isRestricted bool,
			limitReached bool,
		) error {
			updateCalled = true
			if gotAccountID != accountID {
				t.Fatalf("expected account id %s, got %s", accountID.String(), gotAccountID.String())
			}
			if state != domain.AccountStateRestricted {
				t.Fatalf("expected state %s, got %s", domain.AccountStateRestricted, state)
			}
			if !isRestricted {
				t.Fatal("expected isRestricted=true")
			}
			if limitReached {
				t.Fatal("expected limitReached=false")
			}
			return nil
		},
	}

	guard := newTestGuard(repository)
	_, err := guard.Acquire(context.Background(), accountID, "exec-threshold-01")
	if err == nil {
		t.Fatal("expected Acquire() to reject restricted account")
	}
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeAccountRestricted) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeAccountRestricted, err)
	}
	if !updateCalled {
		t.Fatal("expected account state update for restricted account")
	}
}

func TestAcquireDefaultsOutcomeForIneligibleDecision(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	repository := &mockAccountRepository{
		checkEligibilityFn: func(ctx context.Context, accountID uuid.UUID) (domain.EligibilityDecision, error) {
			return domain.EligibilityDecision{
				Eligible:   false,
				ReasonCode: domain.ErrorCodeAccountNotFound,
			}, nil
		},
	}

	guard := newTestGuard(repository)
	_, err := guard.Acquire(context.Background(), accountID, "exec-outcome-default")
	if err == nil {
		t.Fatal("expected Acquire() to reject ineligible account")
	}
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeAccountNotFound) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeAccountNotFound, err)
	}
	if !strings.Contains(err.Error(), "outcome=excluded") {
		t.Fatalf("expected normalized excluded outcome in message, got %v", err)
	}
}

func TestAcquireReturnsCombinedErrorWhenQuotaApplyFails(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	applyErr := errors.New("update account state failed")

	repository := &mockAccountRepository{
		checkEligibilityFn: func(ctx context.Context, accountID uuid.UUID) (domain.EligibilityDecision, error) {
			return domain.EligibilityDecision{
				Eligible:   false,
				Outcome:    domain.EligibilityOutcomeExcluded,
				ReasonCode: domain.ErrorCodeAccountLimitReached,
			}, nil
		},
		getWithProxyFn: func(ctx context.Context, accountID uuid.UUID) (domain.AccountWithProxy, error) {
			return domain.AccountWithProxy{
				Account: domain.Account{
					ID:               accountID,
					OperationalState: domain.AccountStateActive,
					IsReady:          true,
				},
			}, nil
		},
		updateStateFn: func(
			ctx context.Context,
			gotAccountID uuid.UUID,
			state domain.AccountOperationalState,
			isReady bool,
			isQuarantined bool,
			isRestricted bool,
			limitReached bool,
		) error {
			return applyErr
		},
	}

	guard := newTestGuard(repository)
	_, err := guard.Acquire(context.Background(), accountID, "exec-apply-fail")
	if err == nil {
		t.Fatal("expected Acquire() to return apply error")
	}
	if !errors.Is(err, applyErr) {
		t.Fatalf("expected joined error to include apply error, got %v", err)
	}
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeAccountLimitReached) {
		t.Fatalf("expected joined error to include decision reason code, got %v", err)
	}
}
