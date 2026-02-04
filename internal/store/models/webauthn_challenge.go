package models

import "time"

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

func (WebAuthnChallenge) TableName() string {
	return MainTableName()
}

func (w *WebAuthnChallenge) BeforeCreate() error {
	return w.UpdateKeys()
}

func (w *WebAuthnChallenge) UpdateKeys() error {
	w.PK = "CHALLENGE#" + w.Challenge
	w.SK = "WEBAUTHN"
	w.ItemType = "WebAuthnChallenge"
	if w.TTL == 0 && !w.ExpiresAt.IsZero() {
		w.TTL = w.ExpiresAt.Unix()
	}
	return nil
}

func (w *WebAuthnChallenge) GetPK() string { return w.PK }
func (w *WebAuthnChallenge) GetSK() string { return w.SK }

