package models

import (
	"fmt"
	"strings"
	"time"
)

// ProvisionConsentChallenge represents a short-lived managed provisioning consent message.
type ProvisionConsentChallenge struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	ID           string `theorydb:"attr:id" json:"id"`
	Username     string `theorydb:"attr:username" json:"username"`
	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug"`
	Stage        string `theorydb:"attr:stage" json:"stage"`
	AdminUsername string `theorydb:"attr:adminUsername" json:"admin_username"`

	WalletType string `theorydb:"attr:walletType" json:"wallet_type"`
	WalletAddr string `theorydb:"attr:walletAddress" json:"wallet_address"`
	ChainID    int    `theorydb:"attr:chainID" json:"chain_id"`

	Nonce     string    `theorydb:"attr:nonce" json:"nonce"`
	Message   string    `theorydb:"attr:message" json:"message"`
	IssuedAt  time.Time `theorydb:"attr:issuedAt" json:"issued_at"`
	ExpiresAt time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
}

// TableName returns the database table name for ProvisionConsentChallenge.
func (ProvisionConsentChallenge) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating ProvisionConsentChallenge.
func (c *ProvisionConsentChallenge) BeforeCreate() error {
	if err := c.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if c.IssuedAt.IsZero() {
		c.IssuedAt = now
	}
	if c.ExpiresAt.IsZero() {
		c.ExpiresAt = now.Add(10 * time.Minute)
	}
	c.TTL = c.ExpiresAt.Unix()
	if strings.TrimSpace(c.WalletType) == "" {
		c.WalletType = walletTypeEthereum
	}
	return nil
}

// UpdateKeys updates the database keys and TTL for ProvisionConsentChallenge.
func (c *ProvisionConsentChallenge) UpdateKeys() error {
	c.ID = strings.TrimSpace(c.ID)
	c.Username = strings.TrimSpace(c.Username)
	c.InstanceSlug = strings.ToLower(strings.TrimSpace(c.InstanceSlug))
	c.Stage = strings.TrimSpace(c.Stage)
	c.AdminUsername = strings.TrimSpace(c.AdminUsername)
	c.WalletType = strings.TrimSpace(c.WalletType)
	c.WalletAddr = strings.ToLower(strings.TrimSpace(c.WalletAddr))
	c.Nonce = strings.TrimSpace(c.Nonce)
	c.Message = strings.TrimSpace(c.Message)

	c.PK = fmt.Sprintf("PROVISION_CONSENT#%s", c.ID)
	c.SK = "CHALLENGE"
	c.TTL = c.ExpiresAt.Unix()
	return nil
}

// GetPK returns the partition key for ProvisionConsentChallenge.
func (c *ProvisionConsentChallenge) GetPK() string { return c.PK }

// GetSK returns the sort key for ProvisionConsentChallenge.
func (c *ProvisionConsentChallenge) GetSK() string { return c.SK }

