package domain

type AccountOperationalState string

const (
	AccountStateActive         AccountOperationalState = "active"
	AccountStateBusy           AccountOperationalState = "busy"
	AccountStateInvalidSession AccountOperationalState = "invalid_session"
	AccountStateQuarantined    AccountOperationalState = "quarantined"
	AccountStateRestricted     AccountOperationalState = "restricted"
)

func (state AccountOperationalState) IsValid() bool {
	switch state {
	case AccountStateActive,
		AccountStateBusy,
		AccountStateInvalidSession,
		AccountStateQuarantined,
		AccountStateRestricted:
		return true
	default:
		return false
	}
}
