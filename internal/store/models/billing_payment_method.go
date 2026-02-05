package models

import (
	"fmt"
	"strings"
	"time"
)

// BillingPaymentMethod stores a payment method attached to a BillingProfile.
type BillingPaymentMethod struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Username string `theorydb:"attr:username" json:"username"`
	Provider string `theorydb:"attr:provider" json:"provider"`
	ID       string `theorydb:"attr:id" json:"id"`

	Type     string `theorydb:"attr:type" json:"type,omitempty"` // card, bank_account, ...
	Brand    string `theorydb:"attr:brand" json:"brand,omitempty"`
	Last4    string `theorydb:"attr:last4" json:"last4,omitempty"`
	ExpMonth int64  `theorydb:"attr:expMonth" json:"exp_month,omitempty"`
	ExpYear  int64  `theorydb:"attr:expYear" json:"exp_year,omitempty"`

	Status string `theorydb:"attr:status" json:"status"` // active|detached

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

// BillingPaymentMethodStatusActive and BillingPaymentMethodStatusDetached define lifecycle states for stored payment methods.
const (
	BillingPaymentMethodStatusActive   = "active"
	BillingPaymentMethodStatusDetached = "detached"
)

// TableName returns the database table name for BillingPaymentMethod.
func (BillingPaymentMethod) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating BillingPaymentMethod.
func (m *BillingPaymentMethod) BeforeCreate() error {
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = m.CreatedAt
	}
	if strings.TrimSpace(m.Status) == "" {
		m.Status = BillingPaymentMethodStatusActive
	}
	return m.UpdateKeys()
}

// BeforeUpdate updates timestamps and keys before updating BillingPaymentMethod.
func (m *BillingPaymentMethod) BeforeUpdate() error {
	m.UpdatedAt = time.Now().UTC()
	return m.UpdateKeys()
}

// UpdateKeys updates the database keys for BillingPaymentMethod.
func (m *BillingPaymentMethod) UpdateKeys() error {
	m.Username = strings.TrimSpace(m.Username)
	m.Provider = strings.ToLower(strings.TrimSpace(m.Provider))
	m.ID = strings.TrimSpace(m.ID)
	m.Type = strings.TrimSpace(m.Type)
	m.Brand = strings.TrimSpace(m.Brand)
	m.Last4 = strings.TrimSpace(m.Last4)
	m.Status = strings.ToLower(strings.TrimSpace(m.Status))

	m.PK = fmt.Sprintf(KeyPatternUser, m.Username)
	m.SK = fmt.Sprintf("PAYMENT_METHOD#%s#%s", m.Provider, m.ID)
	return nil
}

// GetPK returns the partition key for BillingPaymentMethod.
func (m *BillingPaymentMethod) GetPK() string { return m.PK }

// GetSK returns the sort key for BillingPaymentMethod.
func (m *BillingPaymentMethod) GetSK() string { return m.SK }
