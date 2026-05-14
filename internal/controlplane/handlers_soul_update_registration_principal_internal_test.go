package controlplane

import (
	"crypto/ecdsa"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const soulUpdateRegistrationPrincipalDeclaredAtForTest = "2026-03-01T00:00:00Z"

func signSoulUpdateRegistrationPrincipalForTest(t *testing.T, key *ecdsa.PrivateKey, principalDeclaration string) string {
	t.Helper()

	principalDigest := crypto.Keccak256([]byte(principalDeclaration))
	principalSig, err := crypto.Sign(accounts.TextHash(principalDigest), key)
	if err != nil {
		t.Fatalf("principal sign: %v", err)
	}
	return "0x" + hex.EncodeToString(principalSig)
}

func signSoulUpdateRegistrationVerifiedPrincipalForTest(t *testing.T, s *Server, key *ecdsa.PrivateKey, agentIDHex string, wallet string, principalDeclaration string) string {
	t.Helper()

	principalDigest, appErr := s.computeSoulPrincipalDeclarationDigest(&models.SoulAgentRegistration{
		AgentID:          agentIDHex,
		Wallet:           wallet,
		DomainNormalized: "example.com",
		LocalID:          "agent-alice",
	}, wallet, principalDeclaration, soulUpdateRegistrationPrincipalDeclaredAtForTest)
	if appErr != nil {
		t.Fatalf("principal digest: %v", appErr)
	}
	principalSig, err := crypto.Sign(accounts.TextHash(principalDigest), key)
	if err != nil {
		t.Fatalf("verified principal sign: %v", err)
	}
	return "0x" + hex.EncodeToString(principalSig)
}

func applySoulUpdateRegistrationPrincipalForTest(identity *models.SoulAgentIdentity, wallet string, principalDeclaration string, principalSigHex string) {
	identity.PrincipalAddress = wallet
	identity.PrincipalDeclaration = principalDeclaration
	identity.PrincipalSignature = principalSigHex
	identity.PrincipalDeclaredAt = soulUpdateRegistrationPrincipalDeclaredAtForTest
}

func TestValidateSoulUpdateRegistrationPrincipalBinding(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{SoulChainID: 1, SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001"}}
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	verifiedSig := signSoulUpdateRegistrationVerifiedPrincipalForTest(t, s, key, soulLifecycleTestAgentIDHex, wallet, boundaryTestPrincipalDeclaration)
	principal := soul.PrincipalDeclarationV2{
		Identifier:  wallet,
		Declaration: boundaryTestPrincipalDeclaration,
		DeclaredAt:  soulUpdateRegistrationPrincipalDeclaredAtForTest,
	}
	identity := &models.SoulAgentIdentity{
		AgentID:              soulLifecycleTestAgentIDHex,
		Domain:               "example.com",
		LocalID:              "agent-alice",
		Wallet:               wallet,
		PrincipalAddress:     wallet,
		PrincipalDeclaration: boundaryTestPrincipalDeclaration,
		PrincipalSignature:   verifiedSig,
		PrincipalDeclaredAt:  soulUpdateRegistrationPrincipalDeclaredAtForTest,
	}

	if appErr := s.validateSoulUpdateRegistrationPrincipalBinding(identity, &soul.RegistrationFileV2{Principal: principal}, nil); appErr != nil {
		t.Fatalf("expected v2 binding to validate: %v", appErr)
	}
	if appErr := s.validateSoulUpdateRegistrationPrincipalBinding(identity, nil, &soul.RegistrationFileV3{Principal: principal}); appErr != nil {
		t.Fatalf("expected v3 binding to validate: %v", appErr)
	}
	if appErr := s.validateSoulUpdateRegistrationPrincipalBinding(identity, nil, nil); appErr != nil {
		t.Fatalf("expected legacy registration to skip principal binding: %v", appErr)
	}

	replayed := *identity
	replayed.PrincipalSignature = signSoulUpdateRegistrationPrincipalForTest(t, key, boundaryTestPrincipalDeclaration)
	if appErr := s.validateSoulUpdateRegistrationPrincipalBinding(&replayed, &soul.RegistrationFileV2{Principal: principal}, nil); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected replayed unbound principal signature conflict, got %#v", appErr)
	}

	mismatch := principal
	mismatch.Identifier = "0x0000000000000000000000000000000000000001"
	if appErr := s.validateSoulUpdateRegistrationPrincipalBinding(identity, &soul.RegistrationFileV2{Principal: mismatch}, nil); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected principal mismatch bad_request, got %#v", appErr)
	}

	missingBinding := &models.SoulAgentIdentity{PrincipalAddress: identity.PrincipalAddress}
	if appErr := s.validateSoulUpdateRegistrationPrincipalBinding(missingBinding, &soul.RegistrationFileV2{Principal: principal}, nil); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected missing verified principal conflict, got %#v", appErr)
	}
}
