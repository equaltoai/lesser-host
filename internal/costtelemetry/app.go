package costtelemetry

import (
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
)

// ServiceName is the canonical service identifier for the cost telemetry worker.
const ServiceName = "cost-telemetry-worker"

// New constructs the cost telemetry worker app.
func New(opts ...apptheory.Option) *apptheory.App {
	cfg := config.Load()

	srv := NewServer(cfg)

	app := apptheory.New(opts...)
	Register(app, srv)
	return app
}

// Register registers cost telemetry worker handlers with an app.
func Register(app *apptheory.App, srv *Server) *apptheory.App {
	if app == nil {
		return app
	}
	if srv != nil {
		srv.Register(app)
	}
	return app
}
