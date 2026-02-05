package models

import (
	"fmt"
	"strings"
	"time"
)

// TipHostState stores the latest observed on-chain host registry configuration for a hostId.
type TipHostState struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	ChainID         int64  `theorydb:"attr:chainID" json:"chain_id"`
	ContractAddress string `theorydb:"attr:contractAddress" json:"contract_address"`

	DomainNormalized string `theorydb:"attr:domainNormalized" json:"domain_normalized,omitempty"`
	HostIDHex        string `theorydb:"attr:hostIdHex" json:"host_id_hex"`

	WalletAddr string `theorydb:"attr:walletAddress" json:"wallet_address,omitempty"`
	HostFeeBps int64  `theorydb:"attr:hostFeeBps" json:"host_fee_bps,omitempty"`
	IsActive   bool   `theorydb:"attr:isActive" json:"is_active"`

	ObservedAt time.Time `theorydb:"attr:observedAt" json:"observed_at"`
	UpdatedAt  time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

// TableName returns the database table name for TipHostState.
func (TipHostState) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating TipHostState.
func (s *TipHostState) BeforeCreate() error {
	if err := s.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if s.ObservedAt.IsZero() {
		s.ObservedAt = now
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = now
	}
	return nil
}

// BeforeUpdate updates timestamps before updating TipHostState.
func (s *TipHostState) BeforeUpdate() error {
	s.UpdatedAt = time.Now().UTC()
	return s.UpdateKeys()
}

// UpdateKeys updates the database keys for TipHostState.
func (s *TipHostState) UpdateKeys() error {
	s.ContractAddress = strings.ToLower(strings.TrimSpace(s.ContractAddress))
	s.DomainNormalized = strings.ToLower(strings.TrimSpace(s.DomainNormalized))
	s.HostIDHex = strings.ToLower(strings.TrimSpace(s.HostIDHex))
	s.WalletAddr = strings.ToLower(strings.TrimSpace(s.WalletAddr))

	s.PK = fmt.Sprintf("TIPHOST#%s", s.HostIDHex)
	s.SK = "STATE"
	return nil
}

// GetPK returns the partition key for TipHostState.
func (s *TipHostState) GetPK() string { return s.PK }

// GetSK returns the sort key for TipHostState.
func (s *TipHostState) GetSK() string { return s.SK }
