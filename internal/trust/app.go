package trust

import (
	"net/http"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

const ServiceName = "trust-api"

func New(opts ...apptheory.Option) *apptheory.App {
	app := apptheory.New(opts...)
	Register(app)
	return app
}

func Register(app *apptheory.App) *apptheory.App {
	if app == nil {
		return app
	}

	app.Get("/healthz", healthz)

	return app
}

func healthz(_ *apptheory.Context) (*apptheory.Response, error) {
	return apptheory.MustJSON(http.StatusOK, map[string]any{
		"ok":      true,
		"service": ServiceName,
	}), nil
}
