package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"follower/internal/domain"
	"follower/internal/stackerr"

	"github.com/playwright-community/playwright-go"
)

type bootstrapCredentialResolver interface {
	Resolve(
		ctx context.Context,
		source domain.CredentialSource,
		reference string,
	) (domain.AccountCredentials, error)
}

type BootstrapLoginRunner interface {
	RunBootstrapLogin(
		ctx context.Context,
		input domain.BootstrapLoginInput,
	) (domain.BootstrapLoginResult, error)
}

func NewBootstrapLoginRunner(
	engine string,
	resolver bootstrapCredentialResolver,
	logger *slog.Logger,
) (BootstrapLoginRunner, error) {
	switch engine {
	case "mock":
		return NewMockBootstrapLoginRunner(resolver, nil, logger), nil
	case "playwright":
		return NewPlaywrightBootstrapLoginRunner(resolver, logger), nil
	default:
		return nil, domain.NewDomainError(
			domain.ErrorCodeInvalidOperationalState,
			fmt.Sprintf("unsupported browser engine for bootstrap login runner: %s", engine),
		)
	}
}

type MockBootstrapLoginRunner struct {
	resolver      bootstrapCredentialResolver
	outcomesByRef map[string]domain.BootstrapLoginOutcome
	logger        *slog.Logger
}

func NewMockBootstrapLoginRunner(
	resolver bootstrapCredentialResolver,
	outcomesByRef map[string]domain.BootstrapLoginOutcome,
	logger *slog.Logger,
) *MockBootstrapLoginRunner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	clonedOutcomes := map[string]domain.BootstrapLoginOutcome{}
	for ref, outcome := range outcomesByRef {
		clonedOutcomes[ref] = outcome
	}

	return &MockBootstrapLoginRunner{
		resolver:      resolver,
		outcomesByRef: clonedOutcomes,
		logger:        logger,
	}
}

func (r *MockBootstrapLoginRunner) RunBootstrapLogin(
	ctx context.Context,
	input domain.BootstrapLoginInput,
) (domain.BootstrapLoginResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.BootstrapLoginResult{}, stackerr.WithStack(err)
	}
	if err := input.Validate(); err != nil {
		return domain.BootstrapLoginResult{}, stackerr.WithStack(err)
	}
	if r.resolver == nil {
		return domain.BootstrapLoginResult{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"bootstrap credential resolver is not configured",
		)
	}
	credentials, err := r.resolver.Resolve(ctx, input.CredentialSource, input.CredentialRef)
	if err != nil {
		return domain.BootstrapLoginResult{}, stackerr.WithStack(err)
	}
	if err := credentials.Validate(); err != nil {
		return domain.BootstrapLoginResult{
			Outcome: domain.BootstrapLoginOutcomeAuthInvalidCredentials,
			Diagnostics: domain.BootstrapLoginDiagnostics{
				Engine: "mock",
			},
		}, nil
	}

	outcome := domain.BootstrapLoginOutcomeSuccess
	if configuredOutcome, ok := r.outcomesByRef[input.CredentialRef]; ok {
		outcome = configuredOutcome
	}

	result := domain.BootstrapLoginResult{
		Outcome: outcome,
		Diagnostics: domain.BootstrapLoginDiagnostics{
			Engine: "mock",
		},
	}
	if outcome == domain.BootstrapLoginOutcomeSuccess {
		payload, marshalErr := json.Marshal(map[string]any{
			"cookies": []map[string]string{
				{
					"name":  "sid",
					"value": "bootstrap-mock-session",
				},
			},
		})
		if marshalErr != nil {
			return domain.BootstrapLoginResult{}, domain.NewDomainError(
				domain.ErrorCodeAuthBootstrapFailed,
				"mock bootstrap payload serialization failed",
			)
		}
		result.SessionPayload = payload
	}

	if err := result.Validate(); err != nil {
		return domain.BootstrapLoginResult{}, stackerr.WithStack(err)
	}

	r.logger.Debug(
		"bootstrap.login.finished",
		"component", "browser.bootstrap_login_flow",
		"task_id", input.TaskID.String(),
		"account_id", input.AccountID.String(),
		"attempt", input.Attempt,
		"engine", "mock",
		"outcome", outcome,
	)

	return result, nil
}

type playwrightBootstrapAdapter interface {
	Execute(
		ctx context.Context,
		credentials domain.AccountCredentials,
	) (domain.BootstrapLoginOutcome, []byte, map[string][]byte, error)
}

type PlaywrightBootstrapLoginRunner struct {
	resolver bootstrapCredentialResolver
	adapter  playwrightBootstrapAdapter
	logger   *slog.Logger
}

func NewPlaywrightBootstrapLoginRunner(
	resolver bootstrapCredentialResolver,
	logger *slog.Logger,
	adapter ...playwrightBootstrapAdapter,
) *PlaywrightBootstrapLoginRunner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	var selectedAdapter playwrightBootstrapAdapter = &defaultPlaywrightBootstrapAdapter{}
	if len(adapter) > 0 && adapter[0] != nil {
		selectedAdapter = adapter[0]
	}

	return &PlaywrightBootstrapLoginRunner{
		resolver: resolver,
		adapter:  selectedAdapter,
		logger:   logger,
	}
}

func (r *PlaywrightBootstrapLoginRunner) RunBootstrapLogin(
	ctx context.Context,
	input domain.BootstrapLoginInput,
) (domain.BootstrapLoginResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.BootstrapLoginResult{}, stackerr.WithStack(err)
	}
	if err := input.Validate(); err != nil {
		return domain.BootstrapLoginResult{}, stackerr.WithStack(err)
	}
	if r.resolver == nil {
		return domain.BootstrapLoginResult{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"bootstrap credential resolver is not configured",
		)
	}
	if r.adapter == nil {
		return domain.BootstrapLoginResult{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"playwright bootstrap adapter is not configured",
		)
	}

	credentials, err := r.resolver.Resolve(ctx, input.CredentialSource, input.CredentialRef)
	if err != nil {
		return domain.BootstrapLoginResult{}, stackerr.WithStack(err)
	}
	if err := credentials.Validate(); err != nil {
		return domain.BootstrapLoginResult{
			Outcome: domain.BootstrapLoginOutcomeAuthInvalidCredentials,
			Diagnostics: domain.BootstrapLoginDiagnostics{
				Engine: "playwright",
			},
		}, nil
	}

	startedAt := time.Now()
	outcome, payload, authScreenshots, runtimeErr := r.adapter.Execute(ctx, credentials)
	result := domain.BootstrapLoginResult{
		Outcome:         outcome,
		SessionPayload:  append([]byte(nil), payload...),
		AuthScreenshots: cloneBootstrapScreenshots(authScreenshots),
		Diagnostics: domain.BootstrapLoginDiagnostics{
			Engine:     "playwright",
			DurationMS: time.Since(startedAt).Milliseconds(),
		},
	}
	if runtimeErr != nil && !outcome.IsValid() {
		result.Outcome = domain.BootstrapLoginOutcomeAuthRuntimeError
	}
	if !result.Outcome.IsValid() {
		result.Outcome = domain.BootstrapLoginOutcomeAuthRuntimeError
	}
	if result.Outcome != domain.BootstrapLoginOutcomeSuccess {
		result.SessionPayload = nil
	}
	if err := result.Validate(); err != nil {
		return domain.BootstrapLoginResult{}, stackerr.WithStack(err)
	}

	r.logger.Debug(
		"bootstrap.login.finished",
		"component", "browser.bootstrap_login_flow",
		"task_id", input.TaskID.String(),
		"account_id", input.AccountID.String(),
		"attempt", input.Attempt,
		"engine", "playwright",
		"outcome", result.Outcome,
	)

	return result, nil
}

type defaultPlaywrightBootstrapAdapter struct{}

func (a *defaultPlaywrightBootstrapAdapter) executeLegacy(
	ctx context.Context,
	credentials domain.AccountCredentials,
) (domain.BootstrapLoginOutcome, []byte, error) {
	if err := ctx.Err(); err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(err)
	}
	if err := credentials.Validate(); err != nil {
		return domain.BootstrapLoginOutcomeAuthInvalidCredentials, nil, nil
	}

	playwrightInstance, err := playwright.Run()
	if err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(err)
	}
	defer playwrightInstance.Stop()

	browser, err := playwrightInstance.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(err)
	}
	defer browser.Close()

	browserContext, err := browser.NewContext()
	if err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(err)
	}
	defer browserContext.Close()

	page, err := browserContext.NewPage()
	if err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(err)
	}

	if _, err := page.Goto("https://oskelly.ru/login", playwright.PageGotoOptions{
		Timeout: playwright.Float(45000),
	}); err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(err)
	}

	if err := fillFirstSelector(page, []string{
		"input[name='email']",
		"input[type='email']",
		"input[name='login']",
		"input[name='username']",
	}, credentials.Username); err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(err)
	}
	if err := fillFirstSelector(page, []string{
		"input[name='password']",
		"input[type='password']",
	}, credentials.Password); err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(err)
	}
	if err := clickFirstSelector(page, []string{
		"section.authorization .login.active .submit input.form_submit",
		".authorization .login.active input.form_submit",
		"form[action*='/login'] input.form_submit",
		"input.form_submit[value*='Войти']",
		"input.form_submit",
		"button[type='submit']",
		"input[type='submit']",
		".submit input[type='submit']",
		"button:has-text('Войти')",
		"button:has-text('Login')",
	}); err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(err)
	}

	if authenticated := waitAnySelector(page, []string{
		"[data-testid='profile-avatar']",
		".header__profile",
		"a[href*='/profile']",
		"[class*='account']",
	}, 30*time.Second); authenticated || isAuthenticatedURL(page.URL()) {
		storageState, stateErr := browserContext.StorageState()
		if stateErr != nil {
			return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(stateErr)
		}
		payload, marshalErr := json.Marshal(storageState)
		if marshalErr != nil {
			return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, stackerr.WithStack(marshalErr)
		}
		return domain.BootstrapLoginOutcomeSuccess, payload, nil
	}

	pageContent, _ := page.Content()
	loweredContent := strings.ToLower(pageContent)
	if containsAny(loweredContent, []string{
		"РЅРµРІРµСЂРЅ",
		"РЅРµРїСЂР°РІРёР»СЊРЅ",
		"invalid credentials",
		"incorrect password",
		"wrong password",
	}) {
		return domain.BootstrapLoginOutcomeAuthInvalidCredentials, nil, nil
	}
	if containsAny(loweredContent, []string{
		"captcha",
		"РїРѕРґС‚РІРµСЂРґ",
		"РїСЂРѕРІРµСЂРє",
		"challenge",
		"verification required",
	}) {
		return domain.BootstrapLoginOutcomeAuthChallenge, nil, nil
	}

	return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, fmt.Errorf(
		"authenticated oskelly UI signal was not detected after login submit",
	)
}

func (a *defaultPlaywrightBootstrapAdapter) Execute(
	ctx context.Context,
	credentials domain.AccountCredentials,
) (domain.BootstrapLoginOutcome, []byte, map[string][]byte, error) {
	authScreenshots := map[string][]byte{}

	if err := ctx.Err(); err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	if err := credentials.Validate(); err != nil {
		return domain.BootstrapLoginOutcomeAuthInvalidCredentials, nil, authScreenshots, nil
	}

	playwrightInstance, err := playwright.Run()
	if err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	defer playwrightInstance.Stop()

	browser, err := playwrightInstance.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	defer browser.Close()

	browserContext, err := browser.NewContext()
	if err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	defer browserContext.Close()

	page, err := browserContext.NewPage()
	if err != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}

	profileIconSelector := "svg.osk-icon.osk-icon_size-l.osk-header-top-actions__link"
	emailPasswordLoginSelector := "button.osk-button.osk-button_color-secondary.osk-button_size-m.osk-button_full-width[type='button']"
	emailInputSelector := "input.osk-input__input[type='text'][autocomplete='email']"
	passwordInputSelector := "input.osk-input__input.osk-input__input_password[type='password'][autocomplete='current-password']"
	submitSelector := "button.osk-button.osk-button_color-primary.osk-button_size-m.osk-button_full-width.osk-auth-login-dialog__button[type='submit']"

	if _, err := page.Goto("https://oskelly.ru", playwright.PageGotoOptions{
		Timeout:   playwright.Float(45000),
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-goto-home-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	captureBootstrapScreenshot(page, authScreenshots, "auth-home")

	if _, err := page.WaitForSelector(profileIconSelector, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(15000),
		State:   playwright.WaitForSelectorStateVisible,
	}); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-profile-icon-wait-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	if err := page.Click(profileIconSelector, playwright.PageClickOptions{
		Timeout: playwright.Float(15000),
	}); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-profile-icon-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	captureBootstrapScreenshot(page, authScreenshots, "auth-profile-icon")

	if _, err := page.WaitForSelector(emailPasswordLoginSelector, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(15000),
		State:   playwright.WaitForSelectorStateVisible,
	}); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-email-password-button-wait-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	if err := page.Click(emailPasswordLoginSelector, playwright.PageClickOptions{
		Timeout: playwright.Float(15000),
	}); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-email-password-button-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	captureBootstrapScreenshot(page, authScreenshots, "auth-email-password-form")

	if _, err := page.WaitForSelector(emailInputSelector, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(15000),
		State:   playwright.WaitForSelectorStateVisible,
	}); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-email-wait-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	if err := page.Fill(emailInputSelector, credentials.Username); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-email-fill-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	if _, err := page.WaitForSelector(passwordInputSelector, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(15000),
		State:   playwright.WaitForSelectorStateVisible,
	}); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-password-wait-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	if err := page.Fill(passwordInputSelector, credentials.Password); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-password-fill-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	captureBootstrapScreenshot(page, authScreenshots, "auth-credentials-filled")

	if _, err := page.WaitForSelector(submitSelector, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(15000),
		State:   playwright.WaitForSelectorStateVisible,
	}); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-submit-wait-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	if err := page.Click(submitSelector, playwright.PageClickOptions{
		Timeout: playwright.Float(15000),
	}); err != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-submit-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(err)
	}
	captureBootstrapScreenshot(page, authScreenshots, "auth-submit")

	time.Sleep(2 * time.Second)
	captureBootstrapScreenshot(page, authScreenshots, "auth-post-submit")

	if hasAnySelector(page, []string{
		emailPasswordLoginSelector,
		emailInputSelector,
		passwordInputSelector,
		submitSelector,
	}) {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"login form remains available after submit",
		)
	}

	storageState, stateErr := browserContext.StorageState()
	if stateErr != nil {
		captureBootstrapScreenshot(page, authScreenshots, "auth-storage-state-error")
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(stateErr)
	}
	payload, marshalErr := json.Marshal(storageState)
	if marshalErr != nil {
		return domain.BootstrapLoginOutcomeAuthRuntimeError, nil, authScreenshots, stackerr.WithStack(marshalErr)
	}

	return domain.BootstrapLoginOutcomeSuccess, payload, authScreenshots, nil
}

func fillFirstSelector(page playwright.Page, selectors []string, value string) error {
	selector, err := firstExistingSelector(page, selectors)
	if err != nil {
		return stackerr.WithStack(err)
	}
	return page.Fill(selector, value)
}

func clickFirstSelector(page playwright.Page, selectors []string) error {
	selector, err := firstExistingSelector(page, selectors)
	if err != nil {
		return stackerr.WithStack(err)
	}
	return page.Click(selector)
}

func firstExistingSelector(page playwright.Page, selectors []string) (string, error) {
	for _, selector := range selectors {
		handle, err := page.QuerySelector(selector)
		if err != nil {
			continue
		}
		if handle != nil {
			return selector, nil
		}
	}
	return "", domain.NewDomainError(
		domain.ErrorCodeAuthBootstrapFailed,
		"required bootstrap login UI element not found",
	)
}

func waitAnySelector(page playwright.Page, selectors []string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, selector := range selectors {
			handle, err := page.QuerySelector(selector)
			if err != nil {
				continue
			}
			if handle != nil {
				return true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func isAuthenticatedURL(pageURL string) bool {
	return !isAuthenticationRequiredURL(pageURL)
}

func cloneBootstrapScreenshots(source map[string][]byte) map[string][]byte {
	if len(source) == 0 {
		return nil
	}

	cloned := make(map[string][]byte, len(source))
	for key, payload := range source {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" || len(payload) == 0 {
			continue
		}
		cloned[trimmedKey] = append([]byte(nil), payload...)
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func captureBootstrapScreenshot(
	page playwright.Page,
	dest map[string][]byte,
	stage string,
) {
	if page == nil || dest == nil {
		return
	}
	trimmedStage := strings.TrimSpace(stage)
	if trimmedStage == "" {
		return
	}

	screenshot, err := page.Screenshot(playwright.PageScreenshotOptions{
		FullPage: playwright.Bool(true),
	})
	if err != nil || len(screenshot) == 0 {
		return
	}

	dest[trimmedStage] = append([]byte(nil), screenshot...)
}

func containsAny(content string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}
