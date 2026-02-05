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

	Slug                   string    `theorydb:"attr:slug" json:"slug"`
	Owner                  string    `theorydb:"attr:owner" json:"owner,omitempty"`
	Status                 string    `theorydb:"attr:status" json:"status"`
	ProvisionStatus        string    `theorydb:"attr:provisionStatus" json:"provision_status,omitempty"` // queued|running|ok|error
	ProvisionJobID         string    `theorydb:"attr:provisionJobId" json:"provision_job_id,omitempty"`
	HostedAccountID        string    `theorydb:"attr:hostedAccountId" json:"hosted_account_id,omitempty"`
	HostedRegion           string    `theorydb:"attr:hostedRegion" json:"hosted_region,omitempty"`
	HostedBaseDomain       string    `theorydb:"attr:hostedBaseDomain" json:"hosted_base_domain,omitempty"`
	HostedZoneID           string    `theorydb:"attr:hostedZoneId" json:"hosted_zone_id,omitempty"`
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
	if i.CreatedAt.IsZero() {
		i.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(i.Status) == "" {
		i.Status = InstanceStatusActive
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
	return nil
}

// UpdateKeys updates the database keys for Instance.
func (i *Instance) UpdateKeys() error {
	slug := strings.TrimSpace(i.Slug)
	i.PK = fmt.Sprintf("INSTANCE#%s", slug)
	i.SK = SKMetadata
	return nil
}

// GetPK returns the partition key for Instance.
func (i *Instance) GetPK() string { return i.PK }

// GetSK returns the sort key for Instance.
func (i *Instance) GetSK() string { return i.SK }
