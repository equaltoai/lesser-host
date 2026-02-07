package trust

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFetchSharedWrappers(t *testing.T) {
	t.Parallel()

	normalized, u, err := NormalizeLinkURL("https://bücher.example/path/../?b=2&a=1#frag")
	if err != nil {
		t.Fatalf("NormalizeLinkURL: %v", err)
	}
	if normalized != "https://xn--bcher-kva.example/?a=1&b=2" {
		t.Fatalf("unexpected normalized: %q", normalized)
	}
	if u == nil || u.Host != "xn--bcher-kva.example" {
		t.Fatalf("unexpected parsed URL: %#v", u)
	}

	_, out, err := NormalizeLinkURL("http://127.0.0.1/")
	if err != nil {
		t.Fatalf("NormalizeLinkURL(ip): %v", err)
	}
	if err := ValidateOutboundURL(context.Background(), out); err == nil {
		t.Fatalf("expected ValidateOutboundURL to block localhost")
	}

	_, out, err = NormalizeLinkURL("https://93.184.216.34/")
	if err != nil {
		t.Fatalf("NormalizeLinkURL(public ip): %v", err)
	}
	if err := ValidateOutboundURL(context.Background(), out); err != nil {
		t.Fatalf("ValidateOutboundURL(public ip): %v", err)
	}

	client := NewPreviewHTTPClient(3 * time.Second)
	if client == nil || client.Timeout != 3*time.Second {
		t.Fatalf("unexpected client: %#v", client)
	}

	httpClient := &http.Client{
		Transport: stubTransport{responses: map[string]*http.Response{
			"https://93.184.216.34/": {
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("ok")),
			},
		}},
		Timeout: 2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	final, chain, body, ct, err := FetchWithRedirects(ctx, httpClient, out, 0, 16)
	if err != nil {
		t.Fatalf("FetchWithRedirects: %v", err)
	}
	if final == nil || final.String() != out.String() {
		t.Fatalf("unexpected final: %#v", final)
	}
	if len(chain) != 1 || chain[0] != out.String() {
		t.Fatalf("unexpected chain: %#v", chain)
	}
	if string(body) != "ok" || ct != "text/plain" {
		t.Fatalf("unexpected response: body=%q ct=%q", string(body), ct)
	}
}
