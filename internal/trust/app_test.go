package trust_test

import (
	"context"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-host/internal/trust"
)

func TestHealthz_LambdaFunctionURL(t *testing.T) {
	t.Parallel()

	env := testkit.New()
	app := env.App()
	trust.Register(app)

	event := testkit.LambdaFunctionURLRequest("GET", "/healthz", testkit.HTTPEventOptions{})
	resp := app.ServeLambdaFunctionURL(context.Background(), event)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d (body=%q)", resp.StatusCode, resp.Body)
	}
}

func TestHealthz_Portable(t *testing.T) {
	t.Parallel()

	env := testkit.New()
	app := env.App()
	trust.Register(app)

	resp := env.Invoke(context.Background(), app, apptheory.Request{
		Method: "GET",
		Path:   "/healthz",
	})
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
}
