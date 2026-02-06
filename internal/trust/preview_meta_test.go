package trust

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExtractLinkPreviewMeta_PrefersOGAndResolvesRelativeImageURL(t *testing.T) {
	t.Parallel()

	base, err := url.Parse("https://example.com/a/b")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}

	doc := []byte(`<!doctype html><html><head>
<title>Title fallback</title>
<meta property="og:title" content=" OG Title ">
<meta property="og:description" content="Desc">
<meta property="og:image" content="/img.png">
</head><body>hello</body></html>`)

	meta := extractLinkPreviewMeta(base, doc)
	if meta.Title != "OG Title" {
		t.Fatalf("expected og title, got %q", meta.Title)
	}
	if meta.Description != "Desc" {
		t.Fatalf("expected desc, got %q", meta.Description)
	}
	if meta.ImageURL != "https://example.com/img.png" {
		t.Fatalf("expected resolved image url, got %q", meta.ImageURL)
	}
}

func TestExtractLinkPreviewMeta_UsesTitleFallbackAndNameDescription(t *testing.T) {
	t.Parallel()

	doc := []byte(`<!doctype html><html><head>
<title>  My Title </title>
<meta name="description" content="  D ">
</head><body>hello</body></html>`)

	meta := extractLinkPreviewMeta(nil, doc)
	if meta.Title != "My Title" {
		t.Fatalf("expected title trimmed, got %q", meta.Title)
	}
	if meta.Description != "D" {
		t.Fatalf("expected description, got %q", meta.Description)
	}
}

func TestPreviewMetaHelpers(t *testing.T) {
	t.Parallel()

	if got := resolveMaybeRelativeURL(nil, ""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := resolveMaybeRelativeURL(nil, "http://%"); got != "" {
		t.Fatalf("expected empty for invalid url, got %q", got)
	}
	if got := resolveMaybeRelativeURL(nil, "/x"); got != "/x" {
		t.Fatalf("expected passthrough when base nil, got %q", got)
	}

	base, _ := url.Parse("https://example.com/a")
	if got := resolveMaybeRelativeURL(base, "/x"); got != "https://example.com/x" {
		t.Fatalf("expected resolved url, got %q", got)
	}

	if !isHTMLContentType("text/html; charset=utf-8") || !isHTMLContentType("application/xhtml+xml, text/html") {
		t.Fatalf("expected html content type true")
	}
	if isHTMLContentType("application/json") {
		t.Fatalf("expected html content type false")
	}

	c := newPreviewHTTPClient(2 * time.Second)
	if c == nil || c.Timeout != 2*time.Second {
		t.Fatalf("unexpected http client: %#v", c)
	}
	if err := c.CheckRedirect(&http.Request{}, nil); err != http.ErrUseLastResponse {
		t.Fatalf("expected ErrUseLastResponse, got %v", err)
	}

	u, _ := url.Parse("https://example.com")
	if got := safeURLString(u); got != "https://example.com" {
		t.Fatalf("expected trimmed url string, got %q", got)
	}
	if got := safeURLString(nil); got != "" {
		t.Fatalf("expected empty for nil, got %q", got)
	}

	// applyLinkPreviewMeta is a no-op for nil/empty.
	applyLinkPreviewMeta(nil, nil, "og:title", "", "x")
	var meta linkPreviewMeta
	applyLinkPreviewMeta(&meta, nil, "og:title", "", " ")
	if meta.Title != "" {
		t.Fatalf("expected no-op for empty content")
	}

	applyLinkPreviewMeta(&meta, base, "og:image", "", "/img.png")
	if !strings.Contains(meta.ImageURL, "https://example.com/img.png") {
		t.Fatalf("expected resolved image url, got %q", meta.ImageURL)
	}
}
