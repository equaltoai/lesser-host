package trust

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store"
)

// ServiceName is the canonical service identifier for the trust API.
const ServiceName = "trust-api"

// New constructs the trust API app.
func New(opts ...apptheory.Option) *apptheory.App {
	cfg := config.Load()
	resolveTrustSoulRPCURLFromSSM(&cfg)
	resolveTrustSoulPackBucketNameFromSSM(&cfg)

	db, err := store.LambdaInit()
	if err != nil {
		panic(err)
	}

	srv := NewServer(cfg, store.New(db))
	opts = append(opts, apptheory.WithAuthHook(srv.InstanceAuthHook))

	app := apptheory.New(opts...)
	if mw := srv.aiRateLimitMiddleware(); mw != nil {
		app.Use(mw)
	}
	Register(app, srv)
	return app
}

func resolveTrustSoulRPCURLFromSSM(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if strings.TrimSpace(cfg.SoulRPCURL) != "" {
		return
	}
	paramName := strings.TrimSpace(cfg.SoulRPCURLSSMParam)
	if paramName == "" {
		return
	}
	if !trustRunningInLambda() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	val, err := secrets.GetSSMParameter(ctx, nil, paramName)
	if err != nil {
		log.Printf("trust: failed to resolve SOUL_RPC_URL from SSM param %q: %v", paramName, err)
		return
	}
	cfg.SoulRPCURL = val
}

func resolveTrustSoulPackBucketNameFromSSM(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if strings.TrimSpace(cfg.SoulPackBucketName) != "" {
		return
	}

	paramName := strings.TrimSpace(cfg.SoulPackBucketNameSSMParam)
	if paramName == "" {
		stage := strings.ToLower(strings.TrimSpace(cfg.Stage))
		if stage == "" {
			return
		}
		paramName = "/soul/" + stage + "/packBucketName"
	}
	if !trustRunningInLambda() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	val, err := secrets.GetSSMParameter(ctx, nil, paramName)
	if err != nil {
		log.Printf("trust: failed to resolve SOUL_PACK_BUCKET_NAME from SSM param %q: %v", paramName, err)
		return
	}
	cfg.SoulPackBucketName = val
}

func trustRunningInLambda() bool {
	return os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" || os.Getenv("AWS_EXECUTION_ENV") != ""
}

// Register registers trust API routes and hooks with an app.
func Register(app *apptheory.App, srv *Server) *apptheory.App {
	if app == nil {
		return app
	}

	app.Get("/healthz", healthz)

	if srv != nil {
		srv.RegisterRoutes(app)
	}

	return app
}

func healthz(_ *apptheory.Context) (*apptheory.Response, error) {
	return apptheory.MustJSON(http.StatusOK, map[string]any{
		"ok":      true,
		"service": ServiceName,
	}), nil
}
