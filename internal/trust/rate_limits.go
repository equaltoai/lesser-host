package trust

import (
	"os"
	"strings"
	"time"

	"github.com/theory-cloud/apptheory/v4/pkg/limited"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
)

const (
	aiRateLimitPerMinute = 60
	aiRateLimitPerHour   = 1000
)

func (s *Server) aiRateLimitMiddleware() apptheory.Middleware {
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
		{Duration: time.Minute, MaxRequests: aiRateLimitPerMinute},
		{Duration: time.Hour, MaxRequests: aiRateLimitPerHour},
	})

	limiter := limited.NewDynamoRateLimiter(s.store.DB, limited.DefaultConfig(), strategy)

	rateLimitMW := apptheory.RateLimitMiddleware(apptheory.RateLimitConfig{
		Limiter:           limiter,
		FailClosed:        true,
		ExtractIdentifier: aiRateLimitIdentifier,
	})

	return func(next apptheory.Handler) apptheory.Handler {
		if next == nil {
			return next
		}
		limitedNext := rateLimitMW(next)
		return func(ctx *apptheory.Context) (*apptheory.Response, error) {
			path := ""
			if ctx != nil {
				path = strings.TrimSpace(ctx.Request.Path)
			}
			if strings.HasPrefix(path, "/api/v1/ai/") {
				return limitedNext(ctx)
			}
			return next(ctx)
		}
	}
}

func aiRateLimitIdentifier(ctx *apptheory.Context) string {
	if ctx == nil {
		return httpx.SourceRateLimitIdentifier(nil, "trust-ai:anonymous")
	}
	if id := strings.TrimSpace(ctx.AuthIdentity); id != "" {
		return id
	}
	return httpx.SourceRateLimitIdentifier(ctx, "trust-ai:anonymous")
}
