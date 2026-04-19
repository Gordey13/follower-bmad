package domain

import (
	"regexp"
	"strings"
)

const OskellyTargetProfileURLPattern = `^https://oskelly\.ru/profile/[0-9]+$`

var oskellyTargetProfileURLMatcher = regexp.MustCompile(OskellyTargetProfileURLPattern)

func NormalizeOskellyTargetProfileURL(target TargetProfileDescriptor) (string, error) {
	normalized := strings.TrimSpace(string(target))
	if normalized == "" {
		return "", NewDomainError(
			ErrorCodeFollowTargetProfile,
			"target_profile must not be empty",
		)
	}
	if !oskellyTargetProfileURLMatcher.MatchString(normalized) {
		return "", NewDomainError(
			ErrorCodeFollowTargetProfile,
			"target_profile must match https://oskelly.ru/profile/<NUM>",
		)
	}
	return normalized, nil
}
