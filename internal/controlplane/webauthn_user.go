package controlplane

import (
	"encoding/base64"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type webAuthnUser struct {
	id          string
	name        string
	displayName string
	credentials []webauthn.Credential
}

func (u *webAuthnUser) WebAuthnID() []byte {
	return []byte(u.id)
}

func (u *webAuthnUser) WebAuthnName() string {
	return u.name
}

func (u *webAuthnUser) WebAuthnDisplayName() string {
	return u.displayName
}

func (u *webAuthnUser) WebAuthnIcon() string {
	return ""
}

func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

func toWebAuthnCredential(c *models.WebAuthnCredential) *webauthn.Credential {
	credID, _ := base64.StdEncoding.DecodeString(c.ID)

	// Default to true for backward compatibility with credentials stored before
	// these flags were persisted.
	userPresent := true
	if c.UserPresent != nil {
		userPresent = *c.UserPresent
	}
	userVerified := true
	if c.UserVerified != nil {
		userVerified = *c.UserVerified
	}

	return &webauthn.Credential{
		ID:              credID,
		PublicKey:       c.PublicKey,
		AttestationType: c.AttestationType,
		Flags: webauthn.CredentialFlags{
			UserPresent:    userPresent,
			UserVerified:   userVerified,
			BackupEligible: c.BackupEligible,
			BackupState:    c.BackupState,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:       c.AAGUID,
			SignCount:    c.SignCount,
			CloneWarning: c.CloneWarning,
		},
	}
}
