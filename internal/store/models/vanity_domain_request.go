package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	VanityDomainRequestStatusPending  = "pending"
	VanityDomainRequestStatusApproved = "approved"
	VanityDomainRequestStatusRejected = "rejected"
)

// VanityDomainRequest tracks operator approval for a verified vanity domain.
type VanityDomainRequest struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	Domain       string `theorydb:"attr:domain" json:"domain"`
	DomainRaw    string `theorydb:"attr:domainRaw" json:"domain_raw,omitempty"`
	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug"`
	RequestedBy  string `theorydb:"attr:requestedBy" json:"requested_by,omitempty"`

	Status string `theorydb:"attr:status" json:"status"` // pending|approved|rejected

	VerifiedAt  time.Time `theorydb:"attr:verifiedAt" json:"verified_at,omitempty"`
	RequestedAt time.Time `theorydb:"attr:requestedAt" json:"requested_at,omitempty"`

	ReviewedBy string    `theorydb:"attr:reviewedBy" json:"reviewed_by,omitempty"`
	ReviewedAt time.Time `theorydb:"attr:reviewedAt" json:"reviewed_at,omitempty"`
	Note       string    `theorydb:"attr:note" json:"note,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

// TableName returns the database table name for VanityDomainRequest.
func (VanityDomainRequest) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating VanityDomainRequest.
func (r *VanityDomainRequest) BeforeCreate() error {
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	if strings.TrimSpace(r.Status) == "" {
		r.Status = VanityDomainRequestStatusPending
	}
	if r.RequestedAt.IsZero() {
		r.RequestedAt = r.CreatedAt
	}
	return r.UpdateKeys()
}

// BeforeUpdate updates timestamps and keys before updating VanityDomainRequest.
func (r *VanityDomainRequest) BeforeUpdate() error {
	r.UpdatedAt = time.Now().UTC()
	return r.UpdateKeys()
}

// UpdateKeys updates the database keys for VanityDomainRequest.
func (r *VanityDomainRequest) UpdateKeys() error {
	r.Domain = strings.ToLower(strings.TrimSpace(r.Domain))
	r.DomainRaw = strings.TrimSpace(r.DomainRaw)
	r.InstanceSlug = strings.TrimSpace(r.InstanceSlug)
	r.RequestedBy = strings.TrimSpace(r.RequestedBy)
	r.Status = strings.ToLower(strings.TrimSpace(r.Status))
	r.ReviewedBy = strings.TrimSpace(r.ReviewedBy)
	r.Note = strings.TrimSpace(r.Note)

	r.PK = fmt.Sprintf("VANITY_DOMAIN_REQUEST#%s", r.Domain)
	r.SK = SKMetadata

	r.GSI1PK = fmt.Sprintf("VANITY_DOMAIN_REQUESTS#%s", r.Status)
	r.GSI1SK = fmt.Sprintf("%s#%s", r.RequestedAt.UTC().Format(time.RFC3339Nano), r.Domain)

	return nil
}

// GetPK returns the partition key for VanityDomainRequest.
func (r *VanityDomainRequest) GetPK() string { return r.PK }

// GetSK returns the sort key for VanityDomainRequest.
func (r *VanityDomainRequest) GetSK() string { return r.SK }
