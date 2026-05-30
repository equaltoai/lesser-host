package models

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// UsageMeteringDedup reserves a deterministic metering scope before mutating
// usage budgets. Creating this record inside the same transaction as the ledger
// entry makes replayed provider webhooks idempotent even when the human-facing
// ledger entry remains time-sorted by CreatedAt.
type UsageMeteringDedup struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	ID           string `theorydb:"attr:id" json:"id"`
	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug"`
	Month        string `theorydb:"attr:month" json:"month"`
	Module       string `theorydb:"attr:module" json:"module"`
	Target       string `theorydb:"attr:target" json:"target"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for UsageMeteringDedup.
func (UsageMeteringDedup) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating UsageMeteringDedup.
func (d *UsageMeteringDedup) BeforeCreate() error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if err := d.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", d.InstanceSlug); err != nil {
		return err
	}
	if err := requireNonEmpty("month", d.Month); err != nil {
		return err
	}
	if err := requireNonEmpty("module", d.Module); err != nil {
		return err
	}
	return requireNonEmpty("target", d.Target)
}

// UpdateKeys updates the database keys for UsageMeteringDedup.
func (d *UsageMeteringDedup) UpdateKeys() error {
	d.InstanceSlug = strings.TrimSpace(d.InstanceSlug)
	d.Month = strings.TrimSpace(d.Month)
	d.Module = strings.TrimSpace(d.Module)
	d.Target = strings.TrimSpace(d.Target)
	d.ID = UsageMeteringDedupID(d.InstanceSlug, d.Month, d.Module, d.Target)
	d.PK = UsageMeteringDedupPK(d.InstanceSlug, d.Month)
	d.SK = UsageMeteringDedupSK(d.ID)
	return nil
}

// GetPK returns the partition key for UsageMeteringDedup.
func (d *UsageMeteringDedup) GetPK() string { return d.PK }

// GetSK returns the sort key for UsageMeteringDedup.
func (d *UsageMeteringDedup) GetSK() string { return d.SK }

// UsageMeteringDedupID returns the deterministic metering scope hash.
func UsageMeteringDedupID(instanceSlug string, month string, module string, target string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"usage-metering-dedup",
		strings.TrimSpace(instanceSlug),
		strings.TrimSpace(month),
		strings.TrimSpace(module),
		strings.TrimSpace(target),
	}, "|")))
	return hex.EncodeToString(sum[:])
}

// UsageMeteringDedupPK returns the partition key for usage metering dedup records.
func UsageMeteringDedupPK(instanceSlug string, month string) string {
	return fmt.Sprintf("USAGE_DEDUP#%s#%s", strings.TrimSpace(instanceSlug), strings.TrimSpace(month))
}

// UsageMeteringDedupSK returns the sort key for a deterministic metering scope.
func UsageMeteringDedupSK(id string) string {
	return "ENTRY#" + strings.TrimSpace(id)
}
