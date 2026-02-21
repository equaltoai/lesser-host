package soulreputation

import (
	"math"
	"testing"
	"time"
)

func TestWeightsNormalized_DefaultsToEconomic(t *testing.T) {
	t.Parallel()

	got := (Weights{}).Normalized()
	if got.Economic != 1 || got.Social != 0 || got.Validation != 0 || got.Trust != 0 {
		t.Fatalf("unexpected normalized weights: %#v", got)
	}
}

func TestWeightsNormalized_NormalizesToUnitSum(t *testing.T) {
	t.Parallel()

	got := (Weights{Economic: 2, Social: 1, Validation: 1}).Normalized()
	sum := got.Economic + got.Social + got.Validation + got.Trust
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("expected unit sum, got %#v (sum=%f)", got, sum)
	}
}

func TestComputeV0_EconomicOnly_UsesTipScore(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 21, 0, 0, 0, 0, time.UTC)
	rep := ComputeV0(
		"0x00000000000000000000000000000000000000000000000000000000000000aa",
		123,
		now,
		V0Config{TipScale: 10, Weights: Weights{Economic: 1}},
		SignalCounts{TipsReceived: 3},
		SignalScores{},
	)

	wantEconomic := 1 - math.Exp(-0.3)
	if math.Abs(rep.Economic-wantEconomic) > 1e-9 {
		t.Fatalf("expected economic=%f, got %f", wantEconomic, rep.Economic)
	}
	if math.Abs(rep.Composite-rep.Economic) > 1e-9 {
		t.Fatalf("expected composite==economic, got composite=%f economic=%f", rep.Composite, rep.Economic)
	}
	if rep.TipsReceived != 3 {
		t.Fatalf("expected tips_received=3, got %#v", rep)
	}
	if rep.BlockRef != 123 {
		t.Fatalf("expected block_ref=123, got %#v", rep)
	}
	if !rep.UpdatedAt.Equal(now) {
		t.Fatalf("expected updated_at=%v, got %#v", now, rep.UpdatedAt)
	}
}
