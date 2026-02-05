package ai

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// InstanceConfig is the effective AI configuration for an instance.
type InstanceConfig struct {
	Enabled bool

	ModelSet string

	BatchingMode       string
	BatchMaxItems      int64
	BatchMaxTotalBytes int64

	PricingMultiplierBps int64
}

// DefaultInstanceConfig returns the default AI instance configuration.
func DefaultInstanceConfig() InstanceConfig {
	return InstanceConfig{
		Enabled:              false,
		ModelSet:             "openai:gpt-5-mini-2025-08-07",
		BatchingMode:         "none",
		BatchMaxItems:        8,
		BatchMaxTotalBytes:   64 * 1024,
		PricingMultiplierBps: 10000,
	}
}

// EffectiveInstanceConfig resolves an InstanceConfig from an instance record.
func EffectiveInstanceConfig(inst *models.Instance) InstanceConfig {
	cfg := DefaultInstanceConfig()
	if inst == nil {
		return cfg
	}

	if inst.AIEnabled != nil {
		cfg.Enabled = *inst.AIEnabled
	}
	if strings.TrimSpace(inst.AIModelSet) != "" {
		cfg.ModelSet = strings.TrimSpace(inst.AIModelSet)
	}

	mode := strings.ToLower(strings.TrimSpace(inst.AIBatchingMode))
	switch mode {
	case "none", "in_request", "worker", "hybrid":
		cfg.BatchingMode = mode
	}

	if inst.AIBatchMaxItems > 0 {
		cfg.BatchMaxItems = inst.AIBatchMaxItems
	}
	if inst.AIBatchMaxTotalBytes > 0 {
		cfg.BatchMaxTotalBytes = inst.AIBatchMaxTotalBytes
	}

	if inst.AIPricingMultiplierBps != nil && *inst.AIPricingMultiplierBps > 0 {
		cfg.PricingMultiplierBps = *inst.AIPricingMultiplierBps
	}

	return cfg
}
