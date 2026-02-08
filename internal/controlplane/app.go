package controlplane

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store"
)

// ServiceName is the canonical service identifier for the control plane API.
const ServiceName = "control-plane-api"

// New constructs the control plane API app.
func New(opts ...apptheory.Option) *apptheory.App {
	cfg := config.Load()
	resolveTipRPCURLFromSSM(&cfg)

	db, err := store.LambdaInit()
	if err != nil {
		panic(err)
	}

	srv := NewServer(cfg, store.New(db))

	opts = append(opts, apptheory.WithAuthHook(srv.OperatorAuthHook))

	app := apptheory.New(opts...)
	Register(app, srv)
	return app
}

func resolveTipRPCURLFromSSM(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if strings.TrimSpace(cfg.TipRPCURL) != "" {
		return
	}
	paramName := strings.TrimSpace(cfg.TipRPCURLSSMParam)
	if paramName == "" {
		return
	}

	// Tests and local tooling should not require live AWS connections.
	// In Lambda, AWS runtime env vars are always present.
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") == "" && os.Getenv("AWS_EXECUTION_ENV") == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	val, err := secrets.GetSSMParameter(ctx, nil, paramName)
	if err != nil {
		log.Printf("controlplane: failed to resolve TIP_RPC_URL from SSM param %q: %v", paramName, err)
		return
	}
	cfg.TipRPCURL = val
}

// Register registers control plane routes and hooks with an app.
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
