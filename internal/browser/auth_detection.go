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
	if len(selectors) > 0 && hasAnyVisibleSelector(page, selectors) {
		return true
	}
	return false
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

func hasAnyVisibleSelector(page playwright.Page, selectors []string) bool {
	for _, selector := range selectors {
		visible, err := page.IsVisible(selector)
		if err != nil {
			continue
		}
		if visible {
			return true
		}
	}
	return false
}
