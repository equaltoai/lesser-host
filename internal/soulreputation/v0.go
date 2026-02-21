package soulreputation

import (
	"math"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type Weights struct {
	Economic   float64 `json:"economic"`
	Social     float64 `json:"social"`
	Validation float64 `json:"validation"`
	Trust      float64 `json:"trust"`
}

func (w Weights) Normalized() Weights {
	sum := w.Economic + w.Social + w.Validation + w.Trust
	if sum <= 0 {
		return Weights{Economic: 1}
	}
	return Weights{
		Economic:   w.Economic / sum,
		Social:     w.Social / sum,
		Validation: w.Validation / sum,
		Trust:      w.Trust / sum,
	}
}

type SignalCounts struct {
	TipsReceived      int64 `json:"tips_received"`
	Interactions      int64 `json:"interactions"`
	ValidationsPassed int64 `json:"validations_passed"`
	Endorsements      int64 `json:"endorsements"`
	Flags             int64 `json:"flags"`
}

type SignalScores struct {
	Social     float64 `json:"social"`
	Validation float64 `json:"validation"`
	Trust      float64 `json:"trust"`
}

type V0Config struct {
	TipScale float64
	Weights  Weights
}

func tipScore(tipsReceived int64, tipScale float64) float64 {
	if tipsReceived <= 0 {
		return 0
	}
	if tipScale <= 0 {
		tipScale = 10
	}
	return clamp01(1 - math.Exp(-float64(tipsReceived)/tipScale))
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func ComputeV0(agentID string, blockRef uint64, now time.Time, cfg V0Config, signals SignalCounts, scores SignalScores) models.SoulAgentReputation {
	agentID = strings.ToLower(strings.TrimSpace(agentID))

	weights := cfg.Weights.Normalized()

	economic := tipScore(signals.TipsReceived, cfg.TipScale)
	social := clamp01(scores.Social)
	validation := clamp01(scores.Validation)
	trust := clamp01(scores.Trust)

	composite := clamp01(
		weights.Economic*economic +
			weights.Social*social +
			weights.Validation*validation +
			weights.Trust*trust,
	)

	const maxInt64 = int64(^uint64(0) >> 1)

	return models.SoulAgentReputation{
		AgentID: agentID,
		BlockRef: func() int64 {
			if blockRef > uint64(maxInt64) {
				return maxInt64
			}
			return int64(blockRef)
		}(),

		Composite:  composite,
		Economic:   economic,
		Social:     social,
		Validation: validation,
		Trust:      trust,

		TipsReceived:      signals.TipsReceived,
		Interactions:      signals.Interactions,
		ValidationsPassed: signals.ValidationsPassed,
		Endorsements:      signals.Endorsements,
		Flags:             signals.Flags,

		UpdatedAt: now.UTC(),
	}
}
