package metrics

import (
	"math"
	"testing"
)

func TestHelpers(t *testing.T) {
	t.Parallel()

	if got := normalizeNamespace(" "); got != "lesser-host" {
		t.Fatalf("expected default namespace, got %q", got)
	}
	if got := normalizeNamespace(" x "); got != "x" {
		t.Fatalf("expected trimmed namespace, got %q", got)
	}

	keys := dimensionKeys(map[string]string{" b ": "x", "": "y", "a": "z"})
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("unexpected keys: %#v", keys)
	}

	payload := map[string]any{}
	defs := addMetricDefs(payload, []Metric{
		{Name: " ", Unit: UnitCount, Value: 1},
		{Name: "nan", Unit: UnitCount, Value: math.NaN()},
		{Name: "inf", Unit: UnitCount, Value: math.Inf(1)},
		{Name: "m", Unit: "", Value: 2},
	})
	if len(defs) != 1 || payload["m"] != 2.0 {
		t.Fatalf("unexpected defs/payload: defs=%#v payload=%#v", defs, payload)
	}
	if defs[0]["Unit"] != string(UnitNone) {
		t.Fatalf("expected unit none, got %#v", defs[0])
	}
}

func TestEmit_DoesNotPanic(t *testing.T) {
	t.Parallel()

	Emit("", map[string]string{"service": "svc"}, []Metric{{Name: "m", Unit: UnitCount, Value: 1}}, map[string]any{
		"ok": true,
	})

	// Marshal failure should be swallowed.
	Emit("x", map[string]string{"service": "svc"}, []Metric{{Name: "m", Unit: UnitCount, Value: 1}}, map[string]any{
		"bad": func() {},
	})
}
