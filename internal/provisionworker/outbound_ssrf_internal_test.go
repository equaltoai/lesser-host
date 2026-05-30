package provisionworker

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/outboundhttp"
)

func TestSSRFProtectedHTTPClient_UsesSharedGuard(t *testing.T) {
	t.Parallel()

	c := ssrfProtectedHTTPClient(nil)
	if c.Timeout != outboundhttp.DefaultSSRFProtectedTimeout {
		t.Fatalf("expected default timeout %s, got %s", outboundhttp.DefaultSSRFProtectedTimeout, c.Timeout)
	}
	if c.Transport == nil {
		t.Fatalf("expected transport")
	}
	if c.CheckRedirect == nil {
		t.Fatalf("expected CheckRedirect configured")
	}
	if err := c.CheckRedirect(&http.Request{}, nil); !errors.Is(err, outboundhttp.ErrRedirectNotAllowed) {
		t.Fatalf("expected ErrRedirectNotAllowed, got %v", err)
	}
}

func TestSSRFProtectedHTTPClient_PreservesBaseTimeout(t *testing.T) {
	t.Parallel()

	base := &http.Client{Timeout: time.Second}
	c := ssrfProtectedHTTPClient(base)
	if c.Timeout != time.Second {
		t.Fatalf("expected base timeout, got %s", c.Timeout)
	}
}
