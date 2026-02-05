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
	if err := k.UpdateKeys(); err != nil {
		return err
	}
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now().UTC()
	}
	return nil
}

// UpdateKeys updates the database keys for InstanceKey.
func (k *InstanceKey) UpdateKeys() error {
	id := strings.TrimSpace(k.ID)
	k.PK = fmt.Sprintf("INSTANCE_KEY#%s", id)
	k.SK = "KEY"
	return nil
}

// GetPK returns the partition key for InstanceKey.
func (k *InstanceKey) GetPK() string { return k.PK }

// GetSK returns the sort key for InstanceKey.
func (k *InstanceKey) GetSK() string { return k.SK }
