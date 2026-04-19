package browser

import (
	"strings"

	"github.com/playwright-community/playwright-go"
)

func isAuthenticationRequiredPage(page playwright.Page, selectors []string) bool {
	if page == nil {
		return false
	}
	if isAuthenticationRequiredURL(page.URL()) {
		return true
	}
	if len(selectors) > 0 && hasAnySelector(page, selectors) {
		return true
	}
	return hasAuthenticationPromptText(page)
}

func isAuthenticationRequiredURL(pageURL string) bool {
	normalized := strings.ToLower(strings.TrimSpace(pageURL))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "/login") ||
		strings.Contains(normalized, "/auth") ||
		strings.Contains(normalized, "/authorization")
}

func hasAuthenticationPromptText(page playwright.Page) bool {
	content, err := page.Content()
	if err != nil {
		return false
	}
	normalized := strings.ToLower(content)
	return containsAny(normalized, []string{
		"войдите",
		"авториз",
		"sign in",
		"log in",
		"login",
	})
}
