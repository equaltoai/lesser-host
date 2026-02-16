package models

import (
	"fmt"
	"strings"
	"time"
)

// BillingType* constants describe how a request was billed.
const (
	BillingTypeIncluded = "included"
	BillingTypeOverage  = "overage"
	BillingTypeNone     = "none"
	BillingTypeMixed    = "mixed"
)

// UsageLedgerEntry records per-request credit usage and billing attribution.
type UsageLedgerEntry struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	ID           string `theorydb:"attr:id" json:"id"`
	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug"`
	Month        string `theorydb:"attr:month" json:"month"` // YYYY-MM

	Module string `theorydb:"attr:module" json:"module"`
	Target string `theorydb:"attr:target,omitempty" json:"target,omitempty"`

	Cached    bool   `theorydb:"attr:cached" json:"cached"`
	Reason    string `theorydb:"attr:reason,omitempty" json:"reason,omitempty"`
	RequestID string `theorydb:"attr:requestId,omitempty" json:"request_id,omitempty"`

	RequestedCredits int64 `theorydb:"attr:requestedCredits" json:"requested_credits"`
	ListCredits      int64 `theorydb:"attr:listCredits" json:"list_credits,omitempty"`
	// PricingMultiplierBps records any applied multiplier (e.g., author-at-publish discount).
	// 10000 means no change.
	PricingMultiplierBps int64 `theorydb:"attr:pricingMultiplierBps" json:"pricing_multiplier_bps,omitempty"`
	DebitedCredits       int64 `theorydb:"attr:debitedCredits" json:"debited_credits"`

	IncludedDebitedCredits int64 `theorydb:"attr:includedDebitedCredits" json:"included_debited_credits"`
	OverageDebitedCredits  int64 `theorydb:"attr:overageDebitedCredits" json:"overage_debited_credits"`

	BillingType string `theorydb:"attr:billingType" json:"billing_type"` // included|overage|none|mixed

	ActorURI    string `theorydb:"attr:actorUri,omitempty" json:"actor_uri,omitempty"`
	ObjectURI   string `theorydb:"attr:objectUri,omitempty" json:"object_uri,omitempty"`
	ContentHash string `theorydb:"attr:contentHash,omitempty" json:"content_hash,omitempty"`
	LinksHash   string `theorydb:"attr:linksHash,omitempty" json:"links_hash,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for UsageLedgerEntry.
func (UsageLedgerEntry) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating UsageLedgerEntry.
func (e *UsageLedgerEntry) BeforeCreate() error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if err := e.UpdateKeys(); err != nil {
		return err
	}
	if strings.TrimSpace(e.BillingType) == "" {
		e.BillingType = BillingTypeNone
	}
	return nil
}

// UpdateKeys updates the database keys for UsageLedgerEntry.
func (e *UsageLedgerEntry) UpdateKeys() error {
	e.ID = strings.TrimSpace(e.ID)
	e.InstanceSlug = strings.TrimSpace(e.InstanceSlug)
	e.Month = strings.TrimSpace(e.Month)

	e.PK = fmt.Sprintf("USAGE#%s#%s", e.InstanceSlug, e.Month)
	// Sort by time then id for stable pagination.
	ts := e.CreatedAt.UTC().Format(time.RFC3339Nano)
	e.SK = fmt.Sprintf("ENTRY#%s#%s", ts, e.ID)
	return nil
}

// GetPK returns the partition key for UsageLedgerEntry.
func (e *UsageLedgerEntry) GetPK() string { return e.PK }

// GetSK returns the sort key for UsageLedgerEntry.
func (e *UsageLedgerEntry) GetSK() string { return e.SK }
