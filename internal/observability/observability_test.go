package observability

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func TestNew_ProvidesHooks(t *testing.T) {
	hooks := New("svc")
	hooks.Log(apptheory.LogRecord{Level: "warn", Event: "e"})
	hooks.Log(apptheory.LogRecord{Level: "error", Event: "e"})
	hooks.Metric(apptheory.MetricRecord{Name: "m", Value: 1})
}
