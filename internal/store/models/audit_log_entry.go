package models

import (
	"fmt"
	"strings"
	"time"
)

// AuditLogEntry records an operator action for auditing.
type AuditLogEntry struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	ID        string    `theorydb:"attr:id" json:"id"`
	Actor     string    `theorydb:"attr:actor" json:"actor"`
	Action    string    `theorydb:"attr:action" json:"action"`
	Target    string    `theorydb:"attr:target" json:"target"`
	RequestID string    `theorydb:"attr:requestID" json:"request_id"`
	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
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
	a.PK = fmt.Sprintf("AUDIT#%s", target)
	a.SK = fmt.Sprintf("EVENT#%s#%s", createdAt.Format(time.RFC3339Nano), a.ID)
	return nil
}

// GetPK returns the partition key for AuditLogEntry.
func (a *AuditLogEntry) GetPK() string { return a.PK }

// GetSK returns the sort key for AuditLogEntry.
func (a *AuditLogEntry) GetSK() string { return a.SK }
