package models

import (
	"fmt"
	"strings"
	"time"
)

// RenderRetentionClass* constants define the retention class for rendered artifacts.
const (
	RenderRetentionClassBenign   = "benign"
	RenderRetentionClassEvidence = "evidence"

	renderExpiresGSI1PK = "RENDER_EXPIRES"
)

// RenderArtifact stores the output of a link render (snapshot, thumbnail, and derived metadata).
type RenderArtifact struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	ID            string `theorydb:"attr:id" json:"id"`
	PolicyVersion string `theorydb:"attr:policyVersion" json:"policy_version"`

	NormalizedURL string   `theorydb:"attr:normalizedUrl" json:"normalized_url"`
	ResolvedURL   string   `theorydb:"attr:resolvedUrl" json:"resolved_url,omitempty"`
	RedirectChain []string `theorydb:"attr:redirectChain" json:"redirect_chain,omitempty"`

	ThumbnailObjectKey   string `theorydb:"attr:thumbnailObjectKey" json:"-"`
	ThumbnailContentType string `theorydb:"attr:thumbnailContentType" json:"thumbnail_content_type,omitempty"`

	SnapshotObjectKey   string `theorydb:"attr:snapshotObjectKey" json:"-"`
	SnapshotContentType string `theorydb:"attr:snapshotContentType" json:"snapshot_content_type,omitempty"`

	TextPreview string `theorydb:"attr:textPreview" json:"text_preview,omitempty"`

	SummaryPolicyVersion string    `theorydb:"attr:summaryPolicyVersion" json:"summary_policy_version,omitempty"`
	Summary              string    `theorydb:"attr:summary" json:"summary,omitempty"`
	SummarizedAt         time.Time `theorydb:"attr:summarizedAt" json:"summarized_at,omitempty"`

	RetentionClass string `theorydb:"attr:retentionClass" json:"retention_class"`

	ErrorCode    string `theorydb:"attr:errorCode" json:"error_code,omitempty"`
	ErrorMessage string `theorydb:"attr:errorMessage" json:"error_message,omitempty"`

	RequestedBy string `theorydb:"attr:requestedBy" json:"requested_by,omitempty"`
	RequestID   string `theorydb:"attr:requestId" json:"request_id,omitempty"`

	CreatedAt  time.Time `theorydb:"attr:createdAt" json:"created_at"`
	RenderedAt time.Time `theorydb:"attr:renderedAt" json:"rendered_at,omitempty"`
	ExpiresAt  time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
}

// TableName returns the database table name for RenderArtifact.
func (RenderArtifact) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating RenderArtifact.
func (r *RenderArtifact) BeforeCreate() error {
	if err := r.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.ExpiresAt.IsZero() {
		r.ExpiresAt = now.Add(30 * 24 * time.Hour)
	}
	r.TTL = ttlForExpiresAt(r.ExpiresAt)
	r.GSI1PK = renderExpiresGSI1PK
	r.GSI1SK = fmt.Sprintf("%s#%s", r.ExpiresAt.UTC().Format(time.RFC3339Nano), r.ID)
	return nil
}

// BeforeUpdate updates secondary index keys and TTL before updating RenderArtifact.
func (r *RenderArtifact) BeforeUpdate() error {
	r.TTL = ttlForExpiresAt(r.ExpiresAt)
	r.GSI1PK = renderExpiresGSI1PK
	r.GSI1SK = fmt.Sprintf("%s#%s", r.ExpiresAt.UTC().Format(time.RFC3339Nano), r.ID)
	return nil
}

// UpdateKeys updates the database keys for RenderArtifact.
func (r *RenderArtifact) UpdateKeys() error {
	r.ID = strings.TrimSpace(r.ID)
	r.PK = fmt.Sprintf("RENDER#%s", r.ID)
	r.SK = "ARTIFACT"
	r.TTL = ttlForExpiresAt(r.ExpiresAt)
	r.GSI1PK = renderExpiresGSI1PK
	r.GSI1SK = fmt.Sprintf("%s#%s", r.ExpiresAt.UTC().Format(time.RFC3339Nano), r.ID)
	return nil
}

// GetPK returns the partition key for RenderArtifact.
func (r *RenderArtifact) GetPK() string { return r.PK }

// GetSK returns the sort key for RenderArtifact.
func (r *RenderArtifact) GetSK() string { return r.SK }

func ttlForExpiresAt(expiresAt time.Time) int64 {
	if expiresAt.IsZero() {
		return 0
	}
	// Keep the record around for a buffer window so the sweeper can reliably delete S3 objects.
	return expiresAt.Add(7 * 24 * time.Hour).Unix()
}
