package browser

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"follower/internal/domain"

	"github.com/playwright-community/playwright-go"
)

type defaultPlaywrightFollowAdapter struct{}

type playwrightFollowRuntime struct {
	playwrightInstance *playwright.Playwright
	browser            playwright.Browser
	browserContext     playwright.BrowserContext
	page               playwright.Page
	targetURL          string
}

func (a *defaultPlaywrightFollowAdapter) Warmup(
	ctx context.Context,
	input domain.FollowFlowInput,
) error {
	runtime, err := newPlaywrightFollowRuntime(ctx, input)
	if err != nil {
		return err
	}
	defer runtime.close()

	if isAuthenticationRequiredPage(runtime.page, defaultOskellyFollowRules.Selectors.AuthRequiredSignals) {
		return domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapRequired,
			"follow warmup requires authenticated session",
		)
	}

	if !waitAnySelectorWithContext(
		ctx,
		runtime.page,
		playwrightWarmupReadySelectors(),
		defaultOskellyFollowRules.Timeouts.Readiness,
	) {
		if isAuthenticationRequiredPage(runtime.page, defaultOskellyFollowRules.Selectors.AuthRequiredSignals) {
			return domain.NewDomainError(
				domain.ErrorCodeAuthBootstrapRequired,
				"follow warmup requires authenticated session",
			)
		}
		return domain.NewDomainError(
			domain.ErrorCodeFollowActionUnavailable,
			"follow warmup could not detect supported profile UI controls",
		)
	}

	return nil
}

func (a *defaultPlaywrightFollowAdapter) RunFollowAction(
	ctx context.Context,
	input domain.FollowFlowInput,
) (domain.FollowFlowOutcome, error) {
	runtime, err := newPlaywrightFollowRuntime(ctx, input)
	if err != nil {
		return "", err
	}
	defer runtime.close()

	if isAuthenticationRequiredPage(runtime.page, defaultOskellyFollowRules.Selectors.AuthRequiredSignals) {
		return "", domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapRequired,
			"follow flow requires authenticated session",
		)
	}

	if !waitAnySelectorWithContext(
		ctx,
		runtime.page,
		playwrightFollowInteractiveSelectors(),
		defaultOskellyFollowRules.Timeouts.Readiness,
	) {
		if isAuthenticationRequiredPage(runtime.page, defaultOskellyFollowRules.Selectors.AuthRequiredSignals) {
			return "", domain.NewDomainError(
				domain.ErrorCodeAuthBootstrapRequired,
				"follow flow requires authenticated session",
			)
		}
		return domain.FollowFlowOutcomeActionUnavailable, nil
	}

	state, selector, detectErr := detectPlaywrightFollowState(runtime.page)
	if detectErr != nil {
		return "", detectErr
	}

	switch state {
	case followControlStateAlreadyDone:
		return domain.FollowFlowOutcomeAlreadyDone, nil
	case followControlStateUnavailable:
		return domain.FollowFlowOutcomeActionUnavailable, nil
	case followControlStateActionable:
		if strings.TrimSpace(selector) == "" {
			return domain.FollowFlowOutcomeActionUnavailable, nil
		}
	default:
		return domain.FollowFlowOutcomeNavigationFailed, nil
	}

	const maxClickAttempts = 2
	for attempt := 1; attempt <= maxClickAttempts; attempt++ {
		clicked, clickErr := clickVisibleFollowControl(runtime.page, selector)
		if clickErr != nil {
			return "", normalizePlaywrightFollowError(clickErr)
		}
		if !clicked {
			return domain.FollowFlowOutcomeActionUnavailable, nil
		}

		if waitAnySelectorWithContext(
			ctx,
			runtime.page,
			defaultOskellyFollowRules.Selectors.PostFollowSignals,
			defaultOskellyFollowRules.Timeouts.PostClick,
		) {
			return domain.FollowFlowOutcomeCompleted, nil
		}
		if hasFollowConfirmedButtonText(runtime.page) {
			return domain.FollowFlowOutcomeCompleted, nil
		}
		if waitAnySelectorWithContext(
			ctx,
			runtime.page,
			defaultOskellyFollowRules.Selectors.UnavailableSignals,
			2*time.Second,
		) {
			return domain.FollowFlowOutcomeActionUnavailable, nil
		}
		if isAuthenticationRequiredPage(runtime.page, defaultOskellyFollowRules.Selectors.AuthRequiredSignals) {
			return "", domain.NewDomainError(
				domain.ErrorCodeAuthBootstrapRequired,
				"follow flow requires authenticated session",
			)
		}

		if attempt < maxClickAttempts && hasSubscribeButtonText(runtime.page) {
			time.Sleep(800 * time.Millisecond)
			continue
		}
	}

	// Keep runtime failures deterministic and machine-readable.
	return domain.FollowFlowOutcomeNavigationFailed, nil
}

func newPlaywrightFollowRuntime(
	ctx context.Context,
	input domain.FollowFlowInput,
) (*playwrightFollowRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	targetURL, err := normalizeOskellyTargetProfileURL(input.TargetProfile)
	if err != nil {
		return nil, err
	}

	storageState, err := parsePlaywrightStorageStatePayload(input.SessionPayload)
	if err != nil {
		return nil, err
	}

	pw, err := playwright.Run()
	if err != nil {
		return nil, err
	}

	browserInstance, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		_ = pw.Stop()
		return nil, err
	}

	browserContext, err := browserInstance.NewContext(playwright.BrowserNewContextOptions{
		StorageState: storageState,
	})
	if err != nil {
		_ = browserInstance.Close()
		_ = pw.Stop()
		return nil, err
	}

	page, err := browserContext.NewPage()
	if err != nil {
		_ = browserContext.Close()
		_ = browserInstance.Close()
		_ = pw.Stop()
		return nil, err
	}

	response, err := page.Goto(targetURL, playwright.PageGotoOptions{
		Timeout:   playwright.Float(float64(defaultOskellyFollowRules.Timeouts.Navigation.Milliseconds())),
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	if err != nil {
		_ = browserContext.Close()
		_ = browserInstance.Close()
		_ = pw.Stop()
		return nil, normalizePlaywrightFollowError(err)
	}
	if response != nil && response.Status() >= 400 {
		_ = browserContext.Close()
		_ = browserInstance.Close()
		_ = pw.Stop()
		return nil, domain.NewDomainError(
			domain.ErrorCodeFollowTargetUnreachable,
			fmt.Sprintf("target profile navigation failed with status=%d", response.Status()),
		)
	}
	if isAuthenticationRequiredPage(page, defaultOskellyFollowRules.Selectors.AuthRequiredSignals) {
		_ = browserContext.Close()
		_ = browserInstance.Close()
		_ = pw.Stop()
		return nil, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapRequired,
			"session restore requires authentication before follow flow",
		)
	}
	if !isResolvedOskellyProfileURL(page.URL()) {
		_ = browserContext.Close()
		_ = browserInstance.Close()
		_ = pw.Stop()
		return nil, domain.NewDomainError(
			domain.ErrorCodeFollowTargetUnreachable,
			"navigated page is not a supported oskelly profile",
		)
	}

	return &playwrightFollowRuntime{
		playwrightInstance: pw,
		browser:            browserInstance,
		browserContext:     browserContext,
		page:               page,
		targetURL:          targetURL,
	}, nil
}

func (runtime *playwrightFollowRuntime) close() {
	if runtime == nil {
		return
	}
	if runtime.browserContext != nil {
		_ = runtime.browserContext.Close()
	}
	if runtime.browser != nil {
		_ = runtime.browser.Close()
	}
	if runtime.playwrightInstance != nil {
		_ = runtime.playwrightInstance.Stop()
	}
}

type followControlState int

const (
	followControlStateUnknown followControlState = iota
	followControlStateActionable
	followControlStateAlreadyDone
	followControlStateUnavailable
)

func detectPlaywrightFollowState(page playwright.Page) (followControlState, string, error) {
	if hasAnySelector(page, defaultOskellyFollowRules.Selectors.AlreadyDoneSignals) {
		return followControlStateAlreadyDone, "", nil
	}
	if hasAnySelector(page, defaultOskellyFollowRules.Selectors.UnavailableSignals) {
		return followControlStateUnavailable, "", nil
	}

	selector, exists, err := firstVisibleSelector(page, defaultOskellyFollowRules.Selectors.FollowControlPaths)
	if err != nil {
		return followControlStateUnknown, "", err
	}
	if !exists {
		return followControlStateUnavailable, "", nil
	}

	enabled, err := selectorIsEnabled(page, selector)
	if err != nil {
		return followControlStateUnknown, "", err
	}
	if !enabled {
		return followControlStateUnavailable, selector, nil
	}

	return followControlStateActionable, selector, nil
}

func firstVisibleSelector(
	page playwright.Page,
	selectors []string,
) (string, bool, error) {
	for _, selector := range selectors {
		visible, err := selectorHasVisibleElement(page, selector)
		if err != nil {
			return "", false, err
		}
		if !visible {
			continue
		}
		return selector, true, nil
	}
	return "", false, nil
}

func selectorIsEnabled(page playwright.Page, selector string) (bool, error) {
	locator := page.Locator(selector).First()
	count, err := page.Locator(selector).Count()
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}

	isEnabled, err := locator.IsEnabled()
	if err != nil {
		return false, err
	}
	if !isEnabled {
		return false, nil
	}

	ariaDisabled, err := locator.GetAttribute("aria-disabled")
	if err == nil && strings.EqualFold(strings.TrimSpace(ariaDisabled), "true") {
		return false, nil
	}

	disabledAttr, err := locator.GetAttribute("disabled")
	if err == nil && strings.TrimSpace(disabledAttr) != "" {
		return false, nil
	}

	classAttr, err := locator.GetAttribute("class")
	if err == nil && containsAny(strings.ToLower(classAttr), []string{"disabled", "is-disabled"}) {
		return false, nil
	}

	return true, nil
}

func hasAnySelector(page playwright.Page, selectors []string) bool {
	for _, selector := range selectors {
		visible, err := selectorHasVisibleElement(page, selector)
		if err == nil && visible {
			return true
		}
	}
	return false
}

func selectorHasVisibleElement(page playwright.Page, selector string) (bool, error) {
	locator := page.Locator(selector)
	count, err := locator.Count()
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}

	limit := count
	if limit > 5 {
		limit = 5
	}
	for idx := 0; idx < limit; idx++ {
		visible, visibleErr := locator.Nth(idx).IsVisible()
		if visibleErr != nil {
			continue
		}
		if visible {
			return true, nil
		}
	}

	return false, nil
}

func hasFollowConfirmedButtonText(page playwright.Page) bool {
	texts, err := pageButtonTexts(page)
	if err != nil || len(texts) == 0 {
		return false
	}
	for _, text := range texts {
		normalized := strings.ToLower(strings.TrimSpace(text))
		if normalized == "" {
			continue
		}
		if strings.Contains(normalized, "подписан") ||
			strings.Contains(normalized, "following") ||
			strings.Contains(normalized, "followed") ||
			strings.Contains(normalized, "отписаться") {
			return true
		}
	}
	return false
}

func hasSubscribeButtonText(page playwright.Page) bool {
	texts, err := pageButtonTexts(page)
	if err != nil || len(texts) == 0 {
		return false
	}
	for _, text := range texts {
		normalized := strings.ToLower(strings.TrimSpace(text))
		if normalized == "" {
			continue
		}
		if strings.Contains(normalized, "подписаться") || normalized == "follow" {
			return true
		}
	}
	return false
}

func clickVisibleFollowControl(page playwright.Page, selector string) (bool, error) {
	locator := page.Locator(selector)
	count, err := locator.Count()
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}

	limit := count
	if limit > 5 {
		limit = 5
	}

	var lastErr error
	for idx := 0; idx < limit; idx++ {
		candidate := locator.Nth(idx)
		visible, visibleErr := candidate.IsVisible()
		if visibleErr != nil || !visible {
			continue
		}
		enabled, enabledErr := candidate.IsEnabled()
		if enabledErr != nil || !enabled {
			continue
		}

		_ = candidate.ScrollIntoViewIfNeeded()
		if clickErr := candidate.Click(playwright.LocatorClickOptions{
			Timeout: playwright.Float(5000),
		}); clickErr != nil {
			lastErr = clickErr
			continue
		}

		return true, nil
	}

	if lastErr != nil {
		return false, lastErr
	}
	return false, nil
}

func pageButtonTexts(page playwright.Page) ([]string, error) {
	value, err := page.Evaluate(`() => Array.from(document.querySelectorAll('button')).map((button) => (button.innerText || '').trim())`)
	if err != nil || value == nil {
		return nil, err
	}

	texts := []string{}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return texts, nil
	}
	for idx := 0; idx < rv.Len(); idx++ {
		entry := rv.Index(idx).Interface()
		typed, ok := entry.(string)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			continue
		}
		texts = append(texts, trimmed)
	}
	return texts, nil
}

func playwrightWarmupReadySelectors() []string {
	combined := make([]string, 0, len(defaultOskellyFollowRules.Selectors.ReadySignals)+len(defaultOskellyFollowRules.Selectors.AlreadyDoneSignals)+len(defaultOskellyFollowRules.Selectors.FollowControlPaths)+len(defaultOskellyFollowRules.Selectors.UnavailableSignals))
	combined = append(combined, defaultOskellyFollowRules.Selectors.ReadySignals...)
	combined = append(combined, defaultOskellyFollowRules.Selectors.AlreadyDoneSignals...)
	combined = append(combined, defaultOskellyFollowRules.Selectors.FollowControlPaths...)
	combined = append(combined, defaultOskellyFollowRules.Selectors.UnavailableSignals...)
	return combined
}

func playwrightFollowInteractiveSelectors() []string {
	combined := make([]string, 0, len(defaultOskellyFollowRules.Selectors.AlreadyDoneSignals)+len(defaultOskellyFollowRules.Selectors.FollowControlPaths)+len(defaultOskellyFollowRules.Selectors.UnavailableSignals))
	combined = append(combined, defaultOskellyFollowRules.Selectors.AlreadyDoneSignals...)
	combined = append(combined, defaultOskellyFollowRules.Selectors.FollowControlPaths...)
	combined = append(combined, defaultOskellyFollowRules.Selectors.UnavailableSignals...)
	return combined
}

func waitAnySelectorWithContext(
	ctx context.Context,
	page playwright.Page,
	selectors []string,
	timeout time.Duration,
) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return false
		}
		if hasAnySelector(page, selectors) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
