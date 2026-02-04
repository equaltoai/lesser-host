package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	DomainTypePrimary = "primary"
	DomainTypeVanity  = "vanity"
)

const (
	DomainStatusPending  = "pending"
	DomainStatusVerified = "verified"
	DomainStatusRejected = "rejected"
)

type Domain struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	Domain       string `theorydb:"attr:domain" json:"domain"`
	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug"`

	Type   string `theorydb:"attr:type" json:"type"`     // primary|vanity
	Status string `theorydb:"attr:status" json:"status"` // pending|verified|rejected

	VerificationMethod string `theorydb:"attr:verificationMethod" json:"verification_method,omitempty"` // dns_txt|manual
	VerificationToken  string `theorydb:"attr:verificationToken" json:"verification_token,omitempty"`

	CreatedAt  time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt  time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	VerifiedAt time.Time `theorydb:"attr:verifiedAt" json:"verified_at,omitempty"`
}

func (Domain) TableName() string { return MainTableName() }

func (d *Domain) BeforeCreate() error {
	if err := d.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	if strings.TrimSpace(d.Type) == "" {
		d.Type = DomainTypeVanity
	}
	if strings.TrimSpace(d.Status) == "" {
		d.Status = DomainStatusPending
	}
	return nil
}

func (d *Domain) BeforeUpdate() error {
	d.UpdatedAt = time.Now().UTC()
	return d.UpdateKeys()
}

func (d *Domain) UpdateKeys() error {
	domain := strings.ToLower(strings.TrimSpace(d.Domain))
	d.InstanceSlug = strings.TrimSpace(d.InstanceSlug)
	d.Type = strings.ToLower(strings.TrimSpace(d.Type))

	d.PK = fmt.Sprintf("DOMAIN#%s", domain)
	d.SK = SKMetadata

	d.GSI1PK = fmt.Sprintf("INSTANCE_DOMAINS#%s", strings.TrimSpace(d.InstanceSlug))
	d.GSI1SK = fmt.Sprintf("%s#%s", d.Type, domain)

	return nil
}

func (d *Domain) GetPK() string { return d.PK }
func (d *Domain) GetSK() string { return d.SK }
