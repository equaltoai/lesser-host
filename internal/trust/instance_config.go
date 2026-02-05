package trust

import (
	"context"
	"strings"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type instanceTrustConfig struct {
	HostedPreviewsEnabled bool
	LinkSafetyEnabled     bool
	RendersEnabled        bool
	RenderPolicy          string // always|suspicious
	OveragePolicy         string // block|allow

	ModerationEnabled     bool
	ModerationTrigger     string // on_reports|always|links_media_only|virality
	ModerationViralityMin int64

	AIEnabled              bool
	AIModelSet             string
	AIBatchingMode         string
	AIBatchMaxItems        int64
	AIBatchMaxTotalBytes   int64
	AIPricingMultiplierBps int64
	AIMaxInflightJobs      int64
}

func defaultInstanceTrustConfig() instanceTrustConfig {
	return instanceTrustConfig{
		HostedPreviewsEnabled: true,
		LinkSafetyEnabled:     true,
		RendersEnabled:        true,
		RenderPolicy:          renderPolicySuspicious,
		OveragePolicy:         overagePolicyBlock,

		ModerationEnabled:     false,
		ModerationTrigger:     moderationTriggerOnReports,
		ModerationViralityMin: 0,

		AIEnabled:              false,
		AIModelSet:             "openai:gpt-5-mini-2025-08-07",
		AIBatchingMode:         aiBatchingModeNone,
		AIBatchMaxItems:        8,
		AIBatchMaxTotalBytes:   64 * 1024,
		AIPricingMultiplierBps: 10000,
		AIMaxInflightJobs:      200,
	}
}

func (s *Server) loadInstanceTrustConfig(ctx context.Context, instanceSlug string) instanceTrustConfig {
	cfg := defaultInstanceTrustConfig()
	if s == nil || s.store == nil || s.store.DB == nil {
		return cfg
	}

	instanceSlug = strings.TrimSpace(instanceSlug)
	if instanceSlug == "" {
		return cfg
	}

	var inst models.Instance
	err := s.store.DB.WithContext(ctx).
		Model(&models.Instance{}).
		Where("PK", "=", "INSTANCE#"+instanceSlug).
		Where("SK", "=", models.SKMetadata).
		First(&inst)
	if err != nil {
		// Default config for missing instance record.
		if theoryErrors.IsNotFound(err) {
			return cfg
		}
		return cfg
	}

	if inst.HostedPreviewsEnabled != nil {
		cfg.HostedPreviewsEnabled = *inst.HostedPreviewsEnabled
	}
	if inst.LinkSafetyEnabled != nil {
		cfg.LinkSafetyEnabled = *inst.LinkSafetyEnabled
	}
	if inst.RendersEnabled != nil {
		cfg.RendersEnabled = *inst.RendersEnabled
	}

	rp := strings.ToLower(strings.TrimSpace(inst.RenderPolicy))
	if rp == renderPolicyAlways || rp == renderPolicySuspicious {
		cfg.RenderPolicy = rp
	}

	op := strings.ToLower(strings.TrimSpace(inst.OveragePolicy))
	if op == overagePolicyAllow || op == overagePolicyBlock {
		cfg.OveragePolicy = op
	}

	if inst.ModerationEnabled != nil {
		cfg.ModerationEnabled = *inst.ModerationEnabled
	}
	mt := strings.ToLower(strings.TrimSpace(inst.ModerationTrigger))
	switch mt {
	case moderationTriggerOnReports, moderationTriggerAlways, moderationTriggerLinksMediaOnly, moderationTriggerVirality:
		cfg.ModerationTrigger = mt
	}
	if inst.ModerationViralityMin >= 0 {
		cfg.ModerationViralityMin = inst.ModerationViralityMin
	}

	aiCfg := ai.EffectiveInstanceConfig(&inst)
	cfg.AIEnabled = aiCfg.Enabled
	cfg.AIModelSet = aiCfg.ModelSet
	cfg.AIBatchingMode = aiCfg.BatchingMode
	cfg.AIBatchMaxItems = aiCfg.BatchMaxItems
	cfg.AIBatchMaxTotalBytes = aiCfg.BatchMaxTotalBytes
	cfg.AIPricingMultiplierBps = aiCfg.PricingMultiplierBps
	cfg.AIMaxInflightJobs = aiCfg.MaxInflightJobs

	return cfg
}
