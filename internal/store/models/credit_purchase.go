package models

import (
	"fmt"
	"strings"
	"time"
)

// CreditPurchaseStatus* constants define payment lifecycle states.
const (
	CreditPurchaseStatusPending = "pending"
	CreditPurchaseStatusPaid    = "paid"
	CreditPurchaseStatusExpired = "expired"
	CreditPurchaseStatusFailed  = "failed"
)

// CreditPurchase records a user purchase of credits for an instance/month budget.
type CreditPurchase struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	ID string `theorydb:"attr:id" json:"id"`

	Username     string `theorydb:"attr:username" json:"username"`
	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug"`
	Month        string `theorydb:"attr:month" json:"month"` // YYYY-MM

	Credits     int64  `theorydb:"attr:credits" json:"credits"`
	AmountCents int64  `theorydb:"attr:amountCents" json:"amount_cents"`
	Currency    string `theorydb:"attr:currency" json:"currency"`

	Provider                  string `theorydb:"attr:provider" json:"provider"`
	ProviderCheckoutSessionID string `theorydb:"attr:providerCheckoutSessionId" json:"provider_checkout_session_id,omitempty"`
	ProviderPaymentIntentID   string `theorydb:"attr:providerPaymentIntentId" json:"provider_payment_intent_id,omitempty"`
	ProviderCustomerID        string `theorydb:"attr:providerCustomerId" json:"provider_customer_id,omitempty"`
	ReceiptURL                string `theorydb:"attr:receiptUrl" json:"receipt_url,omitempty"`

	Status string `theorydb:"attr:status" json:"status"`

	RequestID string `theorydb:"attr:requestId" json:"request_id,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	PaidAt    time.Time `theorydb:"attr:paidAt" json:"paid_at,omitempty"`
}

// TableName returns the database table name for CreditPurchase.
func (CreditPurchase) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating CreditPurchase.
func (p *CreditPurchase) BeforeCreate() error {
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	if strings.TrimSpace(p.Status) == "" {
		p.Status = CreditPurchaseStatusPending
	}
	if strings.TrimSpace(p.Currency) == "" {
		p.Currency = "usd"
	}
	return p.UpdateKeys()
}

// BeforeUpdate updates timestamps before updating CreditPurchase.
func (p *CreditPurchase) BeforeUpdate() error {
	p.UpdatedAt = time.Now().UTC()
	return p.UpdateKeys()
}

// UpdateKeys updates the database keys for CreditPurchase.
func (p *CreditPurchase) UpdateKeys() error {
	p.ID = strings.TrimSpace(p.ID)
	p.Username = strings.TrimSpace(p.Username)
	p.InstanceSlug = strings.TrimSpace(p.InstanceSlug)
	p.Month = strings.TrimSpace(p.Month)
	p.Currency = strings.ToLower(strings.TrimSpace(p.Currency))
	p.Provider = strings.ToLower(strings.TrimSpace(p.Provider))
	p.ProviderCheckoutSessionID = strings.TrimSpace(p.ProviderCheckoutSessionID)
	p.ProviderPaymentIntentID = strings.TrimSpace(p.ProviderPaymentIntentID)
	p.ProviderCustomerID = strings.TrimSpace(p.ProviderCustomerID)
	p.ReceiptURL = strings.TrimSpace(p.ReceiptURL)
	p.Status = strings.ToLower(strings.TrimSpace(p.Status))
	p.RequestID = strings.TrimSpace(p.RequestID)

	p.PK = fmt.Sprintf("CREDIT_PURCHASE#%s", p.ID)
	p.SK = SKMetadata

	p.GSI1PK = fmt.Sprintf("USER_PURCHASES#%s", p.Username)
	p.GSI1SK = fmt.Sprintf("PURCHASE#%s#%s", p.CreatedAt.UTC().Format(time.RFC3339Nano), p.ID)

	return nil
}

// GetPK returns the partition key for CreditPurchase.
func (p *CreditPurchase) GetPK() string { return p.PK }

// GetSK returns the sort key for CreditPurchase.
func (p *CreditPurchase) GetSK() string { return p.SK }
