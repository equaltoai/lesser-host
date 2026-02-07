package aiworker

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
)

func TestRegister_ReturnsAppAndHandlesNilInputs(t *testing.T) {
	t.Parallel()

	if got := Register(nil, nil); got != nil {
		t.Fatalf("expected nil app, got %#v", got)
	}

	app := apptheory.New()
	if got := Register(app, nil); got != app {
		t.Fatalf("expected same app")
	}

	srv := &Server{cfg: config.Config{SafetyQueueURL: "https://sqs.us-east-1.amazonaws.com/123/myqueue"}}
	if got := Register(app, srv); got != app {
		t.Fatalf("expected same app")
	}
}
