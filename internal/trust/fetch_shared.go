package trust

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Shared helpers for internal workers. These wrap the hardened fetch/SSRF logic used by the preview pipeline.

// LinkPreviewError is a public alias for link preview errors.
type LinkPreviewError = linkPreviewError

// NormalizeLinkURL normalizes a raw URL string and returns the normalized string and parsed URL.
func NormalizeLinkURL(raw string) (string, *url.URL, error) {
	return normalizeLinkURL(raw)
}

// ValidateOutboundURL validates a URL for safe outbound fetching.
func ValidateOutboundURL(ctx context.Context, u *url.URL) error {
	return validateOutboundURL(ctx, nil, u)
}

// NewPreviewHTTPClient constructs an HTTP client suitable for preview fetching.
func NewPreviewHTTPClient(timeout time.Duration) *http.Client {
	return newPreviewHTTPClient(timeout, nil)
}

// FetchWithRedirects fetches a URL with redirect handling and a maximum body size.
func FetchWithRedirects(ctx context.Context, client *http.Client, start *url.URL, maxRedirects int, maxBytes int64) (*url.URL, []string, []byte, string, error) {
	return fetchWithRedirects(ctx, nil, client, start, maxRedirects, maxBytes)
}
