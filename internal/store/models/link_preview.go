package models

import (
	"fmt"
	"strings"
	"time"
)

// LinkPreview stores derived preview metadata for a normalized URL.
type LinkPreview struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	ID            string `theorydb:"attr:id" json:"id"`
	PolicyVersion string `theorydb:"attr:policyVersion" json:"policy_version"`

	NormalizedURL string   `theorydb:"attr:normalizedUrl" json:"normalized_url"`
	ResolvedURL   string   `theorydb:"attr:resolvedUrl" json:"resolved_url,omitempty"`
	RedirectChain []string `theorydb:"attr:redirectChain" json:"redirect_chain,omitempty"`

	Title       string `theorydb:"attr:title" json:"title,omitempty"`
	Description string `theorydb:"attr:description" json:"description,omitempty"`

	ImageID        string `theorydb:"attr:imageId" json:"image_id,omitempty"`
	ImageObjectKey string `theorydb:"attr:imageObjectKey" json:"-"`

	ErrorCode    string `theorydb:"attr:errorCode" json:"error_code,omitempty"`
	ErrorMessage string `theorydb:"attr:errorMessage" json:"error_message,omitempty"`

	FetchedAt  time.Time `theorydb:"attr:fetchedAt" json:"fetched_at"`
	ExpiresAt  time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
	StoredAt   time.Time `theorydb:"attr:storedAt" json:"stored_at"`
	StoredBy   string    `theorydb:"attr:storedBy" json:"stored_by,omitempty"`
	RequestID  string    `theorydb:"attr:requestId" json:"request_id,omitempty"`
	SourceType string    `theorydb:"attr:sourceType" json:"source_type,omitempty"`
}

// TableName returns the database table name for LinkPreview.
func (LinkPreview) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating LinkPreview.
func (p *LinkPreview) BeforeCreate() error {
	if err := p.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if p.StoredAt.IsZero() {
		p.StoredAt = now
	}
	if p.FetchedAt.IsZero() {
		p.FetchedAt = p.StoredAt
	}
	if p.ExpiresAt.IsZero() {
		p.ExpiresAt = now.Add(24 * time.Hour)
	}
	p.TTL = p.ExpiresAt.Unix()
	return nil
}

// UpdateKeys updates the database keys for LinkPreview.
func (p *LinkPreview) UpdateKeys() error {
	p.ID = strings.TrimSpace(p.ID)
	p.PK = fmt.Sprintf("LINK_PREVIEW#%s", p.ID)
	p.SK = "PREVIEW"
	p.TTL = p.ExpiresAt.Unix()
	return nil
}

// GetPK returns the partition key for LinkPreview.
func (p *LinkPreview) GetPK() string { return p.PK }

// GetSK returns the sort key for LinkPreview.
func (p *LinkPreview) GetSK() string { return p.SK }
