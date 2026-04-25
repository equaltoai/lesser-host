package controlplane

import (
	"os"
	"strings"
	"time"

	"github.com/theory-cloud/apptheory/pkg/limited"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
)

const (
	mintConversationRateLimitPerMinute = 10
	mintConversationRateLimitPerHour   = 200
	mailboxRateLimitPerMinute          = 120
	mailboxRateLimitPerHour            = 1000
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
		Limiter:    limiter,
		FailClosed: true,
		ExtractIdentifier: func(ctx *apptheory.Context) string {
			if ctx == nil {
				return "anonymous"
			}
			id := strings.TrimSpace(ctx.AuthIdentity)
			if id == "" {
				return "anonymous"
			}
			return id
		},
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
		Limiter:    limiter,
		FailClosed: true,
		ExtractIdentifier: func(ctx *apptheory.Context) string {
			if ctx == nil {
				return "mailbox:anonymous"
			}
			if raw := httpx.BearerToken(ctx.Request.Headers); strings.TrimSpace(raw) != "" {
				return "mailbox:" + sha256HexTrimmed(raw)
			}
			return "mailbox:anonymous"
		},
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
			if strings.HasPrefix(strings.TrimSpace(ctx.Request.Path), "/api/v1/soul/comm/mailbox/") {
				return limitedNext(ctx)
			}
			return next(ctx)
		}
	}
}
