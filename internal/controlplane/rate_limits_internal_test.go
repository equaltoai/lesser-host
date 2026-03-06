package controlplane

import (
	"os"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

func TestMintConversationRateLimitMiddleware_GuardsAndBypass(t *testing.T) {
	if mw := (*Server)(nil).mintConversationRateLimitMiddleware(); mw != nil {
		t.Fatalf("expected nil middleware for nil server")
	}

	s := &Server{}
	if mw := s.mintConversationRateLimitMiddleware(); mw != nil {
		t.Fatalf("expected nil middleware without store")
	}

	t.Setenv("APPTHEORY_RATE_LIMIT_TABLE_NAME", "")
	s = &Server{store: store.New(newSoulLifecycleTestDB().db)}
	if mw := s.mintConversationRateLimitMiddleware(); mw != nil {
		t.Fatalf("expected nil middleware without configured table")
	}

	t.Setenv("APPTHEORY_RATE_LIMIT_TABLE_NAME", "")
	s = &Server{
		store: store.New(newSoulLifecycleTestDB().db),
		cfg:   config.Config{StateTableName: "state-table"},
	}
	mw := s.mintConversationRateLimitMiddleware()
	if mw == nil {
		t.Fatalf("expected middleware when state table is available")
	}
	if got := os.Getenv("APPTHEORY_RATE_LIMIT_TABLE_NAME"); got != "state-table" {
		t.Fatalf("expected rate limit table env to be set, got %q", got)
	}

	if got := mw(nil); got != nil {
		t.Fatalf("expected nil handler to remain nil")
	}

	calls := 0
	next := func(ctx *apptheory.Context) (*apptheory.Response, error) {
		calls++
		return apptheory.JSON(200, map[string]any{"ok": true})
	}
	wrapped := mw(next)

	if _, err := wrapped(nil); err != nil {
		t.Fatalf("expected nil context to bypass limiter, got %v", err)
	}
	if _, err := wrapped(&apptheory.Context{Request: apptheory.Request{Method: "GET", Path: "/api/v1/soul/agents/123/mint-conversation"}}); err != nil {
		t.Fatalf("expected GET to bypass limiter, got %v", err)
	}
	if _, err := wrapped(&apptheory.Context{Request: apptheory.Request{Method: "POST", Path: "/api/v1/other"}}); err != nil {
		t.Fatalf("expected non-mint path to bypass limiter, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected next handler to be called 3 times, got %d", calls)
	}
}
