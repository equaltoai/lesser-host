package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	ExternalInstanceRegistrationStatusPending  = "pending"
	ExternalInstanceRegistrationStatusApproved = "approved"
	ExternalInstanceRegistrationStatusRejected = "rejected"
)

// ExternalInstanceRegistration represents a request to register a non-hosted instance with lesser.host.
type ExternalInstanceRegistration struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	ID string `theorydb:"attr:id" json:"id"`

	Username string `theorydb:"attr:username" json:"username"`
	Slug     string `theorydb:"attr:slug" json:"slug"`

	Status string `theorydb:"attr:status" json:"status"` // pending|approved|rejected

	ReviewedBy string    `theorydb:"attr:reviewedBy" json:"reviewed_by,omitempty"`
	ReviewedAt time.Time `theorydb:"attr:reviewedAt" json:"reviewed_at,omitempty"`
	Note       string    `theorydb:"attr:note" json:"note,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

// TableName returns the database table name for ExternalInstanceRegistration.
func (ExternalInstanceRegistration) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating ExternalInstanceRegistration.
func (r *ExternalInstanceRegistration) BeforeCreate() error {
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	if strings.TrimSpace(r.Status) == "" {
		r.Status = ExternalInstanceRegistrationStatusPending
	}
	return r.UpdateKeys()
}

// BeforeUpdate updates timestamps and keys before updating ExternalInstanceRegistration.
func (r *ExternalInstanceRegistration) BeforeUpdate() error {
	r.UpdatedAt = time.Now().UTC()
	return r.UpdateKeys()
}

// UpdateKeys updates the database keys for ExternalInstanceRegistration.
func (r *ExternalInstanceRegistration) UpdateKeys() error {
	r.ID = strings.TrimSpace(r.ID)
	r.Username = strings.TrimSpace(r.Username)
	r.Slug = strings.ToLower(strings.TrimSpace(r.Slug))
	r.Status = strings.ToLower(strings.TrimSpace(r.Status))
	r.ReviewedBy = strings.TrimSpace(r.ReviewedBy)
	r.Note = strings.TrimSpace(r.Note)

	r.PK = fmt.Sprintf(KeyPatternUser, r.Username)
	r.SK = fmt.Sprintf("EXTERNAL_INSTANCE_REG#%s", r.ID)

	r.GSI1PK = fmt.Sprintf("EXTERNAL_INSTANCE_REGS#%s", r.Status)
	r.GSI1SK = fmt.Sprintf("%s#%s", r.CreatedAt.UTC().Format(time.RFC3339Nano), r.ID)

	return nil
}

// GetPK returns the partition key for ExternalInstanceRegistration.
func (r *ExternalInstanceRegistration) GetPK() string { return r.PK }

// GetSK returns the sort key for ExternalInstanceRegistration.
func (r *ExternalInstanceRegistration) GetSK() string { return r.SK }
