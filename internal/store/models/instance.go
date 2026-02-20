package models

import (
	"fmt"
	"strings"
	"time"
)

// InstanceStatus* constants define whether an instance is active.
const (
	InstanceStatusActive   = "active"
	InstanceStatusDisabled = "disabled"
)

// Instance represents a tenant instance and its configuration.
type Instance struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	Slug                           string    `theorydb:"attr:slug" json:"slug"`
	Owner                          string    `theorydb:"attr:owner" json:"owner,omitempty"`
	Status                         string    `theorydb:"attr:status" json:"status"`
	ProvisionStatus                string    `theorydb:"attr:provisionStatus" json:"provision_status,omitempty"` // queued|running|ok|error
	ProvisionJobID                 string    `theorydb:"attr:provisionJobId" json:"provision_job_id,omitempty"`
	UpdateStatus                   string    `theorydb:"attr:updateStatus" json:"update_status,omitempty"` // queued|running|ok|error
	UpdateJobID                    string    `theorydb:"attr:updateJobId" json:"update_job_id,omitempty"`
	HostedAccountID                string    `theorydb:"attr:hostedAccountId" json:"hosted_account_id,omitempty"`
	HostedRegion                   string    `theorydb:"attr:hostedRegion" json:"hosted_region,omitempty"`
	HostedBaseDomain               string    `theorydb:"attr:hostedBaseDomain" json:"hosted_base_domain,omitempty"`
	HostedZoneID                   string    `theorydb:"attr:hostedZoneId" json:"hosted_zone_id,omitempty"`
	LesserVersion                  string    `theorydb:"attr:lesserVersion" json:"lesser_version,omitempty"`
	SoulEnabled                    *bool     `theorydb:"attr:soulEnabled" json:"soul_enabled,omitempty"`
	SoulVersion                    string    `theorydb:"attr:soulVersion" json:"soul_version,omitempty"`
	SoulProvisionedAt              time.Time `theorydb:"attr:soulProvisionedAt" json:"soul_provisioned_at,omitempty"`
	LesserHostBaseURL              string    `theorydb:"attr:lesserHostBaseUrl" json:"lesser_host_base_url,omitempty"`
	LesserHostAttestationsURL      string    `theorydb:"attr:lesserHostAttestationsUrl" json:"lesser_host_attestations_url,omitempty"`
	LesserHostInstanceKeySecretARN string    `theorydb:"attr:lesserHostInstanceKeySecretArn" json:"lesser_host_instance_key_secret_arn,omitempty"`
	TranslationEnabled             *bool     `theorydb:"attr:translationEnabled" json:"translation_enabled,omitempty"`

	// Tips config for hosted Lesser instances (applied via provisioning input).
	TipEnabled         *bool  `theorydb:"attr:tipEnabled" json:"tip_enabled,omitempty"`
	TipChainID         int64  `theorydb:"attr:tipChainId" json:"tip_chain_id,omitempty"`
	TipContractAddress string `theorydb:"attr:tipContractAddress" json:"tip_contract_address,omitempty"`

	// AI config for hosted Lesser instances (distinct from lesser.host's own trust/AI service configuration).
	LesserAIEnabled                 *bool `theorydb:"attr:lesserAiEnabled" json:"lesser_ai_enabled,omitempty"`
	LesserAIModerationEnabled       *bool `theorydb:"attr:lesserAiModerationEnabled" json:"lesser_ai_moderation_enabled,omitempty"`
	LesserAINsfwDetectionEnabled    *bool `theorydb:"attr:lesserAiNsfwDetectionEnabled" json:"lesser_ai_nsfw_detection_enabled,omitempty"`
	LesserAISpamDetectionEnabled    *bool `theorydb:"attr:lesserAiSpamDetectionEnabled" json:"lesser_ai_spam_detection_enabled,omitempty"`
	LesserAIPiiDetectionEnabled     *bool `theorydb:"attr:lesserAiPiiDetectionEnabled" json:"lesser_ai_pii_detection_enabled,omitempty"`
	LesserAIContentDetectionEnabled *bool `theorydb:"attr:lesserAiContentDetectionEnabled" json:"lesser_ai_content_detection_enabled,omitempty"`

	HostedPreviewsEnabled  *bool     `theorydb:"attr:hostedPreviewsEnabled" json:"hosted_previews_enabled,omitempty"`
	LinkSafetyEnabled      *bool     `theorydb:"attr:linkSafetyEnabled" json:"link_safety_enabled,omitempty"`
	RendersEnabled         *bool     `theorydb:"attr:rendersEnabled" json:"renders_enabled,omitempty"`
	RenderPolicy           string    `theorydb:"attr:renderPolicy" json:"render_policy,omitempty"`   // always|suspicious
	OveragePolicy          string    `theorydb:"attr:overagePolicy" json:"overage_policy,omitempty"` // block|allow
	ModerationEnabled      *bool     `theorydb:"attr:moderationEnabled" json:"moderation_enabled,omitempty"`
	ModerationTrigger      string    `theorydb:"attr:moderationTrigger" json:"moderation_trigger,omitempty"` // on_reports|always|links_media_only|virality
	ModerationViralityMin  int64     `theorydb:"attr:moderationViralityMin" json:"moderation_virality_min,omitempty"`
	AIEnabled              *bool     `theorydb:"attr:aiEnabled" json:"ai_enabled,omitempty"`
	AIModelSet             string    `theorydb:"attr:aiModelSet" json:"ai_model_set,omitempty"`
	AIBatchingMode         string    `theorydb:"attr:aiBatchingMode" json:"ai_batching_mode,omitempty"` // none|in_request|worker|hybrid
	AIBatchMaxItems        int64     `theorydb:"attr:aiBatchMaxItems" json:"ai_batch_max_items,omitempty"`
	AIBatchMaxTotalBytes   int64     `theorydb:"attr:aiBatchMaxTotalBytes" json:"ai_batch_max_total_bytes,omitempty"`
	AIPricingMultiplierBps *int64    `theorydb:"attr:aiPricingMultiplierBps" json:"ai_pricing_multiplier_bps,omitempty"`
	AIMaxInflightJobs      *int64    `theorydb:"attr:aiMaxInflightJobs" json:"ai_max_inflight_jobs,omitempty"`
	CreatedAt              time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for Instance.
func (Instance) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating Instance.
func (i *Instance) BeforeCreate() error {
	if err := i.UpdateKeys(); err != nil {
		return err
	}
	i.ensureCoreDefaults()
	i.ensureTrustDefaults()
	i.ensureModerationDefaults()
	i.ensureAIDefaults()
	return nil
}

func (i *Instance) ensureCoreDefaults() {
	if i == nil {
		return
	}
	if i.CreatedAt.IsZero() {
		i.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(i.Status) == "" {
		i.Status = InstanceStatusActive
	}
}

func (i *Instance) ensureTrustDefaults() {
	if i == nil {
		return
	}
	if i.HostedPreviewsEnabled == nil {
		v := true
		i.HostedPreviewsEnabled = &v
	}
	if i.LinkSafetyEnabled == nil {
		v := true
		i.LinkSafetyEnabled = &v
	}
	if i.RendersEnabled == nil {
		v := true
		i.RendersEnabled = &v
	}
	if strings.TrimSpace(i.RenderPolicy) == "" {
		i.RenderPolicy = "suspicious"
	}
	if strings.TrimSpace(i.OveragePolicy) == "" {
		i.OveragePolicy = "block"
	}
}

func (i *Instance) ensureModerationDefaults() {
	if i == nil {
		return
	}
	if i.ModerationEnabled == nil {
		v := false
		i.ModerationEnabled = &v
	}
	if strings.TrimSpace(i.ModerationTrigger) == "" {
		i.ModerationTrigger = "on_reports"
	}
	if i.ModerationViralityMin < 0 {
		i.ModerationViralityMin = 0
	}
}

func (i *Instance) ensureAIDefaults() {
	if i == nil {
		return
	}
	if i.AIEnabled == nil {
		v := false
		i.AIEnabled = &v
	}
	if strings.TrimSpace(i.AIModelSet) == "" {
		i.AIModelSet = "openai:gpt-5-mini-2025-08-07"
	}
	if strings.TrimSpace(i.AIBatchingMode) == "" {
		i.AIBatchingMode = "none"
	}
	if i.AIBatchMaxItems <= 0 {
		i.AIBatchMaxItems = 8
	}
	if i.AIBatchMaxTotalBytes <= 0 {
		i.AIBatchMaxTotalBytes = 64 * 1024
	}
	if i.AIPricingMultiplierBps == nil {
		v := int64(10000)
		i.AIPricingMultiplierBps = &v
	}
	if i.AIMaxInflightJobs == nil {
		v := int64(200)
		i.AIMaxInflightJobs = &v
	}
}

// UpdateKeys updates the database keys for Instance.
func (i *Instance) UpdateKeys() error {
	slug := strings.TrimSpace(i.Slug)
	owner := strings.TrimSpace(i.Owner)
	i.PK = fmt.Sprintf("INSTANCE#%s", slug)
	i.SK = SKMetadata
	i.GSI1PK = fmt.Sprintf("OWNER#%s", owner)
	i.GSI1SK = fmt.Sprintf("INSTANCE#%s", slug)
	return nil
}

// GetPK returns the partition key for Instance.
func (i *Instance) GetPK() string { return i.PK }

// GetSK returns the sort key for Instance.
func (i *Instance) GetSK() string { return i.SK }
