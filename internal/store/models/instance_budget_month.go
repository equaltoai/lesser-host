package models

import (
	"fmt"
	"strings"
	"time"
)

type InstanceBudgetMonth struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	InstanceSlug    string    `theorydb:"attr:instanceSlug" json:"instance_slug"`
	Month           string    `theorydb:"attr:month" json:"month"` // YYYY-MM
	IncludedCredits int64     `theorydb:"attr:includedCredits" json:"included_credits"`
	UsedCredits     int64     `theorydb:"attr:usedCredits" json:"used_credits"`
	UpdatedAt       time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

func (InstanceBudgetMonth) TableName() string { return MainTableName() }

func (b *InstanceBudgetMonth) BeforeCreate() error {
	if err := b.UpdateKeys(); err != nil {
		return err
	}
	if b.UpdatedAt.IsZero() {
		b.UpdatedAt = time.Now().UTC()
	}
	return nil
}

func (b *InstanceBudgetMonth) BeforeUpdate() error {
	b.UpdatedAt = time.Now().UTC()
	return nil
}

func (b *InstanceBudgetMonth) UpdateKeys() error {
	slug := strings.TrimSpace(b.InstanceSlug)
	month := strings.TrimSpace(b.Month)
	b.PK = fmt.Sprintf("INSTANCE#%s", slug)
	b.SK = fmt.Sprintf("BUDGET#%s", month)
	return nil
}

func (b *InstanceBudgetMonth) GetPK() string { return b.PK }
func (b *InstanceBudgetMonth) GetSK() string { return b.SK }

