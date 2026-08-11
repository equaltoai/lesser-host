package models

import (
	"fmt"
	"strings"
	"time"
)

const auditLogPartitionKeyMaxBytes = 1024

// AuditLogEntry records an operator action for auditing.
type AuditLogEntry struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	ID        string    `theorydb:"attr:id" json:"id"`
	Actor     string    `theorydb:"attr:actor" json:"actor"`
	Action    string    `theorydb:"attr:action" json:"action"`
	Target    string    `theorydb:"attr:target" json:"target"`
	Details   string    `theorydb:"attr:details,omitempty" json:"details,omitempty"`
	RequestID string    `theorydb:"attr:requestID" json:"request_id"`
	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`

	// ActedBy is pure caller attribution (local lesser username of the real human
	// who initiated the action under a share grant). Like source provenance, it
	// is audit context only and must never become an authorization input.
	ActedBy string `theorydb:"attr:actedBy,omitempty" json:"acted_by,omitempty"`

	// Source provenance is provider-derived request metadata used only for
	// audit/rate-limit context. It must never become an authorization input.
	SourceIP         string `theorydb:"attr:sourceIP,omitempty" json:"source_ip,omitempty"`
	SourceProvider   string `theorydb:"attr:sourceProvider,omitempty" json:"source_provider,omitempty"`
	SourceProvenance string `theorydb:"attr:sourceProvenance,omitempty" json:"source_provenance,omitempty"`
	SourceValid      bool   `theorydb:"attr:sourceValid,omitempty" json:"source_valid,omitempty"`
}

// TableName returns the database table name for AuditLogEntry.
func (AuditLogEntry) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating AuditLogEntry.
func (a *AuditLogEntry) BeforeCreate() error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	return a.UpdateKeys()
}

// UpdateKeys updates the database keys for AuditLogEntry.
func (a *AuditLogEntry) UpdateKeys() error {
	createdAt := a.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	a.ID = strings.TrimSpace(a.ID)
	if a.ID == "" {
		a.ID = fmt.Sprintf("%d", createdAt.UnixNano())
	}
	target := strings.TrimSpace(a.Target)
	a.ActedBy = strings.TrimSpace(a.ActedBy)
	pk := fmt.Sprintf("AUDIT#%s", target)
	if len(pk) > auditLogPartitionKeyMaxBytes {
		a.PK = ""
		return fmt.Errorf("audit log target too long: partition key length %d exceeds %d bytes", len(pk), auditLogPartitionKeyMaxBytes)
	}
	a.PK = pk
	a.SK = fmt.Sprintf("EVENT#%s#%s", createdAt.Format(time.RFC3339Nano), a.ID)
	return nil
}

// GetPK returns the partition key for AuditLogEntry.
func (a *AuditLogEntry) GetPK() string { return a.PK }

// GetSK returns the sort key for AuditLogEntry.
func (a *AuditLogEntry) GetSK() string { return a.SK }
