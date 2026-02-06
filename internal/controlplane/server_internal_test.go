package controlplane

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
)

func TestServer_RegisterRoutes(t *testing.T) {
	t.Parallel()

	app := apptheory.New()
	srv := NewServer(config.Config{}, nil)
	srv.RegisterRoutes(app)
}

