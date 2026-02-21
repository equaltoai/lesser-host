package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulWalletRotationRequest represents a pending wallet rotation proposal for an agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: ROTATION#{username}
type SoulWalletRotationRequest struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	AgentID  string `theorydb:"attr:agentId" json:"agent_id"`
	Username string `theorydb:"attr:username" json:"username"`

	CurrentWallet string `theorydb:"attr:currentWallet" json:"current_wallet"`
	NewWallet     string `theorydb:"attr:newWallet" json:"new_wallet"`

	Nonce    string `theorydb:"attr:nonce" json:"nonce"`       // decimal uint256 string
	Deadline int64  `theorydb:"attr:deadline" json:"deadline"` // unix seconds

	DigestHex string `theorydb:"attr:digestHex" json:"digest_hex"` // 0x-prefixed 32-byte hex

	Spent bool `theorydb:"attr:spent" json:"spent"`

	CreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt   time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	ExpiresAt   time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
	ConfirmedAt time.Time `theorydb:"attr:confirmedAt" json:"confirmed_at,omitempty"`
}

// TableName returns the database table name for SoulWalletRotationRequest.
func (SoulWalletRotationRequest) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulWalletRotationRequest.
func (r *SoulWalletRotationRequest) BeforeCreate() error {
	if err := r.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	return nil
}

// BeforeUpdate updates timestamps and keys before updating SoulWalletRotationRequest.
func (r *SoulWalletRotationRequest) BeforeUpdate() error {
	r.UpdatedAt = time.Now().UTC()
	return r.UpdateKeys()
}

// UpdateKeys updates the database keys and TTL for SoulWalletRotationRequest.
func (r *SoulWalletRotationRequest) UpdateKeys() error {
	if r == nil {
		return nil
	}

	r.AgentID = strings.ToLower(strings.TrimSpace(r.AgentID))
	r.Username = strings.TrimSpace(r.Username)
	r.CurrentWallet = strings.ToLower(strings.TrimSpace(r.CurrentWallet))
	r.NewWallet = strings.ToLower(strings.TrimSpace(r.NewWallet))
	r.Nonce = strings.TrimSpace(r.Nonce)
	r.DigestHex = strings.ToLower(strings.TrimSpace(r.DigestHex))

	r.PK = fmt.Sprintf("SOUL#AGENT#%s", r.AgentID)
	r.SK = fmt.Sprintf("ROTATION#%s", r.Username)

	if !r.ExpiresAt.IsZero() {
		r.TTL = r.ExpiresAt.UTC().Unix()
	}
	return nil
}

// GetPK returns the partition key for SoulWalletRotationRequest.
func (r *SoulWalletRotationRequest) GetPK() string { return r.PK }

// GetSK returns the sort key for SoulWalletRotationRequest.
func (r *SoulWalletRotationRequest) GetSK() string { return r.SK }
