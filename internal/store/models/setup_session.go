package models

import (
	"fmt"
	"strings"
	"time"
)

type SetupSession struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	ID           string    `theorydb:"attr:id" json:"id"`
	Purpose      string    `theorydb:"attr:purpose" json:"purpose"`
	WalletType   string    `theorydb:"attr:walletType" json:"wallet_type"`
	WalletAddr   string    `theorydb:"attr:walletAddress" json:"wallet_address"`
	IssuedAt     time.Time `theorydb:"attr:issuedAt" json:"issued_at"`
	ExpiresAt    time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
	InstanceLock bool      `theorydb:"attr:instanceLocked" json:"instance_locked"`
}

func (SetupSession) TableName() string {
	return MainTableName()
}

func (s *SetupSession) UpdateKeys() error {
	s.ID = strings.TrimSpace(s.ID)
	s.PK = fmt.Sprintf("SETUP_SESSION#%s", s.ID)
	s.SK = "SESSION"
	s.TTL = s.ExpiresAt.Unix()
	return nil
}

func (s *SetupSession) GetPK() string { return s.PK }
func (s *SetupSession) GetSK() string { return s.SK }
