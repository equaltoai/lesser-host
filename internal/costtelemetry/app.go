package costtelemetry

import (
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
)

// ServiceName is the canonical service identifier for the cost telemetry worker.
const ServiceName = "cost-telemetry-worker"

// New constructs the cost telemetry worker app.
func New(opts ...apptheory.Option) *apptheory.App {
	return NewWithCollector(nil, opts...)
}

// NewWithCollector constructs the cost telemetry worker app with a
// CloudWatchCollector. Pass nil to run without metric collection
// (M3.7 scaffold behavior); pass a collector to enable M3.8+
// CloudWatch metric collection.
func NewWithCollector(collector CloudWatchCollector, opts ...apptheory.Option) *apptheory.App {
	cfg := config.Load()

	srv := NewServer(cfg, collector)

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
