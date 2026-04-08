package domain

type EligibilityOutcome string

const (
	EligibilityOutcomeEligible   EligibilityOutcome = "eligible"
	EligibilityOutcomeExcluded   EligibilityOutcome = "excluded"
	EligibilityOutcomeRestricted EligibilityOutcome = "restricted"
)

type AccountLimits struct {
	LimitReached               bool
	RestrictiveThresholdRaised bool
}

type RuntimeGuardrails struct {
	ExcludeWhenLimitReached      bool
	RestrictWhenThresholdReached bool
	QuarantineOnLimitReached     bool
	RequireProxyBinding          bool
}

func DefaultRuntimeGuardrails() RuntimeGuardrails {
	return RuntimeGuardrails{
		ExcludeWhenLimitReached:      true,
		RestrictWhenThresholdReached: true,
		QuarantineOnLimitReached:     true,
		RequireProxyBinding:          true,
	}
}

func (g RuntimeGuardrails) Normalized() RuntimeGuardrails {
	defaults := DefaultRuntimeGuardrails()
	normalized := g

	if !normalized.ExcludeWhenLimitReached && !normalized.RestrictWhenThresholdReached {
		normalized.ExcludeWhenLimitReached = defaults.ExcludeWhenLimitReached
		normalized.RestrictWhenThresholdReached = defaults.RestrictWhenThresholdReached
		normalized.QuarantineOnLimitReached = defaults.QuarantineOnLimitReached
	}
	if g == (RuntimeGuardrails{}) {
		normalized.RequireProxyBinding = defaults.RequireProxyBinding
	}

	return normalized
}
