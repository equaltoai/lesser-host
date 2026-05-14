package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/theory-cloud/apptheory/pkg/limited"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
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

func TestMintConversationInstanceReadRateLimitMiddleware_GuardsAndPathMatch(t *testing.T) {
	if mw := (*Server)(nil).mintConversationInstanceReadRateLimitMiddleware(); mw != nil {
		t.Fatalf("expected nil middleware for nil server")
	}

	t.Setenv("APPTHEORY_RATE_LIMIT_TABLE_NAME", "")
	s := &Server{store: store.New(newSoulLifecycleTestDB().db)}
	if mw := s.mintConversationInstanceReadRateLimitMiddleware(); mw != nil {
		t.Fatalf("expected nil middleware without configured table")
	}

	if !isSoulMintConversationInstanceReadPath("/api/v1/soul/instance/agents/0xabc/mint-conversations") {
		t.Fatalf("expected list route to be classified as instance mint read")
	}
	if !isSoulMintConversationInstanceReadPath("/api/v1/soul/instance/agents/0xabc/mint-conversations/conv-1") {
		t.Fatalf("expected single route to be classified as instance mint read")
	}
	if isSoulMintConversationInstanceReadPath("/api/v1/soul/agents/0xabc/mint-conversations") {
		t.Fatalf("portal route must not be classified as instance mint read")
	}

	retry := 1500 * time.Millisecond
	resp := soulMintConversationInstanceReadRateLimitResponse(&apptheory.Context{RequestID: "req-rate"}, &limited.LimitDecision{RetryAfter: &retry})
	if resp.Status != 429 || resp.Headers["retry-after"][0] != "2" {
		t.Fatalf("expected 429 with rounded Retry-After=2, got status=%d headers=%#v", resp.Status, resp.Headers)
	}
	var body map[string]map[string]any
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("unmarshal rate-limit body: %v", err)
	}
	if body["error"]["code"] != soulMintInstanceReadCodeRateLimited || body["error"]["request_id"] != "req-rate" {
		t.Fatalf("unexpected rate-limit body: %#v", body)
	}
}

func TestMintConversationInstanceReadRateLimitCheck_UsesHashedBearerAndCachesKey(t *testing.T) {
	t.Parallel()

	s, qKey := newMintConversationInstanceReadRateLimitTestServer()
	rawKey := "raw-instance-key"
	keyHash := sha256HexTrimmed(rawKey)
	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: keyHash, InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()}
	}).Once()

	ctx := &apptheory.Context{
		RequestID: "req-rate-check",
		Request: apptheory.Request{
			Headers: map[string][]string{mintConversationInstanceReadTestAuthHeader: {"Bearer " + rawKey}},
		},
	}
	limiter := &mintConversationInstanceReadTestLimiter{
		decision: &limited.LimitDecision{Allowed: true},
	}

	resp, err := s.soulMintConversationInstanceReadCheckRateLimit(ctx, limiter, "GET", "/api/v1/soul/instance/agents/agent/mint-conversations")
	if resp != nil || err != nil {
		t.Fatalf("expected allowed decision to pass through, got resp=%#v err=%v", resp, err)
	}
	if limiter.calls != 1 {
		t.Fatalf("expected one limiter call, got %d", limiter.calls)
	}
	if limiter.lastKey.Identifier != soulMintConversationInstanceReadRateLimitPrefix+keyHash {
		t.Fatalf("expected hashed bearer identifier, got %q", limiter.lastKey.Identifier)
	}
	if cached := soulMintConversationInstanceReadKeyFromContext(ctx); cached == nil || cached.ID != keyHash {
		t.Fatalf("expected valid key cached for handler reuse, got %#v", cached)
	}
	key, appErr := s.requireSoulMintInstanceReadKey(ctx)
	if appErr != nil || key == nil || key.ID != keyHash {
		t.Fatalf("expected handler auth to reuse cached key, key=%#v err=%#v", key, appErr)
	}
	if limiter.lastKey.Metadata["method"] != http.MethodGet ||
		limiter.lastKey.Metadata[soulMintConversationInstanceReadRateLimitRouteKey] != soulMintInstanceReadRouteList {
		t.Fatalf("expected method/path metadata, got %#v", limiter.lastKey.Metadata)
	}
	if routeClass := soulMintConversationInstanceReadRateLimitRouteClass("/api/v1/soul/instance/agents/agent/mint-conversations/conv-1"); routeClass != soulMintInstanceReadRouteSingle {
		t.Fatalf("expected single route class, got %q", routeClass)
	}

	qKey.AssertExpectations(t)
}

func TestMintConversationInstanceReadRateLimitCheck_TypedErrorsAndAnonymousIdentifier(t *testing.T) {
	t.Parallel()

	s, _ := newMintConversationInstanceReadRateLimitTestServer()
	ctx := &apptheory.Context{RequestID: "req-rate-check"}
	limiter := &mintConversationInstanceReadTestLimiter{}

	retry := time.Second
	limiter.decision = &limited.LimitDecision{Allowed: false, RetryAfter: &retry}
	resp, err := s.soulMintConversationInstanceReadCheckRateLimit(ctx, limiter, "GET", "/api/v1/soul/instance/agents/agent/mint-conversations")
	if err != nil || resp == nil || resp.Status != http.StatusTooManyRequests {
		t.Fatalf("expected typed 429 response, got resp=%#v err=%v", resp, err)
	}
	if limiter.lastKey.Identifier != soulMintConversationInstanceReadRateLimitAnonymous {
		t.Fatalf("expected anonymous identifier, got %q", limiter.lastKey.Identifier)
	}

	limiter.err = errors.New("rate limit store unavailable")
	resp, err = s.soulMintConversationInstanceReadCheckRateLimit(ctx, limiter, "GET", "/api/v1/soul/instance/agents/agent/mint-conversations")
	appErr := requireAppTheoryError(t, err)
	if resp != nil || appErr.Code != soulMintInstanceReadCodeInternal || appErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected internal app error on limiter failure, got resp=%#v err=%#v", resp, appErr)
	}

	if id, err := s.soulMintConversationInstanceReadRateLimitIdentifier(&apptheory.Context{}); err != nil || id != soulMintConversationInstanceReadRateLimitAnonymous {
		t.Fatalf("expected anonymous identifier, got %q", id)
	}
}

func TestMintConversationInstanceReadRateLimitCheck_InvalidBearerUsesSharedBudget(t *testing.T) {
	t.Parallel()

	s, qKey := newMintConversationInstanceReadRateLimitTestServer()
	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{
		RequestID: "req-invalid-rate-check",
		Request: apptheory.Request{
			Headers: map[string][]string{mintConversationInstanceReadTestAuthHeader: {"Bearer invalid-instance-key"}},
		},
	}
	limiter := &mintConversationInstanceReadTestLimiter{
		decision: &limited.LimitDecision{Allowed: true},
	}

	resp, err := s.soulMintConversationInstanceReadCheckRateLimit(ctx, limiter, "GET", "/api/v1/soul/instance/agents/agent/mint-conversations")
	if resp != nil || err != nil {
		t.Fatalf("expected invalid bearer to consume limiter then continue to auth, got resp=%#v err=%v", resp, err)
	}
	if limiter.calls != 1 {
		t.Fatalf("expected one limiter call, got %d", limiter.calls)
	}
	if limiter.lastKey.Identifier != soulMintConversationInstanceReadRateLimitInvalid {
		t.Fatalf("expected invalid bearer to use shared invalid budget, got %q", limiter.lastKey.Identifier)
	}
	if cached := soulMintConversationInstanceReadKeyFromContext(ctx); cached != nil {
		t.Fatalf("invalid bearer must not cache an instance key, got %#v", cached)
	}
	qKey.AssertExpectations(t)
}

func newMintConversationInstanceReadRateLimitTestServer() (*Server, *ttmocks.MockQuery) {
	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qKey).Maybe()
	qKey.On("ConsistentRead").Return(qKey).Maybe()
	qKey.On("IfExists").Return(qKey).Maybe()
	qKey.On("Update", mock.Anything).Return(nil).Maybe()
	return &Server{store: store.New(db)}, qKey
}

type mintConversationInstanceReadTestLimiter struct {
	decision *limited.LimitDecision
	lastKey  limited.RateLimitKey
	err      error
	calls    int
}

func (l *mintConversationInstanceReadTestLimiter) CheckLimit(context.Context, limited.RateLimitKey) (*limited.LimitDecision, error) {
	return l.decision, l.err
}

func (l *mintConversationInstanceReadTestLimiter) RecordRequest(context.Context, limited.RateLimitKey) error {
	return l.err
}

func (l *mintConversationInstanceReadTestLimiter) GetUsage(context.Context, limited.RateLimitKey) (*limited.UsageStats, error) {
	return nil, l.err
}

func (l *mintConversationInstanceReadTestLimiter) CheckAndIncrement(_ context.Context, key limited.RateLimitKey) (*limited.LimitDecision, error) {
	l.calls++
	l.lastKey = key
	return l.decision, l.err
}
