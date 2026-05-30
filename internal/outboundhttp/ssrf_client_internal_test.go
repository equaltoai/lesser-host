package outboundhttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestNormalizeOutboundHost(t *testing.T) {
	t.Parallel()

	t.Run("host_and_port", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeOutboundHost("Example.com:443")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "example.com" {
			t.Fatalf("expected example.com, got %q", got)
		}
	})

	t.Run("ipv6_brackets", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeOutboundHost("[2001:db8::1]:443")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "2001:db8::1" {
			t.Fatalf("expected 2001:db8::1, got %q", got)
		}
	})

	t.Run("disallows_localhost", func(t *testing.T) {
		t.Parallel()
		if _, err := normalizeOutboundHost("localhost"); err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestResolveAndValidateOutboundHostIPs_IPOnly(t *testing.T) {
	t.Parallel()

	ips, err := resolveAndValidateOutboundHostIPs(context.Background(), nil, "8.8.8.8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 1 || ips[0].IP.String() != "8.8.8.8" {
		t.Fatalf("unexpected ips: %#v", ips)
	}
}

func TestResolveAndValidateOutboundHostIPs_BlocksLoopback(t *testing.T) {
	t.Parallel()

	if _, err := resolveAndValidateOutboundHostIPs(context.Background(), nil, "127.0.0.1:443"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestResolveAndValidateOutboundHostIPs_Empty(t *testing.T) {
	t.Parallel()

	if _, err := resolveAndValidateOutboundHostIPs(context.Background(), nil, " "); err == nil {
		t.Fatalf("expected error")
	}
}

func TestResolveAndValidateOutboundHostIPs_BlocksInternalSuffix(t *testing.T) {
	t.Parallel()

	if _, err := resolveAndValidateOutboundHostIPs(context.Background(), nil, "example.internal"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestResolveAndValidateOutboundHostIPs_ResolvesPublicDomain(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	ips, err := resolveAndValidateOutboundHostIPs(ctx, nil, "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) == 0 {
		t.Fatalf("expected ips")
	}
}

func TestResolveAndValidateOutboundHostIPs_IPv6NoPort(t *testing.T) {
	t.Parallel()

	ips, err := resolveAndValidateOutboundHostIPs(context.Background(), nil, "2001:4860:4860::8888")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 1 || ips[0].IP.String() != "2001:4860:4860::8888" {
		t.Fatalf("unexpected ips: %#v", ips)
	}
}

func TestValidateOutboundResolvedIPs_BlocksPrivate(t *testing.T) {
	t.Parallel()

	ips := []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}
	if err := validateOutboundResolvedIPs(ips); err == nil {
		t.Fatalf("expected error")
	}
}

func TestSSRFProtectedHTTPClient_DefaultTransport(t *testing.T) {
	t.Parallel()

	c := NewSSRFProtectedClient(nil)
	if c.Timeout != 10*time.Second {
		t.Fatalf("expected default timeout 10s, got %s", c.Timeout)
	}

	guard, ok := c.Transport.(ssrfProtectedRoundTripper)
	if !ok {
		t.Fatalf("expected ssrfProtectedRoundTripper, got %#v", c.Transport)
	}
	tr, ok := guard.base.(*http.Transport)
	if !ok || tr == nil {
		t.Fatalf("expected guarded *http.Transport, got %#v", guard.base)
	}
	if tr.Proxy != nil {
		t.Fatalf("expected proxy disabled")
	}
	if tr.DialContext == nil {
		t.Fatalf("expected custom DialContext")
	}
	if c.CheckRedirect == nil {
		t.Fatalf("expected CheckRedirect configured")
	}
}

func TestSSRFProtectedHTTPClient_RejectsRedirects(t *testing.T) {
	t.Parallel()

	c := NewSSRFProtectedClient(nil)
	err := c.CheckRedirect(&http.Request{}, []*http.Request{{}})
	if !errors.Is(err, ErrRedirectNotAllowed) {
		t.Fatalf("expected ErrRedirectNotAllowed, got %v", err)
	}
}

func TestValidateOutboundURL_RejectsHTTPAndNonDefaultPort(t *testing.T) {
	t.Parallel()

	httpURL, err := url.Parse("http://example.com/")
	if err != nil {
		t.Fatalf("parse http url: %v", err)
	}
	httpErr := validateOutboundURL(context.Background(), nil, httpURL)
	if httpErr == nil {
		t.Fatalf("expected http scheme rejection")
	}

	portURL, err := url.Parse("https://example.com:444/")
	if err != nil {
		t.Fatalf("parse port url: %v", err)
	}
	portErr := validateOutboundURL(context.Background(), nil, portURL)
	if portErr == nil {
		t.Fatalf("expected non-default port rejection")
	}
}

func TestContextOrBackground(t *testing.T) {
	t.Parallel()

	var nilCtx context.Context
	if contextOrBackground(nilCtx) == nil {
		t.Fatalf("expected non-nil context")
	}
	ctx := context.Background()
	if contextOrBackground(ctx) != ctx {
		t.Fatalf("expected same context")
	}
}

func TestSSRFProtectedHTTPClient_CustomTransportPreserved(t *testing.T) {
	t.Parallel()

	baseTransport := &http.Transport{}
	base := &http.Client{Transport: baseTransport, Timeout: 1 * time.Second}

	c := NewSSRFProtectedClient(base)
	if c.Timeout != 1*time.Second {
		t.Fatalf("expected base timeout, got %s", c.Timeout)
	}
	if c.Transport == baseTransport {
		t.Fatalf("expected base transport cloned before proxy stripping")
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr == nil {
		t.Fatalf("expected cloned *http.Transport, got %#v", c.Transport)
	}
	if tr.Proxy != nil {
		t.Fatalf("expected proxy disabled")
	}
	if c.CheckRedirect == nil {
		t.Fatalf("expected CheckRedirect configured")
	}
}

func TestNewSSRFProtectedDialContext_InvalidAddr(t *testing.T) {
	t.Parallel()

	dial := newSSRFProtectedDialContext(nil)
	if _, err := dial(context.Background(), "tcp", "missingport"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestNewSSRFProtectedDialContext_BlocksLoopback(t *testing.T) {
	t.Parallel()

	dial := newSSRFProtectedDialContext(nil)
	if _, err := dial(context.Background(), "tcp", "127.0.0.1:443"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestNewSSRFProtectedDialContext_DialFailsWithoutNetwork(t *testing.T) {
	t.Parallel()

	dial := newSSRFProtectedDialContext(nil)
	if _, err := dial(context.Background(), "badnet", "8.8.8.8:443"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestMustPrefix_PanicsOnInvalidCIDR(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()

	_ = mustPrefix("not-a-cidr")
}
