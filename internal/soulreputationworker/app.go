package soulreputationworker

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store"
)

// ServiceName is the canonical service identifier for the soul reputation worker.
const ServiceName = "soul-reputation-worker"

// New constructs the soul reputation worker app.
func New(opts ...apptheory.Option) *apptheory.App {
	cfg := config.Load()
	resolveTipRPCURLFromSSM(&cfg)
	resolveSoulPackBucketNameFromSSM(&cfg)

	db, err := store.LambdaInit()
	if err != nil {
		panic(err)
	}

	srv := NewServer(cfg, store.New(db), artifacts.New(cfg.SoulPackBucketName))

	app := apptheory.New(opts...)
	Register(app, srv)
	return app
}

// Register registers soul reputation worker handlers with an app.
func Register(app *apptheory.App, srv *Server) *apptheory.App {
	if app == nil {
		return app
	}
	if srv != nil {
		srv.Register(app)
	}
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
		log.Printf("soulreputationworker: failed to resolve TIP_RPC_URL from SSM param %q: %v", paramName, err)
		return
	}
	cfg.TipRPCURL = val
}

func resolveSoulPackBucketNameFromSSM(cfg *config.Config) {
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

	// Tests and local tooling should not require live AWS connections.
	// In Lambda, AWS runtime env vars are always present.
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") == "" && os.Getenv("AWS_EXECUTION_ENV") == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	val, err := secrets.GetSSMParameter(ctx, nil, paramName)
	if err != nil {
		log.Printf("soulreputationworker: failed to resolve SOUL_PACK_BUCKET_NAME from SSM param %q: %v", paramName, err)
		return
	}
	cfg.SoulPackBucketName = val
}
