package worker

import (
	"context"

	"follower/internal/audit"
	"follower/internal/domain"
	"follower/internal/repository"
	"follower/internal/stackerr"

	"github.com/google/uuid"
)

type QuotaService struct {
	repository repository.AccountRepository
	guardrails domain.RuntimeGuardrails
}

func NewQuotaService(
	repository repository.AccountRepository,
	guardrails domain.RuntimeGuardrails,
) *QuotaService {
	return &QuotaService{
		repository: repository,
		guardrails: guardrails.Normalized(),
	}
}

func (s *QuotaService) ApplyDecision(
	ctx context.Context,
	accountID uuid.UUID,
	decision domain.EligibilityDecision,
) error {
	if decision.Eligible {
		return nil
	}

	switch decision.ReasonCode {
	case domain.ErrorCodeAccountLimitReached:
		if !s.guardrails.QuarantineOnLimitReached {
			return nil
		}

		account, err := s.currentAccount(ctx, accountID)
		if err != nil {
			return stackerr.WithStack(err)
		}

		return s.repository.UpdateAccountState(
			audit.WithActor(ctx, audit.Actor{
				Type: audit.ActorTypeInternalProcess,
				ID:   "worker.quota_service",
			}),
			accountID,
			domain.AccountStateQuarantined,
			account.IsReady,
			true,
			account.IsRestricted,
			true,
		)
	case domain.ErrorCodeAccountRestricted:
		account, err := s.currentAccount(ctx, accountID)
		if err != nil {
			return stackerr.WithStack(err)
		}
		if account.IsQuarantined || account.OperationalState == domain.AccountStateQuarantined {
			return nil
		}

		return s.repository.UpdateAccountState(
			audit.WithActor(ctx, audit.Actor{
				Type: audit.ActorTypeInternalProcess,
				ID:   "worker.quota_service",
			}),
			accountID,
			domain.AccountStateRestricted,
			account.IsReady,
			account.IsQuarantined,
			true,
			account.LimitReached,
		)
	default:
		return nil
	}
}

func (s *QuotaService) EventName(decision domain.EligibilityDecision) string {
	switch decision.ReasonCode {
	case domain.ErrorCodeAccountLimitReached:
		if s.guardrails.QuarantineOnLimitReached {
			return "account.quarantined"
		}
		return "account.limit_reached"
	case domain.ErrorCodeAccountRestricted:
		return "account.restricted"
	default:
		return "account.eligibility_rejected"
	}
}

func (s *QuotaService) currentAccount(ctx context.Context, accountID uuid.UUID) (domain.Account, error) {
	accountWithProxy, err := s.repository.GetAccountWithProxy(ctx, accountID)
	if err != nil {
		return domain.Account{}, stackerr.WithStack(err)
	}

	return accountWithProxy.Account, nil
}
