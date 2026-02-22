package renderworker

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

func allowChromiumRequestURL(raw string) bool {
	u := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(u, "data:") || strings.HasPrefix(u, "blob:") || strings.HasPrefix(u, "about:")
}

func renderHTMLWithChrome(ctx context.Context, html []byte) ([]byte, []byte, string, error) {
	if len(html) == 0 {
		return nil, nil, "", fmt.Errorf("html is empty")
	}

	execPath, err := ensureChromiumReady(ctx)
	if err != nil {
		return nil, nil, "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(execPath),
		chromedp.Flag("headless", "shell"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("single-process", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-webgl", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	// Block all browser requests except for local schemes (prevents SSRF and file:// reads).
	chromedp.ListenTarget(browserCtx, func(ev any) {
		e, ok := ev.(*fetch.EventRequestPaused)
		if !ok || e == nil {
			return
		}

		if allowChromiumRequestURL(e.Request.URL) {
			_ = chromedp.Run(browserCtx, fetch.ContinueRequest(e.RequestID))
			return
		}

		_ = chromedp.Run(browserCtx, fetch.FailRequest(e.RequestID, network.ErrorReasonBlockedByClient))
	})

	dataURL := "data:text/html;base64," + base64.StdEncoding.EncodeToString(html)

	var innerText string
	var screenshot []byte

	err = chromedp.Run(browserCtx,
		fetch.Enable().WithPatterns([]*fetch.RequestPattern{{
			URLPattern:   "*",
			RequestStage: fetch.RequestStageRequest,
		}}),
		chromedp.EmulateViewport(1200, 630),
		chromedp.Navigate(dataURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(`(document && document.body && document.body.innerText) ? document.body.innerText : ""`, &innerText),
		chromedp.ActionFunc(func(ctx context.Context) error {
			buf, screenshotErr := page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatJpeg).
				WithQuality(70).
				Do(ctx)
			if screenshotErr != nil {
				return screenshotErr
			}
			screenshot = buf
			return nil
		}),
	)
	if err != nil {
		return nil, nil, "", err
	}

	innerText = strings.TrimSpace(innerText)
	if len(innerText) > 20000 {
		innerText = innerText[:20000]
	}

	textPreview := innerText
	if len(textPreview) > 512 {
		textPreview = textPreview[:512]
	}

	return screenshot, []byte(innerText), textPreview, nil
}
