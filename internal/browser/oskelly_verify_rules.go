package browser

import "time"

type oskellyVerifySelectorRules struct {
	ReadySignals             []string
	FollowConfirmedSignals   []string
	ActionUnavailableSignals []string
	TargetUnreachableSignals []string
}

type oskellyVerifyTimeoutRules struct {
	Navigation time.Duration
	Readiness  time.Duration
}

type oskellyVerifyRules struct {
	Selectors oskellyVerifySelectorRules
	Timeouts  oskellyVerifyTimeoutRules
}

var defaultOskellyVerifyRules = oskellyVerifyRules{
	Selectors: oskellyVerifySelectorRules{
		ReadySignals: []string{
			"main",
			"[class*='profile']",
			"section[class*='profile']",
			"[data-testid='profile']",
		},
		FollowConfirmedSignals: []string{
			"button:has-text('Following')",
			"button:has-text('Followed')",
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u043D (-\u0430)')",
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u043D')",
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u043D\u044B')",
			"button:has-text('\u0412\u044B \u043F\u043E\u0434\u043F\u0438\u0441\u0430\u043D\u044B')",
			"button:has-text('\u041E\u0442\u043F\u0438\u0441\u0430\u0442\u044C\u0441\u044F')",
		},
		ActionUnavailableSignals: []string{
			"[data-testid='follow-button'][disabled]",
			"button[disabled]:has-text('Follow')",
			"button[disabled]:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u0442\u044C\u0441\u044F')",
			"[class*='follow'] button[disabled]",
		},
		TargetUnreachableSignals: []string{
			"[data-testid='profile-not-found']",
			".error-404",
			"text=Profile not found",
			"text=\u041F\u0440\u043E\u0444\u0438\u043B\u044C \u043D\u0435 \u043D\u0430\u0439\u0434\u0435\u043D",
		},
	},
	Timeouts: oskellyVerifyTimeoutRules{
		Navigation: 45 * time.Second,
		Readiness:  15 * time.Second,
	},
}

func playwrightVerifyReadySelectors() []string {
	combined := make([]string, 0, len(defaultOskellyVerifyRules.Selectors.ReadySignals)+len(defaultOskellyVerifyRules.Selectors.FollowConfirmedSignals)+len(defaultOskellyVerifyRules.Selectors.ActionUnavailableSignals)+len(defaultOskellyVerifyRules.Selectors.TargetUnreachableSignals))
	combined = append(combined, defaultOskellyVerifyRules.Selectors.ReadySignals...)
	combined = append(combined, defaultOskellyVerifyRules.Selectors.FollowConfirmedSignals...)
	combined = append(combined, defaultOskellyVerifyRules.Selectors.ActionUnavailableSignals...)
	combined = append(combined, defaultOskellyVerifyRules.Selectors.TargetUnreachableSignals...)
	return combined
}
