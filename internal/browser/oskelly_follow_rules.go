package browser

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"follower/internal/domain"
)

type oskellyFollowSelectorRules struct {
	ReadySignals       []string
	AlreadyDoneSignals []string
	FollowControlPaths []string
	UnavailableSignals []string
	PostFollowSignals  []string
}

type oskellyFollowTimeoutRules struct {
	Navigation time.Duration
	Readiness  time.Duration
	PostClick  time.Duration
}

type oskellyFollowRules struct {
	Selectors oskellyFollowSelectorRules
	Timeouts  oskellyFollowTimeoutRules
}

const (
	oskellyProfileURLPattern = `^https://oskelly\.ru/profile/[0-9]+$`
	oskellyProfilePathRegex  = `^/profile/[0-9]+$`
)

var (
	oskellyProfileURLMatcher  = regexp.MustCompile(oskellyProfileURLPattern)
	oskellyProfilePathMatcher = regexp.MustCompile(oskellyProfilePathRegex)
)

var defaultOskellyFollowRules = oskellyFollowRules{
	Selectors: oskellyFollowSelectorRules{
		ReadySignals: []string{
			"main",
			"[class*='profile']",
			"section[class*='profile']",
			"[data-testid='profile']",
		},
		AlreadyDoneSignals: []string{
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u043D (-\u0430)')",
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u043D')",
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u043D\u044B')",
			"button:has-text('\u0412\u044B \u043F\u043E\u0434\u043F\u0438\u0441\u0430\u043D\u044B')",
			"button:has-text('\u041E\u0442\u043F\u0438\u0441\u0430\u0442\u044C\u0441\u044F')",
			"button:has-text('Following')",
		},
		FollowControlPaths: []string{
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u0442\u044C\u0441\u044F')",
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u0442\u044C\u0441\u044F \u043D\u0430 \u043F\u0440\u043E\u0434\u0430\u0432\u0446\u0430')",
			"button:has-text('Follow')",
			"button[data-testid='follow-button']",
			"[class*='follow'] button",
		},
		UnavailableSignals: []string{
			"[data-testid='profile-not-found']",
			".error-404",
			"text=\u041F\u0440\u043E\u0444\u0438\u043B\u044C \u043D\u0435 \u043D\u0430\u0439\u0434\u0435\u043D",
			"text=Profile not found",
		},
		PostFollowSignals: []string{
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u043D (-\u0430)')",
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u043D')",
			"button:has-text('\u041F\u043E\u0434\u043F\u0438\u0441\u0430\u043D\u044B')",
			"button:has-text('\u0412\u044B \u043F\u043E\u0434\u043F\u0438\u0441\u0430\u043D\u044B')",
			"button:has-text('\u041E\u0442\u043F\u0438\u0441\u0430\u0442\u044C\u0441\u044F')",
			"button:has-text('Following')",
		},
	},
	Timeouts: oskellyFollowTimeoutRules{
		Navigation: 45 * time.Second,
		Readiness:  15 * time.Second,
		PostClick:  10 * time.Second,
	},
}

func normalizeOskellyTargetProfileURL(
	target domain.TargetProfileDescriptor,
) (string, error) {
	normalized := strings.TrimSpace(string(target))
	if normalized == "" {
		return "", domain.NewDomainError(
			domain.ErrorCodeFollowTargetProfile,
			"target_profile must not be empty",
		)
	}
	if !oskellyProfileURLMatcher.MatchString(normalized) {
		return "", domain.NewDomainError(
			domain.ErrorCodeFollowTargetProfile,
			"target_profile must match https://oskelly.ru/profile/<NUM>",
		)
	}
	return normalized, nil
}

func isResolvedOskellyProfileURL(rawURL string) bool {
	normalized := strings.TrimSpace(strings.ToLower(rawURL))
	if normalized == "" {
		return false
	}
	if !strings.HasPrefix(normalized, "https://oskelly.ru") && !strings.HasPrefix(normalized, "http://oskelly.ru") {
		return false
	}

	pathStart := strings.Index(normalized, "://oskelly.ru")
	if pathStart < 0 {
		return false
	}
	path := normalized[pathStart+len("://oskelly.ru"):]
	if queryIdx := strings.IndexAny(path, "?#"); queryIdx >= 0 {
		path = path[:queryIdx]
	}

	return oskellyProfilePathMatcher.MatchString(path)
}

func normalizePlaywrightFollowError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return domain.NewDomainError(
			domain.ErrorCodeFollowNavigationFailed,
			"playwright follow flow timed out",
		)
	}

	var domainErr *domain.DomainError
	if errors.As(err, &domainErr) {
		switch domainErr.Code {
		case domain.ErrorCodeFollowTargetProfile,
			domain.ErrorCodeSessionPayloadInvalid,
			domain.ErrorCodeFollowActionUnavailable,
			domain.ErrorCodeFollowTargetUnreachable,
			domain.ErrorCodeFollowNavigationFailed:
			return domainErr
		}
	}

	lowered := strings.ToLower(err.Error())
	if containsAny(lowered, []string{
		"net::err_name_not_resolved",
		"net::err_connection",
		"net::err_internet_disconnected",
		"status=404",
		"status=410",
		"not found",
		"target unreachable",
	}) {
		return domain.NewDomainError(
			domain.ErrorCodeFollowTargetUnreachable,
			"target profile is unreachable",
		)
	}
	if containsAny(lowered, []string{
		"not visible",
		"not enabled",
		"element is disabled",
		"action unavailable",
	}) {
		return domain.NewDomainError(
			domain.ErrorCodeFollowActionUnavailable,
			"follow action is unavailable",
		)
	}

	return domain.NewDomainError(
		domain.ErrorCodeFollowNavigationFailed,
		"playwright follow flow failed",
	)
}
