package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type errReadCloser struct{}

func (errReadCloser) Read(_ []byte) (int, error) { return 0, errors.New("read failed") }
func (errReadCloser) Close() error               { return nil }

func TestVerifyTipRegistryHTTPS_Branches(t *testing.T) {
	// Not parallel: overrides http.DefaultTransport.

	// Use a public IP literal so validateOutboundHost does not do DNS and passes deterministically.
	ctx := context.Background()
	domain := "8.8.8.8"
	proofValue := "lesser-host-tip-registry=tok"

	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	t.Run("do_error", func(t *testing.T) {
		http.DefaultTransport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("boom")
		})

		if ok := verifyTipRegistryHTTPS(ctx, domain, proofValue); ok {
			t.Fatalf("expected false")
		}
	})

	t.Run("non_200", func(t *testing.T) {
		var gotURL string
		http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotURL = req.URL.String()
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(testNope)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})

		if ok := verifyTipRegistryHTTPS(ctx, domain, proofValue); ok {
			t.Fatalf("expected false")
		}
		if gotURL == "" || !strings.Contains(gotURL, "/.well-known/") {
			t.Fatalf("expected request to well-known url, got %q", gotURL)
		}
	})

	t.Run("read_error", func(t *testing.T) {
		http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       errReadCloser{},
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})

		if ok := verifyTipRegistryHTTPS(ctx, domain, proofValue); ok {
			t.Fatalf("expected false")
		}
	})

	t.Run("body_mismatch", func(t *testing.T) {
		http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("wrong")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})

		if ok := verifyTipRegistryHTTPS(ctx, domain, proofValue); ok {
			t.Fatalf("expected false")
		}
	})

	t.Run("success", func(t *testing.T) {
		var gotURL string
		http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotURL = req.URL.String()
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("  " + proofValue + " \n")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})

		if ok := verifyTipRegistryHTTPS(ctx, domain, proofValue); !ok {
			t.Fatalf("expected true")
		}
		if gotURL != "https://"+domain+tipRegistryWellKnown {
			t.Fatalf("unexpected url: %q", gotURL)
		}
	})
}
