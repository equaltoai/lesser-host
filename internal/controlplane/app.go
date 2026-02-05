package controlplane

import (
	"net/http"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
)

// ServiceName is the canonical service identifier for the control plane API.
const ServiceName = "control-plane-api"

// New constructs the control plane API app.
func New(opts ...apptheory.Option) *apptheory.App {
	cfg := config.Load()

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
