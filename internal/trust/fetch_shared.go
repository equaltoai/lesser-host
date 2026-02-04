package trust

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Shared helpers for internal workers. These wrap the hardened fetch/SSRF logic used by the preview pipeline.

type LinkPreviewError = linkPreviewError

func NormalizeLinkURL(raw string) (string, *url.URL, error) {
	return normalizeLinkURL(raw)
}

func ValidateOutboundURL(ctx context.Context, u *url.URL) error {
	return validateOutboundURL(ctx, nil, u)
}

func NewPreviewHTTPClient(timeout time.Duration) *http.Client {
	return newPreviewHTTPClient(timeout)
}

func FetchWithRedirects(ctx context.Context, client *http.Client, start *url.URL, maxRedirects int, maxBytes int64) (*url.URL, []string, []byte, string, error) {
	return fetchWithRedirects(ctx, nil, client, start, maxRedirects, maxBytes)
}
