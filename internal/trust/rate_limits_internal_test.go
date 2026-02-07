package trust

import (
	"os"
	"testing"

	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store"
)

func TestAIRateLimitMiddleware_Basic(t *testing.T) {
	t.Parallel()

	original := os.Getenv("APPTHEORY_RATE_LIMIT_TABLE_NAME")
	t.Cleanup(func() {
		_ = os.Setenv("APPTHEORY_RATE_LIMIT_TABLE_NAME", original)
	})
	_ = os.Unsetenv("APPTHEORY_RATE_LIMIT_TABLE_NAME")

	if (&Server{}).aiRateLimitMiddleware() != nil {
		t.Fatalf("expected nil middleware with nil store")
	}

	if (&Server{store: store.New(nil)}).aiRateLimitMiddleware() != nil {
		t.Fatalf("expected nil middleware with nil db")
	}

	db := ttmocks.NewMockExtendedDB()

	if (&Server{store: store.New(db)}).aiRateLimitMiddleware() != nil {
		t.Fatalf("expected nil middleware when table name not configured")
	}

	s := &Server{
		cfg:   configForTests(),
		store: store.New(db),
	}
	s.cfg.StateTableName = "tbl"

	mw := s.aiRateLimitMiddleware()
	if mw == nil {
		t.Fatalf("expected middleware when table name configured")
	}

	var called int
	next := func(_ *apptheory.Context) (*apptheory.Response, error) {
		called++
		return apptheory.JSON(200, map[string]any{"ok": true})
	}

	h := mw(next)
	if h == nil {
		t.Fatalf("expected wrapped handler")
	}

	resp, err := h(&apptheory.Context{Request: apptheory.Request{Path: "/healthz"}})
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected response: resp=%#v err=%v", resp, err)
	}
	if called != 1 {
		t.Fatalf("expected next called once, got %d", called)
	}

	resp, err = h(&apptheory.Context{Request: apptheory.Request{Path: ""}})
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected response: resp=%#v err=%v", resp, err)
	}
	if called != 2 {
		t.Fatalf("expected next called twice, got %d", called)
	}

	resp, err = h(nil)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected response: resp=%#v err=%v", resp, err)
	}
	if called != 3 {
		t.Fatalf("expected next called thrice, got %d", called)
	}

	if mw(nil) != nil {
		t.Fatalf("expected nil when next is nil")
	}
}
