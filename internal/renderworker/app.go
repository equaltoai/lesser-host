package renderworker

import (
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

const ServiceName = "render-worker"

func New(opts ...apptheory.Option) *apptheory.App {
	cfg := config.Load()

	db, err := store.LambdaInit()
	if err != nil {
		panic(err)
	}

	srv := NewServer(cfg, store.New(db), artifacts.New(cfg.ArtifactBucketName))

	app := apptheory.New(opts...)
	Register(app, srv)
	return app
}

func Register(app *apptheory.App, srv *Server) *apptheory.App {
	if app == nil {
		return app
	}
	if srv != nil {
		srv.Register(app)
	}
	return app
}
