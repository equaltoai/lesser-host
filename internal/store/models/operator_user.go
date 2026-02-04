package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
)

type OperatorUser struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Username    string    `theorydb:"attr:username" json:"username"`
	Role        string    `theorydb:"attr:role" json:"role"`
	DisplayName string    `theorydb:"attr:displayName" json:"display_name,omitempty"`
	CreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

func (OperatorUser) TableName() string {
	return MainTableName()
}

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

func (u *OperatorUser) UpdateKeys() error {
	username := strings.TrimSpace(u.Username)
	u.PK = fmt.Sprintf(KeyPatternUser, username)
	u.SK = SKProfile
	return nil
}

func (u *OperatorUser) GetPK() string { return u.PK }
func (u *OperatorUser) GetSK() string { return u.SK }

