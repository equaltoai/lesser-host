package renderworker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func resetChromiumGlobals() {
	chromiumOnce = sync.Once{}
	chromiumPath = ""
	chromiumInitErr = nil
}

func TestRenderHTMLWithChrome_ReturnsErrorWhenHTMLEmpty(t *testing.T) {
	resetChromiumGlobals()

	screenshot, snapshot, preview, err := renderHTMLWithChrome(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "html is empty") {
		t.Fatalf("expected html empty error, got %v", err)
	}
	if screenshot != nil || snapshot != nil || preview != "" {
		t.Fatalf("expected empty outputs, got screenshot=%d snapshot=%d preview=%q", len(screenshot), len(snapshot), preview)
	}
}

func TestRenderHTMLWithChrome_PropagatesChromiumInitError(t *testing.T) {
	resetChromiumGlobals()
	t.Cleanup(resetChromiumGlobals)

	chromiumOnce.Do(func() {}) // mark once as completed so initChromium is not invoked.

	want := errors.New("chromium init failed")
	chromiumInitErr = want

	_, _, _, err := renderHTMLWithChrome(context.Background(), []byte("<html></html>"))
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

func TestRenderHTMLWithChrome_FailsFastWhenChromiumMissing(t *testing.T) {
	resetChromiumGlobals()
	t.Cleanup(resetChromiumGlobals)

	chromiumOnce.Do(func() {}) // mark once as completed so initChromium is not invoked.
	chromiumPath = "/no/such/chromium"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, _, err := renderHTMLWithChrome(ctx, []byte("<html><body>ok</body></html>"))
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestAllowChromiumRequestURL_AllowsOnlyLocalSchemes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		url  string
		want bool
	}{
		{name: "data", url: "data:text/html;base64,AA==", want: true},
		{name: "blob", url: "blob:https://example.com/123", want: true},
		{name: "about_blank", url: "about:blank", want: true},
		{name: "http", url: "http://example.com", want: false},
		{name: "https", url: "https://example.com", want: false},
		{name: "file", url: "file:///etc/passwd", want: false},
		{name: "chrome", url: "chrome://version", want: false},
		{name: "empty", url: " ", want: false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := allowChromiumRequestURL(tc.url); got != tc.want {
				t.Fatalf("allowChromiumRequestURL(%q)=%v want %v", tc.url, got, tc.want)
			}
		})
	}
}
