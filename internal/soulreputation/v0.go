package soulreputation

import (
	"math"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type Weights struct {
	Economic      float64 `json:"economic"`
	Social        float64 `json:"social"`
	Validation    float64 `json:"validation"`
	Trust         float64 `json:"trust"`
	Integrity     float64 `json:"integrity"`
	Communication float64 `json:"communication"`
}

func (w Weights) Normalized() Weights {
	sum := w.Economic + w.Social + w.Validation + w.Trust + w.Integrity + w.Communication
	if sum <= 0 {
		return Weights{Economic: 1}
	}
	return Weights{
		Economic:      w.Economic / sum,
		Social:        w.Social / sum,
		Validation:    w.Validation / sum,
		Trust:         w.Trust / sum,
		Integrity:     w.Integrity / sum,
		Communication: w.Communication / sum,
	}
}

type SignalCounts struct {
	TipsReceived         int64 `json:"tips_received"`
	Interactions         int64 `json:"interactions"`
	ValidationsPassed    int64 `json:"validations_passed"`
	Endorsements         int64 `json:"endorsements"`
	Flags                int64 `json:"flags"`
	DelegationsCompleted int64 `json:"delegations_completed"`
	BoundaryViolations   int64 `json:"boundary_violations"`
	FailureRecoveries    int64 `json:"failure_recoveries"`

	EmailsSent                      int64   `json:"emails_sent"`
	EmailsReceived                  int64   `json:"emails_received"`
	SMSSent                         int64   `json:"sms_sent"`
	SMSReceived                     int64   `json:"sms_received"`
	CallsMade                       int64   `json:"calls_made"`
	CallsReceived                   int64   `json:"calls_received"`
	CommunicationBoundaryViolations int64   `json:"communication_boundary_violations"`
	SpamReports                     int64   `json:"spam_reports"`
	ResponseRate                    float64 `json:"response_rate"`
	AvgResponseTimeMinutes          float64 `json:"avg_response_time_minutes"`
}

type SignalScores struct {
	Social        float64 `json:"social"`
	Validation    float64 `json:"validation"`
	Trust         float64 `json:"trust"`
	Integrity     float64 `json:"integrity"`
	Communication float64 `json:"communication"`
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
	integrity := clamp01(scores.Integrity)
	communication := clamp01(scores.Communication)

	composite := clamp01(
		weights.Economic*economic +
			weights.Social*social +
			weights.Validation*validation +
			weights.Trust*trust +
			weights.Integrity*integrity +
			weights.Communication*communication,
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

		Composite:     composite,
		Economic:      economic,
		Social:        social,
		Validation:    validation,
		Trust:         trust,
		Integrity:     integrity,
		Communication: communication,

		TipsReceived:         signals.TipsReceived,
		Interactions:         signals.Interactions,
		ValidationsPassed:    signals.ValidationsPassed,
		Endorsements:         signals.Endorsements,
		Flags:                signals.Flags,
		DelegationsCompleted: signals.DelegationsCompleted,
		BoundaryViolations:   signals.BoundaryViolations,
		FailureRecoveries:    signals.FailureRecoveries,

		EmailsSent:                      signals.EmailsSent,
		EmailsReceived:                  signals.EmailsReceived,
		SMSSent:                         signals.SMSSent,
		SMSReceived:                     signals.SMSReceived,
		CallsMade:                       signals.CallsMade,
		CallsReceived:                   signals.CallsReceived,
		CommunicationBoundaryViolations: signals.CommunicationBoundaryViolations,
		SpamReports:                     signals.SpamReports,
		ResponseRate:                    signals.ResponseRate,
		AvgResponseTimeMinutes:          signals.AvgResponseTimeMinutes,

		UpdatedAt: now.UTC(),
	}
}
