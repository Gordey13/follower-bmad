package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"follower/internal/audit"
	"follower/internal/domain"
	"follower/internal/repository"

	"github.com/google/uuid"
)

type AccountGuard struct {
	repository repository.AccountRepository
	quota      *QuotaService
	logger     *slog.Logger
}

func NewAccountGuard(
	repository repository.AccountRepository,
	guardrails domain.RuntimeGuardrails,
	logger *slog.Logger,
) *AccountGuard {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}

	return &AccountGuard{
		repository: repository,
		quota:      NewQuotaService(repository, guardrails),
		logger:     logger,
	}
}

func (g *AccountGuard) Acquire(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
) (domain.AccountWithProxy, error) {
	decision, err := g.repository.CheckEligibility(ctx, accountID)
	if err != nil {
		return domain.AccountWithProxy{}, err
	}
	if !decision.Eligible {
		decision = normalizeEligibilityDecision(decision)

		if applyErr := g.quota.ApplyDecision(ctx, accountID, decision); applyErr != nil {
			g.logger.Error("failed to apply account quota decision",
				"account_id", accountID.String(),
				"error_code", resolveErrorCode(applyErr),
			)
			return domain.AccountWithProxy{}, errors.Join(
				domain.NewDomainError(
					decision.ReasonCode,
					fmt.Sprintf("account eligibility outcome=%s", decision.Outcome),
				),
				fmt.Errorf("apply quota decision: %w", applyErr),
			)
		}

		g.logger.Warn(g.quota.EventName(decision),
			"account_id", accountID.String(),
			"eligibility_outcome", decision.Outcome,
			"error_code", decision.ReasonCode,
		)
		return domain.AccountWithProxy{}, domain.NewDomainError(
			decision.ReasonCode,
			fmt.Sprintf("account eligibility outcome=%s", decision.Outcome),
		)
	}

	if _, err := g.repository.ClaimAccount(ctx, accountID, executionContextID); err != nil {
		g.logger.Warn("account claim rejected",
			"account_id", accountID.String(),
			"error_code", resolveErrorCode(err),
		)
		return domain.AccountWithProxy{}, err
	}

	accountWithProxy, err := g.repository.GetAccountWithProxy(ctx, accountID)
	if err != nil {
		releaseErr := g.repository.ReleaseAccount(ctx, accountID, executionContextID)
		if releaseErr != nil {
			g.logger.Error("failed to rollback claimed account",
				"account_id", accountID.String(),
				"error_code", resolveErrorCode(releaseErr),
			)
			return domain.AccountWithProxy{}, errors.Join(
				err,
				fmt.Errorf("failed to release claimed account: %w", releaseErr),
			)
		}
		return domain.AccountWithProxy{}, err
	}

	proxyID := ""
	if accountWithProxy.Account.ProxyID != uuid.Nil {
		proxyID = accountWithProxy.Account.ProxyID.String()
	}

	g.logger.Info("account acquired",
		"account_id", accountID.String(),
		"proxy_bound", accountWithProxy.Account.ProxyID != uuid.Nil,
		"proxy_id", proxyID,
	)

	return accountWithProxy, nil
}

func (g *AccountGuard) Release(ctx context.Context, accountID uuid.UUID, executionContextID string) error {
	if err := g.repository.ReleaseAccount(ctx, accountID, executionContextID); err != nil {
		return err
	}

	g.logger.Info("account released", "account_id", accountID.String())
	return nil
}

func (g *AccountGuard) MarkQuarantined(ctx context.Context, accountID uuid.UUID) error {
	err := g.repository.UpdateAccountState(
		audit.WithActor(ctx, audit.Actor{
			Type: audit.ActorTypeInternalProcess,
			ID:   "worker.account_guard",
		}),
		accountID,
		domain.AccountStateQuarantined,
		false,
		true,
		false,
		false,
	)
	if err != nil {
		return err
	}

	g.logger.Warn("account.quarantined",
		"account_id", accountID.String(),
		"error_code", domain.ErrorCodeAccountQuarantined,
	)

	return nil
}

func resolveErrorCode(err error) domain.ErrorCode {
	var domainErr *domain.DomainError
	if errors.As(err, &domainErr) {
		return domainErr.Code
	}
	return domain.ErrorCodeInternal
}

func normalizeEligibilityDecision(decision domain.EligibilityDecision) domain.EligibilityDecision {
	if decision.Eligible {
		if decision.Outcome == "" {
			decision.Outcome = domain.EligibilityOutcomeEligible
		}
		if decision.ReasonCode == "" {
			decision.ReasonCode = domain.ErrorCodeEligible
		}
		return decision
	}

	if decision.Outcome == "" {
		decision.Outcome = domain.EligibilityOutcomeExcluded
	}

	return decision
}
