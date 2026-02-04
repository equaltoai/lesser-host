package models

import (
	"fmt"
	"strings"
	"time"
)

type WalletChallenge struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	ID        string    `theorydb:"attr:id" json:"id"`
	Username  string    `theorydb:"attr:username" json:"username,omitempty"`
	Address   string    `theorydb:"attr:address" json:"address"`
	ChainID   int       `theorydb:"attr:chainID" json:"chain_id"`
	Nonce     string    `theorydb:"attr:nonce" json:"nonce"`
	Message   string    `theorydb:"attr:message" json:"message"`
	IssuedAt  time.Time `theorydb:"attr:issuedAt" json:"issued_at"`
	ExpiresAt time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
	Used      bool      `theorydb:"attr:used" json:"used"`
	Spent     bool      `theorydb:"attr:spent" json:"spent"`
}

func (WalletChallenge) TableName() string {
	return MainTableName()
}

func (w *WalletChallenge) BeforeCreate() error {
	if err := w.UpdateKeys(); err != nil {
		return fmt.Errorf("update keys: %w", err)
	}
	if w.IssuedAt.IsZero() {
		w.IssuedAt = time.Now().UTC()
	}
	return nil
}

func (w *WalletChallenge) UpdateKeys() error {
	w.PK = fmt.Sprintf("WALLET_CHALLENGE#%s", strings.TrimSpace(w.ID))
	w.SK = "CHALLENGE"
	w.TTL = w.ExpiresAt.Unix()
	return nil
}

func (w *WalletChallenge) GetPK() string { return w.PK }
func (w *WalletChallenge) GetSK() string { return w.SK }

type WalletCredential struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Username string    `theorydb:"attr:username" json:"username"`
	Address  string    `theorydb:"attr:address" json:"address"`
	ChainID  int       `theorydb:"attr:chainID" json:"chain_id"`
	Type     string    `theorydb:"attr:type" json:"type"`
	ENS      string    `theorydb:"attr:ens" json:"ens,omitempty"`
	LinkedAt time.Time `theorydb:"attr:linkedAt" json:"linked_at"`
	LastUsed time.Time `theorydb:"attr:lastUsed" json:"last_used"`
}

func (WalletCredential) TableName() string {
	return MainTableName()
}

func (w *WalletCredential) BeforeCreate() error {
	if err := w.UpdateKeys(); err != nil {
		return fmt.Errorf("update keys: %w", err)
	}
	if w.LinkedAt.IsZero() {
		w.LinkedAt = time.Now().UTC()
	}
	if w.LastUsed.IsZero() {
		w.LastUsed = w.LinkedAt
	}
	if strings.TrimSpace(w.Type) == "" {
		w.Type = "ethereum"
	}
	return nil
}

func (w *WalletCredential) BeforeUpdate() error {
	w.LastUsed = time.Now().UTC()
	return nil
}

func (w *WalletCredential) UpdateKeys() error {
	address := strings.ToLower(strings.TrimSpace(w.Address))
	w.PK = fmt.Sprintf(KeyPatternUser, strings.TrimSpace(w.Username))
	w.SK = fmt.Sprintf("WALLET#%s", address)
	return nil
}

func (w *WalletCredential) GetPK() string { return w.PK }
func (w *WalletCredential) GetSK() string { return w.SK }

type WalletIndex struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Username   string `theorydb:"attr:username" json:"username"`
	WalletType string `theorydb:"attr:walletType" json:"wallet_type"`
	Address    string `theorydb:"attr:address" json:"address"`
}

func (WalletIndex) TableName() string {
	return MainTableName()
}

func (w *WalletIndex) BeforeCreate() error {
	if strings.TrimSpace(w.WalletType) == "" {
		w.WalletType = "ethereum"
	}
	w.UpdateKeys(w.WalletType, w.Address, w.Username)
	return nil
}

func (w *WalletIndex) UpdateKeys(walletType, address, username string) {
	address = strings.ToLower(strings.TrimSpace(address))
	w.PK = fmt.Sprintf("WALLET#%s#%s", walletType, address)
	w.SK = fmt.Sprintf(KeyPatternUser, strings.TrimSpace(username))
	w.Username = strings.TrimSpace(username)
	w.WalletType = walletType
	w.Address = address
}

func (w *WalletIndex) GetPK() string { return w.PK }
func (w *WalletIndex) GetSK() string { return w.SK }

