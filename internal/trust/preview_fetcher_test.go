package trust

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const linkPreviewErrBlockedSSRF = "blocked_ssrf"

type stubResolver struct {
	ipsByHost map[string][]net.IP
}

func (r stubResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	var out []net.IPAddr
	for _, ip := range r.ipsByHost[host] {
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

type stubTransport struct {
	responses map[string]*http.Response
}

func (t stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, io.ErrUnexpectedEOF
	}
	key := req.URL.String()
	resp := t.responses[key]
	if resp == nil {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}
	// Clone minimal fields to avoid sharing Body between calls.
	out := *resp
	if out.Header == nil {
		out.Header = make(http.Header)
	}
	out.Request = req
	return &out, nil
}

func TestNormalizeLinkURL_IDNA_QueryAndFragment(t *testing.T) {
	t.Parallel()

	got, _, err := normalizeLinkURL("https://bücher.example/path/../?b=2&a=1#frag")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	want := "https://xn--bcher-kva.example/?a=1&b=2"
	if got != want {
		t.Fatalf("normalized mismatch: got %q want %q", got, want)
	}
}

func TestNormalizeLinkURL_RejectsNonDefaultPort(t *testing.T) {
	t.Parallel()

	_, _, err := normalizeLinkURL("https://example.com:8443/")
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != "invalid_url" {
		t.Fatalf("expected invalid_url, got %T: %v", err, err)
	}
}

func TestValidateOutboundURL_BlocksPrivateIP(t *testing.T) {
	t.Parallel()

	_, u, err := normalizeLinkURL("http://127.0.0.1/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	err = validateOutboundURL(context.Background(), nil, u)
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != linkPreviewErrBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}
}

func TestValidateOutboundURL_BlocksHostnameResolvingToPrivateIP(t *testing.T) {
	t.Parallel()

	_, u, err := normalizeLinkURL("https://example.com/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	resolver := stubResolver{ipsByHost: map[string][]net.IP{
		"example.com": {net.ParseIP("10.0.0.1")},
	}}
	err = validateOutboundURL(context.Background(), resolver, u)
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != linkPreviewErrBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}
}

func TestFetchWithRedirects_BlocksRedirectToPrivateIP(t *testing.T) {
	t.Parallel()

	_, start, err := normalizeLinkURL("https://good.example/")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	resolver := stubResolver{ipsByHost: map[string][]net.IP{
		"good.example": {net.ParseIP("93.184.216.34")},
	}}

	client := &http.Client{
		Transport: stubTransport{responses: map[string]*http.Response{
			"https://good.example/": {
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"http://127.0.0.1/"}},
				Body:       io.NopCloser(strings.NewReader("")),
			},
		}},
		Timeout: linkPreviewFetchTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, chain, _, _, err := fetchWithRedirects(ctx, resolver, client, start, 3, 64)
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != linkPreviewErrBlockedSSRF {
		t.Fatalf("expected blocked_ssrf, got %T: %v", err, err)
	}
	if len(chain) != 2 {
		t.Fatalf("expected 2 urls in redirect chain, got %d (%v)", len(chain), chain)
	}
	if !strings.Contains(chain[1], "127.0.0.1") {
		t.Fatalf("expected redirect target to include 127.0.0.1, got %q", chain[1])
	}
}

func TestFetchWithRedirects_EnforcesByteLimit(t *testing.T) {
	t.Parallel()

	_, start, err := normalizeLinkURL("https://good.example/large")
	if err != nil {
		t.Fatalf("normalizeLinkURL error: %v", err)
	}
	resolver := stubResolver{ipsByHost: map[string][]net.IP{
		"good.example": {net.ParseIP("93.184.216.34")},
	}}

	client := &http.Client{
		Transport: stubTransport{responses: map[string]*http.Response{
			"https://good.example/large": {
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", 65))),
			},
		}},
		Timeout: linkPreviewFetchTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, _, _, err = fetchWithRedirects(ctx, resolver, client, start, 0, 64)
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*linkPreviewError); !ok || pe.Code != "too_large" {
		t.Fatalf("expected too_large, got %T: %v", err, err)
	}
}
