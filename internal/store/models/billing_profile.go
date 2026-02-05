package models

import (
	"fmt"
	"strings"
	"time"
)

// BillingProvider* constants describe supported billing providers.
const (
	BillingProviderNone   = "none"
	BillingProviderStripe = "stripe"
)

// BillingProfile stores the billing configuration for a user.
type BillingProfile struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Username string `theorydb:"attr:username" json:"username"`
	Provider string `theorydb:"attr:provider" json:"provider"`

	StripeCustomerID       string `theorydb:"attr:stripeCustomerId" json:"stripe_customer_id,omitempty"`
	DefaultPaymentMethodID string `theorydb:"attr:defaultPaymentMethodId" json:"default_payment_method_id,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

// TableName returns the database table name for BillingProfile.
func (BillingProfile) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating BillingProfile.
func (p *BillingProfile) BeforeCreate() error {
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	if strings.TrimSpace(p.Provider) == "" {
		p.Provider = BillingProviderNone
	}
	return p.UpdateKeys()
}

// BeforeUpdate updates timestamps and keys before updating BillingProfile.
func (p *BillingProfile) BeforeUpdate() error {
	p.UpdatedAt = time.Now().UTC()
	return p.UpdateKeys()
}

// UpdateKeys updates the database keys for BillingProfile.
func (p *BillingProfile) UpdateKeys() error {
	p.Username = strings.TrimSpace(p.Username)
	p.Provider = strings.ToLower(strings.TrimSpace(p.Provider))
	p.StripeCustomerID = strings.TrimSpace(p.StripeCustomerID)
	p.DefaultPaymentMethodID = strings.TrimSpace(p.DefaultPaymentMethodID)

	p.PK = fmt.Sprintf(KeyPatternUser, p.Username)
	p.SK = "BILLING"
	return nil
}

// GetPK returns the partition key for BillingProfile.
func (p *BillingProfile) GetPK() string { return p.PK }

// GetSK returns the sort key for BillingProfile.
func (p *BillingProfile) GetSK() string { return p.SK }
