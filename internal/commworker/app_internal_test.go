package commworker

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func TestRegister_ReturnsAppAndHandlesNilInputs(t *testing.T) {
	t.Parallel()

	if got := Register(nil, nil); got != nil {
		t.Fatalf("expected nil app")
	}

	app := apptheory.New()
	if got := Register(app, nil); got != app {
		t.Fatalf("expected same app returned")
	}

	srv := &Server{}
	if got := Register(app, srv); got != app {
		t.Fatalf("expected same app returned")
	}
}
