package models

import "time"

// WebAuthnCredential stores a user's WebAuthn credential.
type WebAuthnCredential struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK     string `theorydb:"pk,attr:PK" json:"-"`
	SK     string `theorydb:"sk,attr:SK" json:"-"`
	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	ID              string    `theorydb:"attr:id" json:"id"`
	UserID          string    `theorydb:"attr:userID" json:"user_id"`
	PublicKey       []byte    `theorydb:"attr:publicKey" json:"public_key"`
	AttestationType string    `theorydb:"attr:attestationType" json:"attestation_type"`
	AAGUID          []byte    `theorydb:"attr:aaguid" json:"aaguid"`
	SignCount       uint32    `theorydb:"attr:signCount" json:"sign_count"`
	CloneWarning    bool      `theorydb:"attr:cloneWarning" json:"clone_warning"`
	UserPresent     *bool     `theorydb:"attr:userPresent,omitempty" json:"user_present,omitempty"`
	UserVerified    *bool     `theorydb:"attr:userVerified,omitempty" json:"user_verified,omitempty"`
	BackupEligible  bool      `theorydb:"attr:backupEligible" json:"backup_eligible"`
	BackupState     bool      `theorydb:"attr:backupState" json:"backup_state"`
	CreatedAt       time.Time `theorydb:"attr:createdAt" json:"created_at"`
	LastUsedAt      time.Time `theorydb:"attr:lastUsedAt" json:"last_used_at"`
	Name            string    `theorydb:"attr:name" json:"name"`

	Type string `theorydb:"attr:type" json:"Type"`
}

// TableName returns the database table name for WebAuthnCredential.
func (WebAuthnCredential) TableName() string {
	return MainTableName()
}

// BeforeCreate updates keys and timestamps before creating WebAuthnCredential.
func (w *WebAuthnCredential) BeforeCreate() error {
	return w.updateKeysWithTimestamps()
}

// BeforeUpdate updates timestamps and secondary index keys before updating WebAuthnCredential.
func (w *WebAuthnCredential) BeforeUpdate() error {
	w.LastUsedAt = time.Now().UTC()
	w.GSI1PK = "WEBAUTHN_CREDENTIAL#" + w.ID
	w.GSI1SK = "USER#" + w.UserID
	return nil
}

// UpdateKeys updates the database keys for WebAuthnCredential.
func (w *WebAuthnCredential) UpdateKeys() error {
	w.PK = "USER#" + w.UserID
	w.SK = "WEBAUTHN_CRED#" + w.ID
	w.GSI1PK = "WEBAUTHN_CREDENTIAL#" + w.ID
	w.GSI1SK = "USER#" + w.UserID
	w.Type = "WebAuthnCredential"
	return nil
}

func (w *WebAuthnCredential) updateKeysWithTimestamps() error {
	if err := w.UpdateKeys(); err != nil {
		return err
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}
	if w.LastUsedAt.IsZero() {
		w.LastUsedAt = w.CreatedAt
	}
	return nil
}

// GetPK returns the partition key for WebAuthnCredential.
func (w *WebAuthnCredential) GetPK() string { return w.PK }

// GetSK returns the sort key for WebAuthnCredential.
func (w *WebAuthnCredential) GetSK() string { return w.SK }
