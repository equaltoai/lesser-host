package models

import (
	"fmt"
	"strings"
	"time"
)

// Role* constants define operator roles.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
)

// OperatorUser represents an administrative operator account.
type OperatorUser struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Username    string    `theorydb:"attr:username" json:"username"`
	Role        string    `theorydb:"attr:role" json:"role"`
	DisplayName string    `theorydb:"attr:displayName" json:"display_name,omitempty"`
	CreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for OperatorUser.
func (OperatorUser) TableName() string {
	return MainTableName()
}

// BeforeCreate sets defaults and keys before creating OperatorUser.
func (u *OperatorUser) BeforeCreate() error {
	if err := u.UpdateKeys(); err != nil {
		return err
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	u.Username = strings.TrimSpace(u.Username)
	if u.Role == "" {
		u.Role = RoleOperator
	}
	return nil
}

// UpdateKeys updates the database keys for OperatorUser.
func (u *OperatorUser) UpdateKeys() error {
	username := strings.TrimSpace(u.Username)
	u.PK = fmt.Sprintf(KeyPatternUser, username)
	u.SK = SKProfile
	return nil
}

// GetPK returns the partition key for OperatorUser.
func (u *OperatorUser) GetPK() string { return u.PK }

// GetSK returns the sort key for OperatorUser.
func (u *OperatorUser) GetSK() string { return u.SK }
