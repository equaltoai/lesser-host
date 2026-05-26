package models

import (
	"fmt"
	"strings"
	"time"
)

// CostTelemetryRetentionDays is the number of days cost telemetry records are
// retained before DynamoDB TTL expiry.
//
// The 90-day window supports M3.11's past-30-day default query window with
// ample buffer for Cost Explorer latency (up to 48 hours for current-day
// billing data to finalize), operator review of historical billing data, and
// the reconciliation window between CloudWatch metrics and Cost Explorer
// billing-grain data.
//
// This is a conservative bounded retention: no cost telemetry record lives
// indefinitely in the table. TTL is never zero or unbounded.
const CostTelemetryRetentionDays = 90

// CostTelemetry caches per-instance per-day reconciled cost data produced
// by the cost-telemetry worker (M3.10). Each record represents one day of
// billing data for a single managed instance.
//
// # Key design
//
// Primary key (PK = "COST_TELEMETRY#<slug>", SK = "<date>"):
//
//   - The PK embeds the instance slug, scoping all reads and writes to a
//     single tenant. This shape makes cross-tenant reads difficult to
//     perform accidentally.
//   - The SK is the date in YYYY-MM-DD format. Querying for a date range
//     uses SK >= "2026-01-01" AND SK <= "2026-01-31" on the main table.
//   - Rerunning the worker for the same slug and date upserts the same
//     record via CreateOrUpdate, making the write path naturally idempotent.
//
// # Data safety
//
// Contains no PII, tenant content, raw instance keys, secrets, or request
// bodies. All fields are derived from AWS billing and metric APIs and are
// safe for future customer exposure (M3.11+). The EntriesJSON field holds
// only ReconciledCostEntry values whose constituent types (ReconciledCostEntry,
// ServiceAttribution) are explicitly documented as safe for caching and
// customer exposure.
//
// # TTL
//
// Every record carries a DynamoDB TTL attribute set to ExpiresAt.Unix().
// The default TTL is CostTelemetryRetentionDays (90 days) from CreatedAt.
// Explicit ExpiresAt values are preserved to support per-record retention
// overrides if needed.
type CostTelemetry struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	InstanceSlug string  `theorydb:"attr:instanceSlug" json:"instance_slug"`
	Date         string  `theorydb:"attr:date" json:"date"` // YYYY-MM-DD
	EntriesJSON  string  `theorydb:"attr:entriesJson" json:"entries,omitempty"`
	DayCost      float64 `theorydb:"attr:dayCost" json:"day_cost"`
	Currency     string  `theorydb:"attr:currency" json:"currency"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	ExpiresAt time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
}

// TableName returns the database table name for CostTelemetry.
func (CostTelemetry) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating CostTelemetry.
func (c *CostTelemetry) BeforeCreate() error {
	if err := c.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if c.ExpiresAt.IsZero() {
		c.ExpiresAt = c.CreatedAt.Add(CostTelemetryRetentionDays * 24 * time.Hour)
	}
	c.TTL = c.ExpiresAt.Unix()
	return nil
}

// BeforeUpdate updates timestamps and TTL before updating CostTelemetry.
func (c *CostTelemetry) BeforeUpdate() error {
	c.UpdatedAt = time.Now().UTC()
	c.TTL = c.ExpiresAt.Unix()
	return nil
}

// UpdateKeys updates the database keys for CostTelemetry.
func (c *CostTelemetry) UpdateKeys() error {
	c.InstanceSlug = strings.ToLower(strings.TrimSpace(c.InstanceSlug))
	c.Date = strings.TrimSpace(c.Date)
	c.Currency = strings.TrimSpace(c.Currency)

	if c.InstanceSlug == "" {
		return fmt.Errorf("CostTelemetry: InstanceSlug is required")
	}
	if c.Date == "" {
		return fmt.Errorf("CostTelemetry: Date is required")
	}

	c.PK = fmt.Sprintf("COST_TELEMETRY#%s", c.InstanceSlug)
	c.SK = c.Date
	return nil
}

// GetPK returns the partition key for CostTelemetry.
func (c *CostTelemetry) GetPK() string { return c.PK }

// GetSK returns the sort key for CostTelemetry.
func (c *CostTelemetry) GetSK() string { return c.SK }
