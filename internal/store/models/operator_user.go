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

// UserApprovalStatus* constants define portal approval states.
const (
	UserApprovalStatusPending  = "pending"
	UserApprovalStatusApproved = "approved"
	UserApprovalStatusRejected = "rejected"
)

// User represents an authenticated portal user or operator account.
type User struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	Username       string    `theorydb:"attr:username" json:"username"`
	Role           string    `theorydb:"attr:role" json:"role"`
	Approved       bool      `theorydb:"attr:approved" json:"approved"`
	ApprovalStatus string    `theorydb:"attr:approvalStatus" json:"approval_status,omitempty"`
	ReviewedBy     string    `theorydb:"attr:reviewedBy" json:"reviewed_by,omitempty"`
	ReviewedAt     time.Time `theorydb:"attr:reviewedAt" json:"reviewed_at,omitempty"`
	ApprovalNote   string    `theorydb:"attr:approvalNote" json:"approval_note,omitempty"`
	DisplayName    string    `theorydb:"attr:displayName" json:"display_name,omitempty"`
	Email          string    `theorydb:"attr:email" json:"email,omitempty"`
	CreatedAt      time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for User.
func (User) TableName() string {
	return MainTableName()
}

// BeforeCreate sets defaults and keys before creating User.
func (u *User) BeforeCreate() error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	u.Username = strings.TrimSpace(u.Username)
	u.DisplayName = strings.TrimSpace(u.DisplayName)
	u.Email = strings.TrimSpace(u.Email)
	if u.Role == "" {
		u.Role = RoleOperator
	}
	u.normalizeApprovalStatus()
	if err := u.UpdateKeys(); err != nil {
		return err
	}
	return nil
}

func (u *User) normalizeApprovalStatus() {
	status := strings.ToLower(strings.TrimSpace(u.ApprovalStatus))
	switch status {
	case UserApprovalStatusApproved, UserApprovalStatusRejected, UserApprovalStatusPending:
	default:
		if u.Approved {
			status = UserApprovalStatusApproved
		} else {
			status = UserApprovalStatusPending
		}
	}
	u.ApprovalStatus = status
	u.Approved = status == UserApprovalStatusApproved
}

// UpdateKeys updates the database keys for User.
func (u *User) UpdateKeys() error {
	username := strings.TrimSpace(u.Username)
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	u.normalizeApprovalStatus()
	u.ReviewedBy = strings.TrimSpace(u.ReviewedBy)
	u.ApprovalNote = strings.TrimSpace(u.ApprovalNote)
	u.PK = fmt.Sprintf(KeyPatternUser, username)
	u.SK = SKProfile
	u.GSI1PK = fmt.Sprintf("USER_APPROVAL#%s", u.ApprovalStatus)
	u.GSI1SK = fmt.Sprintf("%s#%s", u.CreatedAt.UTC().Format(time.RFC3339Nano), username)
	return nil
}

// GetPK returns the partition key for User.
func (u *User) GetPK() string { return u.PK }

// GetSK returns the sort key for User.
func (u *User) GetSK() string { return u.SK }
