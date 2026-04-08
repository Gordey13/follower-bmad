package domain

import (
	"time"

	"github.com/google/uuid"
)

type Account struct {
	ID                       uuid.UUID
	Username                 string
	ProxyID                  uuid.UUID
	CredentialSource         CredentialSource
	CredentialRef            string
	OperationalState         AccountOperationalState
	IsActive                 bool
	IsReady                  bool
	IsQuarantined            bool
	IsRestricted             bool
	LimitReached             bool
	ActiveExecutionContextID string
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type AccountWithProxy struct {
	Account Account
	Proxy   Proxy
}

func (a Account) Limits() AccountLimits {
	return AccountLimits{
		LimitReached:               a.LimitReached,
		RestrictiveThresholdRaised: a.IsRestricted || a.OperationalState == AccountStateRestricted,
	}
}
