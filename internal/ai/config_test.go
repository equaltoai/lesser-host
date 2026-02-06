package ai

import (
	"testing"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestDefaultAndEffectiveInstanceConfig(t *testing.T) {
	t.Parallel()

	def := DefaultInstanceConfig()
	if def.Enabled {
		t.Fatalf("expected default disabled")
	}
	if def.ModelSet == "" {
		t.Fatalf("expected default model set")
	}
	if def.BatchMaxItems <= 0 || def.BatchMaxTotalBytes <= 0 {
		t.Fatalf("expected batching defaults set: %#v", def)
	}
	if def.PricingMultiplierBps <= 0 || def.MaxInflightJobs <= 0 {
		t.Fatalf("expected pricing + inflight defaults set: %#v", def)
	}

	if got := EffectiveInstanceConfig(nil); got != def {
		t.Fatalf("expected nil instance => default config; got=%#v want=%#v", got, def)
	}

	t.Run("OverridesValidFields", func(t *testing.T) {
		t.Parallel()

		enabled := true
		bps := int64(12000)
		inflight := int64(42)
		inst := &models.Instance{
			AIEnabled:              &enabled,
			AIModelSet:             " openai:test ",
			AIBatchingMode:         "WORKER",
			AIBatchMaxItems:        2,
			AIBatchMaxTotalBytes:   123,
			AIPricingMultiplierBps: &bps,
			AIMaxInflightJobs:      &inflight,
		}
		got := EffectiveInstanceConfig(inst)
		if !got.Enabled {
			t.Fatalf("expected enabled")
		}
		if got.ModelSet != "openai:test" {
			t.Fatalf("expected trimmed model set, got %q", got.ModelSet)
		}
		if got.BatchingMode != "worker" {
			t.Fatalf("expected lowercased batching mode, got %q", got.BatchingMode)
		}
		if got.BatchMaxItems != 2 || got.BatchMaxTotalBytes != 123 {
			t.Fatalf("unexpected batch overrides: %#v", got)
		}
		if got.PricingMultiplierBps != 12000 || got.MaxInflightJobs != 42 {
			t.Fatalf("unexpected overrides: %#v", got)
		}
	})

	t.Run("IgnoresInvalidFields", func(t *testing.T) {
		t.Parallel()

		enabled := true
		bps := int64(0)
		inflight := int64(-1)
		inst := &models.Instance{
			AIEnabled:              &enabled,
			AIBatchingMode:         "not_a_mode",
			AIPricingMultiplierBps: &bps,
			AIMaxInflightJobs:      &inflight,
		}
		got := EffectiveInstanceConfig(inst)
		if !got.Enabled {
			t.Fatalf("expected enabled")
		}
		if got.BatchingMode != def.BatchingMode {
			t.Fatalf("expected invalid batching mode ignored, got %q", got.BatchingMode)
		}
		if got.PricingMultiplierBps != def.PricingMultiplierBps {
			t.Fatalf("expected invalid bps ignored, got %d", got.PricingMultiplierBps)
		}
		if got.MaxInflightJobs != def.MaxInflightJobs {
			t.Fatalf("expected invalid max inflight ignored, got %d", got.MaxInflightJobs)
		}
	})
}

