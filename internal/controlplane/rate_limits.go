package controlplane

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/theory-cloud/apptheory/pkg/limited"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	mintConversationRateLimitPerMinute             = 10
	mintConversationRateLimitPerHour               = 200
	mailboxRateLimitPerMinute                      = 120
	mailboxRateLimitPerHour                        = 1000
	mintConversationInstanceReadRateLimitPerMinute = 120
	mintConversationInstanceReadRateLimitPerHour   = 1000

	soulMintConversationInstanceReadRateLimitPrefix    = "soul-mint-read:"
	soulMintConversationInstanceReadRateLimitAnonymous = soulMintConversationInstanceReadRateLimitPrefix + "anonymous"
	soulMintConversationInstanceReadRateLimitInvalid   = soulMintConversationInstanceReadRateLimitPrefix + "invalid"
	soulMintConversationInstanceReadRateLimitRouteKey  = "route_class"

	ctxKeySoulMintConversationInstanceReadKey = "soul_mint.instance_read_key"
)

func (s *Server) mintConversationRateLimitMiddleware() apptheory.Middleware {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil
	}

	// Reuse the existing state table unless a dedicated rate-limit table is configured.
	if strings.TrimSpace(os.Getenv("APPTHEORY_RATE_LIMIT_TABLE_NAME")) == "" {
		if name := strings.TrimSpace(s.cfg.StateTableName); name != "" {
			_ = os.Setenv("APPTHEORY_RATE_LIMIT_TABLE_NAME", name)
		}
	}
	if strings.TrimSpace(os.Getenv("APPTHEORY_RATE_LIMIT_TABLE_NAME")) == "" {
		return nil
	}

	strategy := limited.NewMultiWindowStrategy([]limited.WindowConfig{
		{Duration: time.Minute, MaxRequests: mintConversationRateLimitPerMinute},
		{Duration: time.Hour, MaxRequests: mintConversationRateLimitPerHour},
	})

	limiter := limited.NewDynamoRateLimiter(s.store.DB, limited.DefaultConfig(), strategy)
	rateLimitMW := apptheory.RateLimitMiddleware(apptheory.RateLimitConfig{
		Limiter:           limiter,
		FailClosed:        true,
		ExtractIdentifier: mintConversationRateLimitIdentifier,
	})

	return func(next apptheory.Handler) apptheory.Handler {
		if next == nil {
			return next
		}
		limitedNext := rateLimitMW(next)
		return func(ctx *apptheory.Context) (*apptheory.Response, error) {
			if ctx == nil {
				return next(ctx)
			}
			method := strings.ToUpper(strings.TrimSpace(ctx.Request.Method))
			path := strings.TrimSpace(ctx.Request.Path)
			if method == "POST" && strings.Contains(path, "/mint-conversation") {
				return limitedNext(ctx)
			}
			return next(ctx)
		}
	}
}

func (s *Server) mailboxRateLimitMiddleware() apptheory.Middleware {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil
	}
	if strings.TrimSpace(os.Getenv("APPTHEORY_RATE_LIMIT_TABLE_NAME")) == "" {
		if name := strings.TrimSpace(s.cfg.StateTableName); name != "" {
			_ = os.Setenv("APPTHEORY_RATE_LIMIT_TABLE_NAME", name)
		}
	}
	if strings.TrimSpace(os.Getenv("APPTHEORY_RATE_LIMIT_TABLE_NAME")) == "" {
		return nil
	}

	strategy := limited.NewMultiWindowStrategy([]limited.WindowConfig{
		{Duration: time.Minute, MaxRequests: mailboxRateLimitPerMinute},
		{Duration: time.Hour, MaxRequests: mailboxRateLimitPerHour},
	})
	limiter := limited.NewDynamoRateLimiter(s.store.DB, limited.DefaultConfig(), strategy)
	rateLimitMW := apptheory.RateLimitMiddleware(apptheory.RateLimitConfig{
		Limiter:           limiter,
		FailClosed:        true,
		ExtractIdentifier: mailboxRateLimitIdentifier,
	})

	return func(next apptheory.Handler) apptheory.Handler {
		if next == nil {
			return next
		}
		limitedNext := rateLimitMW(next)
		return func(ctx *apptheory.Context) (*apptheory.Response, error) {
			if ctx == nil {
				return next(ctx)
			}
			path := strings.TrimSpace(ctx.Request.Path)
			if strings.HasPrefix(path, "/api/v1/soul/comm/mailbox/") || strings.HasPrefix(path, "/api/v1/soul/comm/contactability/") {
				return limitedNext(ctx)
			}
			return next(ctx)
		}
	}
}

func (s *Server) mintConversationInstanceReadRateLimitMiddleware() apptheory.Middleware {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil
	}
	if strings.TrimSpace(os.Getenv("APPTHEORY_RATE_LIMIT_TABLE_NAME")) == "" {
		if name := strings.TrimSpace(s.cfg.StateTableName); name != "" {
			_ = os.Setenv("APPTHEORY_RATE_LIMIT_TABLE_NAME", name)
		}
	}
	if strings.TrimSpace(os.Getenv("APPTHEORY_RATE_LIMIT_TABLE_NAME")) == "" {
		return nil
	}

	strategy := limited.NewMultiWindowStrategy([]limited.WindowConfig{
		{Duration: time.Minute, MaxRequests: mintConversationInstanceReadRateLimitPerMinute},
		{Duration: time.Hour, MaxRequests: mintConversationInstanceReadRateLimitPerHour},
	})
	limiter := limited.NewDynamoRateLimiter(s.store.DB, limited.DefaultConfig(), strategy)

	return func(next apptheory.Handler) apptheory.Handler {
		if next == nil {
			return next
		}
		return func(ctx *apptheory.Context) (*apptheory.Response, error) {
			if ctx == nil {
				return next(ctx)
			}
			method := strings.ToUpper(strings.TrimSpace(ctx.Request.Method))
			path := strings.TrimSpace(ctx.Request.Path)
			if method == http.MethodGet && isSoulMintConversationInstanceReadPath(path) {
				if resp, err := s.soulMintConversationInstanceReadCheckRateLimit(ctx, limiter, method, path); resp != nil || err != nil {
					return resp, err
				}
			}
			return next(ctx)
		}
	}
}

func isSoulMintConversationInstanceReadPath(path string) bool {
	return strings.HasPrefix(strings.TrimSpace(path), "/api/v1/soul/instance/agents/") &&
		strings.Contains(strings.TrimSpace(path), "/mint-conversations")
}

func mintConversationRateLimitIdentifier(ctx *apptheory.Context) string {
	if ctx == nil {
		return httpx.SourceRateLimitIdentifier(nil, "mint-conversation:anonymous")
	}
	if id := strings.TrimSpace(ctx.AuthIdentity); id != "" {
		return id
	}
	return httpx.SourceRateLimitIdentifier(ctx, "mint-conversation:anonymous")
}

func mailboxRateLimitIdentifier(ctx *apptheory.Context) string {
	if ctx == nil {
		return httpx.SourceRateLimitIdentifier(nil, "mailbox:anonymous")
	}
	if raw := httpx.BearerToken(ctx.Request.Headers); strings.TrimSpace(raw) != "" {
		return "mailbox:" + sha256HexTrimmed(raw)
	}
	return httpx.SourceRateLimitIdentifier(ctx, "mailbox:anonymous")
}

func (s *Server) soulMintConversationInstanceReadRateLimitIdentifier(ctx *apptheory.Context) (string, *apptheory.AppTheoryError) {
	if ctx == nil {
		return httpx.SourceRateLimitIdentifier(nil, soulMintConversationInstanceReadRateLimitAnonymous), nil
	}
	raw := httpx.BearerToken(ctx.Request.Headers)
	if strings.TrimSpace(raw) == "" {
		return httpx.SourceRateLimitIdentifier(ctx, soulMintConversationInstanceReadRateLimitAnonymous), nil
	}
	hash := sha256HexTrimmed(raw)
	if key := soulMintConversationInstanceReadKeyFromContext(ctx); key != nil && soulMintConversationInstanceReadKeyActiveForHash(key, hash) {
		return soulMintConversationInstanceReadRateLimitPrefix + hash, nil
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return "", soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	key, err := s.store.GetInstanceKey(ctx.Context(), hash)
	if err != nil {
		if theoryErrors.IsNotFound(err) {
			return httpx.SourceRateLimitIdentifier(ctx, soulMintConversationInstanceReadRateLimitInvalid), nil
		}
		return "", soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	if key == nil || !soulMintConversationInstanceReadKeyActiveForHash(key, hash) {
		return httpx.SourceRateLimitIdentifier(ctx, soulMintConversationInstanceReadRateLimitInvalid), nil
	}
	ctx.Set(ctxKeySoulMintConversationInstanceReadKey, key)
	return soulMintConversationInstanceReadRateLimitPrefix + hash, nil
}

func (s *Server) soulMintConversationInstanceReadCheckRateLimit(ctx *apptheory.Context, limiter limited.AtomicRateLimiter, method string, path string) (*apptheory.Response, error) {
	if limiter == nil {
		return nil, nil
	}
	identifier, appErr := s.soulMintConversationInstanceReadRateLimitIdentifier(ctx)
	if appErr != nil {
		return nil, appErr
	}
	decision, err := limiter.CheckAndIncrement(ctx.Context(), limited.RateLimitKey{
		Identifier: identifier,
		Resource:   "control-plane-api",
		Operation:  "soul_mint_conversation_instance_read",
		Metadata:   soulMintConversationInstanceReadRateLimitMetadata(ctx, method, path),
	})
	if err != nil {
		return nil, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	ctx.Set(apptheory.RateLimitDecisionKey, decision)
	if decision != nil && !decision.Allowed {
		return soulMintConversationInstanceReadRateLimitResponse(ctx, decision), nil
	}
	return nil, nil
}

func soulMintConversationInstanceReadRateLimitMetadata(ctx *apptheory.Context, method string, path string) map[string]string {
	metadata := httpx.SourceRateLimitMetadata(ctx)
	metadata["method"] = method
	metadata[soulMintConversationInstanceReadRateLimitRouteKey] = soulMintConversationInstanceReadRateLimitRouteClass(path)
	return metadata
}

func soulMintConversationInstanceReadKeyFromContext(ctx *apptheory.Context) *models.InstanceKey {
	if ctx == nil {
		return nil
	}
	key, _ := ctx.Get(ctxKeySoulMintConversationInstanceReadKey).(*models.InstanceKey)
	return key
}

func soulMintConversationInstanceReadKeyActiveForHash(key *models.InstanceKey, hash string) bool {
	return key != nil &&
		strings.TrimSpace(hash) != "" &&
		strings.TrimSpace(key.ID) == strings.TrimSpace(hash) &&
		key.RevokedAt.IsZero()
}

func soulMintConversationInstanceReadRateLimitRouteClass(path string) string {
	if strings.Contains(strings.TrimSpace(path), "/mint-conversations/") {
		return soulMintInstanceReadRouteSingle
	}
	return soulMintInstanceReadRouteList
}

func soulMintConversationInstanceReadRateLimitResponse(ctx *apptheory.Context, decision *limited.LimitDecision) *apptheory.Response {
	headers := map[string][]string{
		soulMintInstanceReadHeaderContentType: {soulMintInstanceReadJSONContentType},
	}
	details := map[string]any{}
	if decision != nil && decision.RetryAfter != nil {
		retrySeconds := int((decision.RetryAfter.Nanoseconds() + int64(time.Second) - 1) / int64(time.Second))
		if retrySeconds < 1 {
			retrySeconds = 1
		}
		headers["retry-after"] = []string{strconv.Itoa(retrySeconds)}
		details["retry_after_seconds"] = retrySeconds
	}
	errBody := map[string]any{
		"code":                              soulMintInstanceReadCodeRateLimited,
		soulMintInstanceReadEnvelopeMessage: soulMintInstanceReadMessageRateLimited,
		"status_code":                       http.StatusTooManyRequests,
	}
	requestID := ""
	if ctx != nil {
		requestID = strings.TrimSpace(ctx.RequestID)
	}
	if requestID != "" {
		errBody["request_id"] = requestID
	}
	if len(details) > 0 {
		errBody["details"] = details
	}
	body, err := json.Marshal(map[string]any{soulMintInstanceReadEnvelopeError: errBody})
	if err != nil {
		body = []byte(`{"error":{"code":"` + soulMintInstanceReadCodeRateLimited + `","message":"` + soulMintInstanceReadMessageRateLimited + `","status_code":429}}`)
	}
	return &apptheory.Response{
		Status:  http.StatusTooManyRequests,
		Headers: headers,
		Body:    body,
	}
}
