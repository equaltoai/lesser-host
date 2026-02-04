package models

import (
	"fmt"
	"strings"
	"time"
)

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

func (AuditLogEntry) TableName() string { return MainTableName() }

func (a *AuditLogEntry) BeforeCreate() error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	return a.UpdateKeys()
}

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

func (a *AuditLogEntry) GetPK() string { return a.PK }
func (a *AuditLogEntry) GetSK() string { return a.SK }

