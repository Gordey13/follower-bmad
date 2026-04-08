package browser

import (
	"context"

	"follower/internal/domain"

	"github.com/playwright-community/playwright-go"
)

type defaultPlaywrightVerifyAdapter struct{}

func (a *defaultPlaywrightVerifyAdapter) InspectFollowState(
	ctx context.Context,
	input domain.FollowVerificationInput,
) (playwrightVerifyDetection, error) {
	if err := ctx.Err(); err != nil {
		return playwrightVerifyDetection{}, err
	}

	targetURL, err := normalizeOskellyTargetProfileURL(input.TargetProfile)
	if err != nil {
		return playwrightVerifyDetection{}, err
	}
	storageState, err := parsePlaywrightStorageStatePayload(input.SessionPayload)
	if err != nil {
		return playwrightVerifyDetection{}, err
	}

	playwrightInstance, err := playwright.Run()
	if err != nil {
		return playwrightVerifyDetection{}, err
	}
	defer playwrightInstance.Stop()

	browserInstance, err := playwrightInstance.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		return playwrightVerifyDetection{}, err
	}
	defer browserInstance.Close()

	browserContext, err := browserInstance.NewContext(playwright.BrowserNewContextOptions{
		StorageState: storageState,
	})
	if err != nil {
		return playwrightVerifyDetection{}, err
	}
	defer browserContext.Close()

	page, err := browserContext.NewPage()
	if err != nil {
		return playwrightVerifyDetection{}, err
	}

	response, gotoErr := page.Goto(targetURL, playwright.PageGotoOptions{
		Timeout:   playwright.Float(float64(defaultOskellyVerifyRules.Timeouts.Navigation.Milliseconds())),
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	})
	if gotoErr != nil {
		return playwrightVerifyDetection{
			State:             playwrightVerifyStateUnknown,
			ScreenshotPayload: captureVerifyScreenshot(page, "navigation-runtime-failure"),
			Reason:            "verify navigation failed before supported UI signals were observed",
		}, nil
	}

	if response != nil && response.Status() >= 400 {
		return playwrightVerifyDetection{
			State:             playwrightVerifyStateTargetUnreachable,
			ScreenshotPayload: captureVerifyScreenshot(page, "target-unreachable"),
			Reason:            "verify navigation returned unreachable target status",
		}, nil
	}

	if !isResolvedOskellyProfileURL(page.URL()) {
		return playwrightVerifyDetection{
			State:             playwrightVerifyStateTargetUnreachable,
			ScreenshotPayload: captureVerifyScreenshot(page, "target-unreachable"),
			Reason:            "verify resolved to a page outside supported oskelly profile path",
		}, nil
	}

	if !waitAnySelectorWithContext(
		ctx,
		page,
		playwrightVerifyReadySelectors(),
		defaultOskellyVerifyRules.Timeouts.Readiness,
	) {
		return playwrightVerifyDetection{
			State:             playwrightVerifyStateUnknown,
			ScreenshotPayload: captureVerifyScreenshot(page, "ready-timeout"),
			Reason:            "verify UI ready signal was not detected before timeout",
		}, nil
	}

	detection := playwrightVerifyDetection{
		State:             playwrightVerifyStateUnknown,
		ScreenshotPayload: captureVerifyScreenshot(page, "ui-unknown"),
		Reason:            "verify UI did not match a supported follow state",
	}

	switch {
	case hasAnySelector(page, defaultOskellyVerifyRules.Selectors.TargetUnreachableSignals):
		detection.State = playwrightVerifyStateTargetUnreachable
		detection.Reason = "verify UI indicates target profile is unreachable"
	case hasAnySelector(page, defaultOskellyVerifyRules.Selectors.ActionUnavailableSignals):
		detection.State = playwrightVerifyStateActionUnavailable
		detection.Reason = "verify UI indicates follow action is unavailable"
	case hasAnySelector(page, defaultOskellyVerifyRules.Selectors.FollowConfirmedSignals):
		detection.State = playwrightVerifyStateFollowConfirmed
		detection.Reason = "verify UI confirms followed state"
	case hasFollowConfirmedButtonText(page):
		detection.State = playwrightVerifyStateFollowConfirmed
		detection.Reason = "verify UI confirms followed state (button text fallback)"
	}

	return detection, nil
}

func captureVerifyScreenshot(page playwright.Page, fallbackSuffix string) []byte {
	if page != nil {
		screenshot, err := page.Screenshot(playwright.PageScreenshotOptions{
			FullPage: playwright.Bool(true),
		})
		if err == nil && len(screenshot) > 0 {
			return screenshot
		}
	}

	return []byte("verify-screenshot:" + fallbackSuffix)
}
