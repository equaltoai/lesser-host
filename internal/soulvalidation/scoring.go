package soulvalidation

import (
	"math"
	"sort"
	"strings"
	"time"
)

type Config struct {
	Epoch     time.Duration
	DecayRate float64 // per epoch, in [0,1]
}

type Event struct {
	EvaluatedAt time.Time
	Result      string
	Delta       float64
}

func IsValidChallengeType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "capability_probe", "identity_verify", "content_quality", "peer_review":
		return true
	default:
		return false
	}
}

func ChallengeWeight(challengeType string) float64 {
	switch strings.ToLower(strings.TrimSpace(challengeType)) {
	case "identity_verify":
		return 0.05
	case "capability_probe":
		return 0.10
	case "peer_review":
		return 0.15
	case "content_quality":
		return 0.20
	default:
		return 0.10
	}
}

func ScoreDelta(challengeType string, result string) float64 {
	w := ChallengeWeight(challengeType)
	switch strings.ToLower(strings.TrimSpace(result)) {
	case "pass":
		return w
	case "timeout":
		return -w * 0.25
	default:
		return -w * 0.50
	}
}

func ComputeProgressiveScore(events []Event, now time.Time, cfg Config) (float64, int64) {
	now = now.UTC()

	epoch := cfg.Epoch
	if epoch <= 0 {
		epoch = 7 * 24 * time.Hour
	}
	decayRate := cfg.DecayRate
	if decayRate < 0 {
		decayRate = 0
	}
	if decayRate > 1 {
		decayRate = 1
	}

	filtered := make([]Event, 0, len(events))
	passed := int64(0)
	for _, ev := range events {
		if ev.EvaluatedAt.IsZero() {
			continue
		}
		at := ev.EvaluatedAt.UTC()
		if at.After(now) {
			continue
		}
		ev.EvaluatedAt = at
		ev.Result = strings.ToLower(strings.TrimSpace(ev.Result))
		if ev.Result == "pass" {
			passed++
		}
		filtered = append(filtered, ev)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].EvaluatedAt.Equal(filtered[j].EvaluatedAt) {
			return filtered[i].Delta < filtered[j].Delta
		}
		return filtered[i].EvaluatedAt.Before(filtered[j].EvaluatedAt)
	})

	score := 0.0
	last := time.Time{}

	for _, ev := range filtered {
		if !last.IsZero() {
			score = applyDecay(score, last, ev.EvaluatedAt, epoch, decayRate)
		}
		score = clamp01(score + ev.Delta)
		last = ev.EvaluatedAt
	}

	if !last.IsZero() {
		score = applyDecay(score, last, now, epoch, decayRate)
	}

	return clamp01(score), passed
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

func applyDecay(score float64, from time.Time, to time.Time, epoch time.Duration, decayRate float64) float64 {
	if score <= 0 || epoch <= 0 || decayRate <= 0 {
		return score
	}

	from = from.UTC()
	to = to.UTC()
	if to.Before(from) {
		return score
	}

	epochs := int64(to.Sub(from) / epoch)
	if epochs <= 0 {
		return score
	}

	mult := math.Pow(1-decayRate, float64(epochs))
	return score * mult
}
