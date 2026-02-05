package models

import "time"

// WebAuthnChallenge stores a short-lived WebAuthn authentication challenge.
type WebAuthnChallenge struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Challenge   string    `theorydb:"attr:challenge" json:"challenge"`
	UserID      string    `theorydb:"attr:userID" json:"user_id"`
	SessionData []byte    `theorydb:"attr:sessionData" json:"session_data"`
	ExpiresAt   time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
	Type        string    `theorydb:"attr:type" json:"type"`

	ItemType string `theorydb:"attr:itemType" json:"ItemType"`

	TTL int64 `theorydb:"ttl,attr:ttl" json:"-"`
}

// TableName returns the database table name for WebAuthnChallenge.
func (WebAuthnChallenge) TableName() string {
	return MainTableName()
}

// BeforeCreate sets keys before creating WebAuthnChallenge.
func (w *WebAuthnChallenge) BeforeCreate() error {
	return w.UpdateKeys()
}

// UpdateKeys updates the database keys and TTL for WebAuthnChallenge.
func (w *WebAuthnChallenge) UpdateKeys() error {
	w.PK = "CHALLENGE#" + w.Challenge
	w.SK = "WEBAUTHN"
	w.ItemType = "WebAuthnChallenge"
	if w.TTL == 0 && !w.ExpiresAt.IsZero() {
		w.TTL = w.ExpiresAt.Unix()
	}
	return nil
}

// GetPK returns the partition key for WebAuthnChallenge.
func (w *WebAuthnChallenge) GetPK() string { return w.PK }

// GetSK returns the sort key for WebAuthnChallenge.
func (w *WebAuthnChallenge) GetSK() string { return w.SK }
