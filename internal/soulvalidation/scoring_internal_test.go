package soulvalidation

import (
	"math"
	"testing"
	"time"
)

func TestScoreDelta_IdentityVerify(t *testing.T) {
	t.Parallel()

	if got := ScoreDelta("identity_verify", "pass"); math.Abs(got-0.05) > 1e-9 {
		t.Fatalf("expected pass delta=0.05, got %f", got)
	}
	if got := ScoreDelta("identity_verify", "fail"); math.Abs(got-(-0.025)) > 1e-9 {
		t.Fatalf("expected fail delta=-0.025, got %f", got)
	}
	if got := ScoreDelta("identity_verify", "timeout"); math.Abs(got-(-0.0125)) > 1e-9 {
		t.Fatalf("expected timeout delta=-0.0125, got %f", got)
	}
}

func TestComputeProgressiveScore_AppliesEpochDecay(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(14 * 24 * time.Hour)

	score, passed := ComputeProgressiveScore(
		[]Event{{EvaluatedAt: start, Result: "pass", Delta: 0.1}},
		now,
		Config{Epoch: 7 * 24 * time.Hour, DecayRate: 0.10},
	)

	// 2 epochs of 10% decay: 0.1 * 0.9^2.
	want := 0.1 * 0.9 * 0.9
	if math.Abs(score-want) > 1e-9 {
		t.Fatalf("expected score=%f, got %f", want, score)
	}
	if passed != 1 {
		t.Fatalf("expected passed=1, got %d", passed)
	}
}

func TestComputeProgressiveScore_AccumulatesAndClamps(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(1 * time.Hour)

	score, passed := ComputeProgressiveScore(
		[]Event{
			{EvaluatedAt: start, Result: "pass", Delta: 0.7},
			{EvaluatedAt: start.Add(1 * time.Minute), Result: "pass", Delta: 0.7},
		},
		now,
		Config{Epoch: 7 * 24 * time.Hour, DecayRate: 0.01},
	)

	if score != 1 {
		t.Fatalf("expected score clamped to 1, got %f", score)
	}
	if passed != 2 {
		t.Fatalf("expected passed=2, got %d", passed)
	}
}
