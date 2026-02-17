package models

import (
	"fmt"
	"strings"
	"time"
)

// InstanceKey represents an API key issued to an instance.
type InstanceKey struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	ID           string    `theorydb:"attr:id" json:"id"`
	InstanceSlug string    `theorydb:"attr:instanceSlug" json:"instance_slug"`
	CreatedAt    time.Time `theorydb:"attr:createdAt" json:"created_at"`
	LastUsedAt   time.Time `theorydb:"attr:lastUsedAt" json:"last_used_at,omitempty"`
	RevokedAt    time.Time `theorydb:"attr:revokedAt" json:"revoked_at,omitempty"`
}

// TableName returns the database table name for InstanceKey.
func (InstanceKey) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating InstanceKey.
func (k *InstanceKey) BeforeCreate() error {
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now().UTC()
	}
	if err := k.UpdateKeys(); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for InstanceKey.
func (k *InstanceKey) UpdateKeys() error {
	id := strings.TrimSpace(k.ID)
	slug := strings.ToLower(strings.TrimSpace(k.InstanceSlug))
	k.PK = fmt.Sprintf("INSTANCE_KEY#%s", id)
	k.SK = "KEY"

	if slug == "" {
		k.GSI1PK = ""
		k.GSI1SK = ""
		return nil
	}
	createdAt := k.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	k.GSI1PK = fmt.Sprintf("INSTANCE_KEYS#%s", slug)
	k.GSI1SK = fmt.Sprintf("%s#%s", createdAt.UTC().Format(time.RFC3339Nano), id)
	return nil
}

// GetPK returns the partition key for InstanceKey.
func (k *InstanceKey) GetPK() string { return k.PK }

// GetSK returns the sort key for InstanceKey.
func (k *InstanceKey) GetSK() string { return k.SK }
