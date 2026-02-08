package models

import (
	"fmt"
	"strings"
	"time"
)

// Role* constants define portal and operator roles.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleCustomer = "customer"
)

// User represents an authenticated portal user or operator account.
type User struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Username    string    `theorydb:"attr:username" json:"username"`
	Role        string    `theorydb:"attr:role" json:"role"`
	Approved    bool      `theorydb:"attr:approved" json:"approved"`
	DisplayName string    `theorydb:"attr:displayName" json:"display_name,omitempty"`
	Email       string    `theorydb:"attr:email" json:"email,omitempty"`
	CreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for User.
func (User) TableName() string {
	return MainTableName()
}

// BeforeCreate sets defaults and keys before creating User.
func (u *User) BeforeCreate() error {
	if err := u.UpdateKeys(); err != nil {
		return err
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	u.Username = strings.TrimSpace(u.Username)
	u.DisplayName = strings.TrimSpace(u.DisplayName)
	u.Email = strings.TrimSpace(u.Email)
	if u.Role == "" {
		u.Role = RoleOperator
	}
	return nil
}

// UpdateKeys updates the database keys for User.
func (u *User) UpdateKeys() error {
	username := strings.TrimSpace(u.Username)
	u.PK = fmt.Sprintf(KeyPatternUser, username)
	u.SK = SKProfile
	return nil
}

// GetPK returns the partition key for User.
func (u *User) GetPK() string { return u.PK }

// GetSK returns the sort key for User.
func (u *User) GetSK() string { return u.SK }
