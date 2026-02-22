package controlplane

import (
	"errors"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/equaltoai/lesser-host/internal/config"
)

type webAuthnEngine interface {
	BeginRegistration(user webauthn.User, opts ...webauthn.RegistrationOption) (creation *protocol.CredentialCreation, session *webauthn.SessionData, err error)
	CreateCredential(user webauthn.User, session webauthn.SessionData, parsedResponse *protocol.ParsedCredentialCreationData) (credential *webauthn.Credential, err error)
	BeginLogin(user webauthn.User, opts ...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error)
	ValidateLogin(user webauthn.User, session webauthn.SessionData, parsedResponse *protocol.ParsedCredentialAssertionData) (credential *webauthn.Credential, err error)
}

func newWebAuthnEngine(cfg config.Config) (webAuthnEngine, error) {
	rpID := strings.TrimSpace(cfg.WebAuthnRPID)
	if rpID == "" {
		return nil, errors.New("WEBAUTHN_RP_ID is required")
	}

	origins := cfg.WebAuthnOrigins
	if len(origins) == 0 {
		origins = []string{"https://" + rpID}
	}

	wconfig := &webauthn.Config{
		RPDisplayName: lesserHostDomain,
		RPID:          rpID,
		RPOrigins:     origins,
	}

	return webauthn.New(wconfig)
}
