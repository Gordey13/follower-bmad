package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestEvaluateAccountEligibility(t *testing.T) {
	t.Parallel()

	proxy := &Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9000,
		IsActive: true,
	}

	baseAccount := Account{
		ID:               uuid.New(),
		Username:         "acc-01",
		ProxyID:          proxy.ID,
		OperationalState: AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}

	t.Run("eligible account", func(t *testing.T) {
		t.Parallel()

		decision := EvaluateAccountEligibility(baseAccount, proxy)
		if !decision.Eligible || decision.ReasonCode != ErrorCodeEligible {
			t.Fatalf("expected eligible decision, got %+v", decision)
		}
		if decision.Outcome != EligibilityOutcomeEligible {
			t.Fatalf("expected outcome %s, got %s", EligibilityOutcomeEligible, decision.Outcome)
		}
	})

	t.Run("missing proxy", func(t *testing.T) {
		t.Parallel()

		account := baseAccount
		account.ProxyID = uuid.Nil
		decision := EvaluateAccountEligibility(account, nil)

		if decision.Eligible || decision.ReasonCode != ErrorCodeAccountMissingProxy {
			t.Fatalf("expected missing proxy decision, got %+v", decision)
		}
	})

	t.Run("quarantined account", func(t *testing.T) {
		t.Parallel()

		account := baseAccount
		account.IsQuarantined = true
		account.OperationalState = AccountStateQuarantined
		decision := EvaluateAccountEligibility(account, proxy)

		if decision.Eligible || decision.ReasonCode != ErrorCodeAccountQuarantined {
			t.Fatalf("expected quarantined decision, got %+v", decision)
		}
	})

	t.Run("restricted account", func(t *testing.T) {
		t.Parallel()

		account := baseAccount
		account.IsRestricted = true
		account.OperationalState = AccountStateRestricted
		decision := EvaluateAccountEligibility(account, proxy)

		if decision.Eligible || decision.ReasonCode != ErrorCodeAccountRestricted {
			t.Fatalf("expected restricted decision, got %+v", decision)
		}
		if decision.Outcome != EligibilityOutcomeRestricted {
			t.Fatalf("expected outcome %s, got %s", EligibilityOutcomeRestricted, decision.Outcome)
		}
	})

	t.Run("limit reached account", func(t *testing.T) {
		t.Parallel()

		account := baseAccount
		account.LimitReached = true
		decision := EvaluateAccountEligibility(account, proxy)

		if decision.Eligible || decision.ReasonCode != ErrorCodeAccountLimitReached {
			t.Fatalf("expected limit reached decision, got %+v", decision)
		}
		if decision.Outcome != EligibilityOutcomeExcluded {
			t.Fatalf("expected outcome %s, got %s", EligibilityOutcomeExcluded, decision.Outcome)
		}
	})

	t.Run("busy account", func(t *testing.T) {
		t.Parallel()

		account := baseAccount
		account.ActiveExecutionContextID = "ctx-1"
		account.OperationalState = AccountStateBusy
		decision := EvaluateAccountEligibility(account, proxy)

		if decision.Eligible || decision.ReasonCode != ErrorCodeAccountBusy {
			t.Fatalf("expected busy decision, got %+v", decision)
		}
	})
}

func TestEvaluateAccountEligibilityDeterministicWithGuardrails(t *testing.T) {
	t.Parallel()

	proxy := &Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9000,
		IsActive: true,
	}

	account := Account{
		ID:               uuid.New(),
		Username:         "acc-deterministic-01",
		ProxyID:          proxy.ID,
		OperationalState: AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}

	guardrails := RuntimeGuardrails{
		ExcludeWhenLimitReached:      true,
		RestrictWhenThresholdReached: true,
		QuarantineOnLimitReached:     true,
		RequireProxyBinding:          true,
	}

	first := EvaluateAccountEligibilityWithGuardrails(account, proxy, guardrails)
	second := EvaluateAccountEligibilityWithGuardrails(account, proxy, guardrails)

	if first != second {
		t.Fatalf("expected deterministic decision, first=%+v second=%+v", first, second)
	}
}

func TestEvaluateAccountEligibilityWithGuardrailsProxyModes(t *testing.T) {
	t.Parallel()

	account := Account{
		ID:               uuid.New(),
		Username:         "acc-proxy-mode-01",
		OperationalState: AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}

	t.Run("proxy binding required rejects missing proxy", func(t *testing.T) {
		t.Parallel()

		decision := EvaluateAccountEligibilityWithGuardrails(
			account,
			nil,
			RuntimeGuardrails{
				ExcludeWhenLimitReached:      true,
				RestrictWhenThresholdReached: true,
				QuarantineOnLimitReached:     true,
				RequireProxyBinding:          true,
			},
		)
		if decision.Eligible || decision.ReasonCode != ErrorCodeAccountMissingProxy {
			t.Fatalf("expected missing proxy decision, got %+v", decision)
		}
	})

	t.Run("proxy binding disabled allows missing proxy", func(t *testing.T) {
		t.Parallel()

		decision := EvaluateAccountEligibilityWithGuardrails(
			account,
			nil,
			RuntimeGuardrails{
				ExcludeWhenLimitReached:      true,
				RestrictWhenThresholdReached: true,
				QuarantineOnLimitReached:     true,
				RequireProxyBinding:          false,
			},
		)
		if !decision.Eligible || decision.ReasonCode != ErrorCodeEligible {
			t.Fatalf("expected eligible decision, got %+v", decision)
		}
	})
}
