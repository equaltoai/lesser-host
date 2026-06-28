package controlplane

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	soulInstanceBootstrapTestActor                = "instance:inst1"
	soulInstanceBootstrapTestInstanceSlug         = "inst1"
	soulInstanceBootstrapTestConversationMessage  = "hello"
	soulInstanceBootstrapTestPrincipalDeclaration = "I declare authority over this Lesser-hosted soul."
	soulInstanceBootstrapTestSigningMethodEIP191  = "eip191_personal_sign"
	soulInstanceBootstrapTestInvalidRegSig        = "invalid registration signature"
	soulInstanceBootstrapTestIdempotencyKey       = "idem-1"
)

func TestSoulInstanceBootstrapScaffold_RequiresStrictInstanceKey(t *testing.T) {
	t.Parallel()

	t.Run("missing bearer fails closed", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)

		_, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(nil, soulInstanceBootstrapDomainOnlyBody(testDomainExampleCom), nil))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulInstanceBootstrapCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized 401, got %#v", appErr)
		}
	})

	t.Run("unknown key hash fails closed", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()

		_, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer raw-key"}, soulInstanceBootstrapDomainOnlyBody(testDomainExampleCom), nil))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulInstanceBootstrapCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized 401, got %#v", appErr)
		}
		tdb.qKey.AssertCalled(t, "ConsistentRead")
		tdb.qKey.AssertNumberOfCalls(t, "First", 1)
	})

	t.Run("revoked key fails closed", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
			*dest = models.InstanceKey{
				ID:           sha256HexTrimmed("raw-key"),
				InstanceSlug: soulInstanceBootstrapTestInstanceSlug,
				CreatedAt:    time.Now().Add(-time.Hour).UTC(),
				RevokedAt:    time.Now().Add(-time.Minute).UTC(),
			}
		}).Once()

		_, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer raw-key"}, soulInstanceBootstrapDomainOnlyBody(testDomainExampleCom), nil))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulInstanceBootstrapCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized 401, got %#v", appErr)
		}
		tdb.qKey.AssertCalled(t, "ConsistentRead")
	})
}

func TestSoulInstanceBootstrapScaffold_RejectsCrossInstanceDomainBeforeBusinessHandler(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationInstanceDomain(t, tdb, testDomainExampleCom, "other-inst")

	_, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey}, soulInstanceBootstrapDomainOnlyBody(testDomainExampleCom), nil))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected boundary violation 403, got %#v", appErr)
	}
	if appErr.Details["field"] != "domain" || appErr.Details["reason"] != soulMintInstanceReadReasonTenantMismatch {
		t.Fatalf("expected tenant-domain mismatch details, got %#v", appErr.Details)
	}
}

func TestSoulInstanceAgentRegistrationBegin_ScopesWritesToInstanceKey(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, testDomainExampleCom, soulInstanceBootstrapTestInstanceSlug)
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(soulAgentRegistrationBeginRequest{
		Domain:       "Example.COM",
		LocalID:      provisionTestAgentLocalID,
		Wallet:       "0x000000000000000000000000000000000000dEaD",
		Capabilities: []any{"social"},
	})

	resp, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey}, body, nil))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulAgentRegistrationBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Registration.Username != soulInstanceBootstrapTestActor || out.Wallet == nil || out.Wallet.Username != soulInstanceBootstrapTestActor {
		t.Fatalf("expected instance actor on registration/wallet challenge, got %#v", out)
	}
	if out.Registration.DomainNormalized != testDomainExampleCom || out.Registration.LocalID != provisionTestAgentLocalID {
		t.Fatalf("expected normalized domain/local id, got %#v", out.Registration)
	}
	if !out.Registration.DNSVerified || !out.Registration.HTTPSVerified || len(out.Proofs) != 0 {
		t.Fatalf("expected instance-owned domain proofs to be auto-verified, got reg=%#v proofs=%#v", out.Registration, out.Proofs)
	}
	if out.Promotion == nil || out.Promotion.RequestedBy != soulInstanceBootstrapTestActor || out.Promotion.AgentID != out.Registration.AgentID {
		t.Fatalf("expected instance promotion snapshot, got %#v", out.Promotion)
	}
}

func TestSoulInstanceHostedInstanceTrustNoWalletBeginReservesAuthority(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, testDomainExampleCom, soulInstanceBootstrapTestInstanceSlug)
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Twice()

	body, _ := json.Marshal(soulAgentRegistrationBeginRequest{
		Domain:         "Example.COM",
		LocalID:        provisionTestAgentLocalID,
		AuthorityModel: models.SoulAuthorityModelInstanceTrust,
		Capabilities:   []any{"travel_planning"},
	})

	resp, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey}, body, nil))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulAgentRegistrationBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertSoulInstanceHostedBeginOmitsWallet(t, out, resp.Body)
	assertSoulInstanceHostedBeginRegistration(t, out.Registration)
	assertSoulInstanceHostedBeginPromotion(t, out.Promotion)
	tdb.qWalletAgent.AssertNotCalled(t, "CreateOrUpdate")
}

func assertSoulInstanceHostedBeginOmitsWallet(t *testing.T, out soulAgentRegistrationBeginResponse, body []byte) {
	t.Helper()
	if out.Wallet != nil || strings.Contains(string(body), `"wallet"`) || strings.Contains(string(body), `"wallet_address"`) {
		t.Fatalf("hosted instance-trust begin must not return wallet material: %s", string(body))
	}
}

func assertSoulInstanceHostedBeginRegistration(t *testing.T, reg models.SoulAgentRegistration) {
	t.Helper()
	if reg.AuthorityModel != models.SoulAuthorityModelInstanceTrust ||
		reg.Wallet != "" ||
		!reg.ExpiresAt.IsZero() ||
		!reg.DNSVerified ||
		!reg.HTTPSVerified {
		t.Fatalf("unexpected hosted registration: %#v", reg)
	}
}

func assertSoulInstanceHostedBeginPromotion(t *testing.T, promotion *soulAgentPromotionView) {
	t.Helper()
	if promotion == nil ||
		promotion.AuthorityModel != models.SoulAuthorityModelInstanceTrust ||
		promotion.AnchorState != models.SoulAnchorStateHostedOffchain ||
		promotion.ReadinessStatus != models.SoulAgentPromotionReadinessReadyForConversation {
		t.Fatalf("unexpected hosted promotion: %#v", promotion)
	}
}

func TestSoulInstanceHostedInstanceTrustNoWalletBeginReplaysPendingAuthority(t *testing.T) {
	t.Parallel()

	reg, identity, promotion := soulInstanceHostedInstanceTrustFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, identity, nil)
	tdb.qPromotion.ExpectedCalls = nil
	addStandardMockQueryStubs(tdb.qPromotion)
	tdb.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentPromotion](t, args, 0)
		*dest = promotion
	}).Once()
	stubMintConversationRegistration(t, tdb, reg)

	body, _ := json.Marshal(soulAgentRegistrationBeginRequest{
		Domain:       strings.ToUpper(reg.DomainNormalized),
		LocalID:      reg.LocalID,
		Capabilities: []any{"travel_planning"},
	})

	resp, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey}, body, nil))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var out soulAgentRegistrationBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Registration.ID != reg.ID || out.Registration.AgentID != reg.AgentID || out.Wallet != nil {
		t.Fatalf("expected idempotent hosted replay, got %#v", out)
	}
	tdb.qReg.AssertNotCalled(t, "Create")
	tdb.qIdentity.AssertNotCalled(t, "Create")
}

func TestSoulInstanceBootstrapScaffold_RejectsCrossInstanceRegistration(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationInstanceDomain(t, tdb, reg.DomainNormalized, "other-inst")

	_, err := s.handleSoulInstanceAgentRegistrationPrincipalDeclarationPreflight(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		[]byte(`{"principal_address":"0x0000000000000000000000000000000000000001","principal_declaration":"I declare authority.","declared_at":"2026-03-05T12:00:00Z"}`),
		map[string]string{"id": reg.ID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected boundary violation 403, got %#v", appErr)
	}
	if appErr.Details["reason"] != soulMintInstanceReadReasonTenantMismatch {
		t.Fatalf("expected tenant-domain mismatch details, got %#v", appErr.Details)
	}
}

func TestSoulInstanceMintConversationRoutes_RequireStrictInstanceKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		call   func(*Server, *apptheory.Context) (*apptheory.Response, error)
		params map[string]string
	}{
		{
			name: "send",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceMintConversation(ctx)
			},
			params: map[string]string{"id": "reg-1"},
		},
		{
			name: "get",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceGetRegistrationMintConversation(ctx)
			},
			params: map[string]string{"id": "reg-1", "conversationId": mintConversationTestConversationID},
		},
		{
			name: "complete",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceCompleteMintConversation(ctx)
			},
			params: map[string]string{"id": "reg-1", "conversationId": mintConversationTestConversationID},
		},
		{
			name: "finalize preflight",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceFinalizeMintConversationPreflight(ctx)
			},
			params: map[string]string{"id": "reg-1", "conversationId": mintConversationTestConversationID},
		},
		{
			name: "finalize begin",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceBeginFinalizeMintConversation(ctx)
			},
			params: map[string]string{"id": "reg-1", "conversationId": mintConversationTestConversationID},
		},
		{
			name: "finalize",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceFinalizeMintConversation(ctx)
			},
			params: map[string]string{"id": "reg-1", "conversationId": mintConversationTestConversationID},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("missing bearer "+tc.name, func(t *testing.T) {
			t.Parallel()

			tdb := newMintConversationTestDB()
			s := newMintConversationServer(tdb)
			_, err := tc.call(s, newSoulInstanceBootstrapContext(nil, nil, tc.params))
			appErr := requireAppTheoryError(t, err)
			if appErr.Code != soulInstanceBootstrapCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
				t.Fatalf("expected unauthorized 401, got %#v", appErr)
			}
			tdb.qKey.AssertNotCalled(t, "First")
		})
	}

	t.Run("unknown key hash fails closed", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()

		_, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
			map[string]string{"authorization": "Bearer raw-key"},
			mustMarshalJSON(t, soulMintConversationRequest{Message: soulInstanceBootstrapTestConversationMessage}),
			map[string]string{"id": "reg-1"},
		))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulInstanceBootstrapCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized 401, got %#v", appErr)
		}
		tdb.qKey.AssertCalled(t, "ConsistentRead")
		tdb.qKey.AssertNumberOfCalls(t, "First", 1)
	})

	t.Run("revoked key fails closed", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
			*dest = models.InstanceKey{
				ID:           sha256HexTrimmed("raw-key"),
				InstanceSlug: soulInstanceBootstrapTestInstanceSlug,
				CreatedAt:    time.Now().Add(-time.Hour).UTC(),
				RevokedAt:    time.Now().Add(-time.Minute).UTC(),
			}
		}).Once()

		_, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
			map[string]string{"authorization": "Bearer raw-key"},
			nil,
			map[string]string{"id": "reg-1", "conversationId": mintConversationTestConversationID},
		))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulInstanceBootstrapCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized 401, got %#v", appErr)
		}
		tdb.qKey.AssertCalled(t, "ConsistentRead")
	})
}

func TestSoulInstanceMintConversation_RejectsCrossInstanceRegistration(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationInstanceDomain(t, tdb, reg.DomainNormalized, "other-inst")

	_, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{Message: soulInstanceBootstrapTestConversationMessage}),
		map[string]string{"id": reg.ID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected boundary violation 403, got %#v", appErr)
	}
	if appErr.Details["reason"] != soulMintInstanceReadReasonTenantMismatch {
		t.Fatalf("expected tenant-domain mismatch details, got %#v", appErr.Details)
	}
}

func TestSoulInstanceAgentRegistrationPrincipalDeclarationPreflight_MatchesCanonicalMaterial(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := soulInstanceBootstrapTestRegistration("reg-preflight", "0x00000000000000000000000000000000000000aa", "wallet message")
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

	principalDeclaration := soulInstanceBootstrapTestPrincipalDeclaration
	declaredAt := canonicalSoulSignedTimestamp(time.Now().UTC())
	body, _ := json.Marshal(soulAgentRegistrationPrincipalDeclarationPreflightRequest{
		PrincipalAddress:     "0x00000000000000000000000000000000000000AA",
		PrincipalDeclaration: principalDeclaration,
		DeclaredAt:           declaredAt,
	})

	resp, err := s.handleSoulInstanceAgentRegistrationPrincipalDeclarationPreflight(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		body,
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulAgentRegistrationPrincipalDeclarationPreflightResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	material, appErr := s.computeSoulPrincipalDeclarationSigningMaterial(&reg, out.PrincipalAddress, principalDeclaration, declaredAt)
	if appErr != nil {
		t.Fatalf("canonical material: %#v", appErr)
	}
	wantDigest := "0x" + hex.EncodeToString(material.digest)
	if out.DigestHex != wantDigest || out.MessageHex != wantDigest || out.CanonicalJSON != material.canonicalJSON {
		t.Fatalf("preflight did not return canonical material: got=%#v wantDigest=%s wantJSON=%s", out, wantDigest, material.canonicalJSON)
	}
	if out.SigningMethod != soulInstanceBootstrapTestSigningMethodEIP191 || out.MessageEncoding != "hex_bytes" {
		t.Fatalf("unexpected signing metadata: %#v", out)
	}
}

func TestSoulInstanceAgentRegistrationVerify_CreatesOperationAndPromotion(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.cfg.SoulMintSignerKey = strings.Repeat("ab", 32)
	s.cfg.WebAuthnRPID = "lesser.host"

	walletMessage := "soul instance registration wallet message"
	walletKey, walletAddr, walletSigHex := soulInstanceBootstrapSignedWallet(t, walletMessage)
	reg := soulInstanceBootstrapTestRegistration("reg-verify", walletAddr, walletMessage)

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

	principalDeclaration := soulInstanceBootstrapTestPrincipalDeclaration
	declaredAt := canonicalSoulSignedTimestamp(time.Now().UTC())
	principalSigHex := signSoulRegistrationVerifyPrincipalForTest(t, s, walletKey, &reg, walletAddr, principalDeclaration, declaredAt)
	body, _ := json.Marshal(soulAgentRegistrationVerifyRequest{
		Signature:            walletSigHex,
		PrincipalAddress:     walletAddr,
		PrincipalDeclaration: principalDeclaration,
		PrincipalSignature:   principalSigHex,
		DeclaredAt:           declaredAt,
	})

	resp, err := s.handleSoulInstanceAgentRegistrationVerify(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		body,
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	requireSoulInstanceVerifyCreatedResponse(t, resp.Body, walletAddr)
}

func TestSoulInstanceAgentRegistrationVerify_ReplaysCompletedRegistrationWithoutDuplicateOperation(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)

	walletMessage := "soul instance registration wallet message"
	walletKey, walletAddr, walletSigHex := soulInstanceBootstrapSignedWallet(t, walletMessage)
	reg := soulInstanceBootstrapTestRegistration("reg-replay", walletAddr, walletMessage)
	reg.Status = models.SoulAgentRegistrationStatusCompleted
	reg.WalletVerified = true
	reg.CompletedAt = time.Date(2026, 3, 5, 12, 5, 0, 0, time.UTC)
	reg.VerifiedAt = reg.CompletedAt
	reg.ExpiresAt = time.Now().Add(-time.Hour).UTC()

	promotion := updateSoulAgentPromotionForVerification(buildSoulAgentPromotionFromRegistration(&reg, reg.CompletedAt), &reg, &models.SoulOperation{
		OperationID: "op-existing",
		Status:      models.SoulOperationStatusPending,
	}, walletAddr, reg.CompletedAt)
	existingOp := models.SoulOperation{
		OperationID:     "op-existing",
		Kind:            models.SoulOperationKindMint,
		AgentID:         reg.AgentID,
		Status:          models.SoulOperationStatusPending,
		SafePayloadJSON: `{"safe_address":"","to":"0x0000000000000000000000000000000000000001","value":"500000000000000","data":"0x1234"}`,
	}
	_ = existingOp.UpdateKeys()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qPromotion.ExpectedCalls = nil
	tdb.qPromotion.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(tdb.qPromotion).Maybe()
	tdb.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentPromotion](t, args, 0)
		*dest = *promotion
	}).Once()
	tdb.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = existingOp
	}).Once()

	principalDeclaration := soulInstanceBootstrapTestPrincipalDeclaration
	declaredAt := canonicalSoulSignedTimestamp(time.Now().UTC())
	principalSigHex := signSoulRegistrationVerifyPrincipalForTest(t, s, walletKey, &reg, walletAddr, principalDeclaration, declaredAt)
	body, _ := json.Marshal(soulAgentRegistrationVerifyRequest{
		Signature:            walletSigHex,
		PrincipalAddress:     walletAddr,
		PrincipalDeclaration: principalDeclaration,
		PrincipalSignature:   principalSigHex,
		DeclaredAt:           declaredAt,
	})

	resp, err := s.handleSoulInstanceAgentRegistrationVerify(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		body,
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulAgentRegistrationVerifyResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Registration.CompletedAt != reg.CompletedAt || out.Operation.OperationID != existingOp.OperationID {
		t.Fatalf("expected stable completed registration/operation, got %#v", out)
	}
	if out.SafeTx == nil || out.SafeTx.Data != "0x1234" {
		t.Fatalf("expected stored safe tx payload, got %#v", out.SafeTx)
	}
	tdb.qOp.AssertNotCalled(t, "Create")
}

func TestSoulInstanceCompletedReplayHelpers(t *testing.T) {
	t.Parallel()

	t.Run("fallback creates deterministic operation without lifecycle duplicate", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		s.cfg.SoulMintSignerKey = strings.Repeat("ab", 32)
		s.cfg.WebAuthnRPID = "lesser.host"

		reg := soulInstanceBootstrapTestRegistration("reg-replay-fallback", "0x00000000000000000000000000000000000000aa", "message")
		reg.Status = models.SoulAgentRegistrationStatusCompleted
		reg.CompletedAt = time.Now().UTC()
		resp, appErr := s.replaySoulInstanceCompletedRegistration(
			&apptheory.Context{},
			&reg,
			"0x00000000000000000000000000000000000000aa",
			"0xprincipal",
			soulInstanceBootstrapTestPrincipalDeclaration,
			canonicalSoulSignedTimestamp(time.Now().UTC()),
		)
		if appErr != nil {
			t.Fatalf("unexpected replay err: %#v", appErr)
		}
		if resp.Operation.OperationID == "" || resp.SafeTx == nil || resp.Promotion == nil || resp.Promotion.MintOperationID != resp.Operation.OperationID {
			t.Fatalf("expected fallback operation and promotion view, got %#v", resp)
		}
		tdb.qLifecycle.AssertNotCalled(t, "Create")
	})

	t.Run("principal mismatch fails closed", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		reg := soulInstanceBootstrapTestRegistration("reg-replay-mismatch", "0x00000000000000000000000000000000000000aa", "message")
		reg.Status = models.SoulAgentRegistrationStatusCompleted
		tdb.qPromotion.ExpectedCalls = nil
		tdb.qPromotion.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(tdb.qPromotion).Maybe()
		tdb.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentPromotion](t, args, 0)
			*dest = models.SoulAgentPromotion{AgentID: reg.AgentID, PrincipalAddress: "0x00000000000000000000000000000000000000bb"}
		}).Once()

		_, appErr := s.replaySoulInstanceCompletedRegistration(
			&apptheory.Context{},
			&reg,
			"0x00000000000000000000000000000000000000aa",
			"0xprincipal",
			soulInstanceBootstrapTestPrincipalDeclaration,
			canonicalSoulSignedTimestamp(time.Now().UTC()),
		)
		if appErr == nil || appErr.Code != soulInstanceBootstrapCodeBoundaryViolation {
			t.Fatalf("expected principal mismatch boundary error, got %#v", appErr)
		}
		tdb.qOp.AssertNotCalled(t, "Create")
	})
}

func TestSoulInstanceBootstrapScaffold_ConversationIdsCannotCrossRegistration(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(theoryErrors.ErrItemNotFound).Once()

	_, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": "conv-from-other-registration"},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeNotFound || appErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected conversation not found within registration boundary, got %#v", appErr)
	}
}

func TestSoulInstanceFinalizeMintConversation_ConversationIdsCannotCrossRegistration(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(theoryErrors.ErrItemNotFound).Once()

	_, err := s.handleSoulInstanceBeginFinalizeMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: map[string]string{"b1": "0x00"}}),
		map[string]string{"id": reg.ID, "conversationId": "conv-from-other-registration"},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeNotFound || appErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected conversation not found within registration boundary, got %#v", appErr)
	}
}

func TestSoulInstanceMintConversation_StartReturnsJSONWithoutQueueAuthority(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	stubHostedGenesisAssistantRunner(t, s, "assistant reply", nil)
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("user-visible hosted genesis start must not enqueue SQS authority: %#v", msg)
		return nil
	}

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	tdb.qMintIdem.On("First", mock.AnythingOfType("*models.SoulMintConversationIdempotency")).Return(theoryErrors.ErrItemNotFound).Once()
	expectSoulInstanceMintConversationDebit(t, tdb, reg.AgentID, true)
	expectSoulInstanceMintConversationProgression(t, tdb, hostedgenesis.StatusAssistantTurnReady)

	resp, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{Model: "anthropic:claude-sonnet-4-6", Message: soulInstanceBootstrapTestConversationMessage, IdempotencyKey: soulInstanceBootstrapTestIdempotencyKey, CorrelationID: "corr-1"}),
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	out := assertSoulInstanceMintConversationAcceptedResponse(t, resp)
	if out.Conversation.RegistrationID != reg.ID {
		t.Fatalf("expected durable session authority for registration %s, got %#v", reg.ID, out.Conversation)
	}
	tdb.qLifecycle.AssertCalled(t, "Create")
}

func TestSoulInstanceMintConversation_AssistantFailurePersistsTypedFailure(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	stubHostedGenesisAssistantRunner(t, s, "", errors.New("provider unavailable"))
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("failed hosted genesis turn must not enqueue SQS authority: %#v", msg)
		return nil
	}

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	tdb.qMintIdem.On("First", mock.AnythingOfType("*models.SoulMintConversationIdempotency")).Return(theoryErrors.ErrItemNotFound).Once()
	expectSoulInstanceMintConversationDebit(t, tdb, reg.AgentID, true)
	expectSoulInstanceMintConversationProgression(t, tdb, hostedgenesis.StatusFailed)

	resp, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{Model: "anthropic:claude-sonnet-4-6", Message: soulInstanceBootstrapTestConversationMessage, IdempotencyKey: soulInstanceBootstrapTestIdempotencyKey, CorrelationID: "corr-1"}),
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected JSON 200 failed response, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusFailed || out.Conversation.Failure == nil {
		t.Fatalf("expected typed failed status, got %#v", out.Conversation)
	}
	if out.Conversation.Failure.Code != hostedGenesisFailureAssistantTurnFailed || out.Conversation.Failure.Recovery.Action != hostedGenesisRecoveryRetrySameStep {
		t.Fatalf("expected retryable assistant failure, got %#v", out.Conversation.Failure)
	}
	if strings.Contains(string(resp.Body), soulInstanceBootstrapTestConversationMessage) ||
		strings.Contains(string(resp.Body), mintConversationInstanceReadTestRawKey) {
		t.Fatalf("failure response leaked transcript or credential material: %s", string(resp.Body))
	}
}

func TestSoulInstanceMintConversation_IdempotentRetryDoesNotDebitOrAppend(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	stubHostedGenesisAssistantRunner(t, s, "assistant retry reply", nil)
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("idempotent replay must not enqueue duplicate user-visible execution: %#v", msg)
		return nil
	}
	idemKey := "idem-retry-1"
	reqHash := hostedGenesisRequestHash(reg.ID, "", "anthropic:claude-sonnet-4-6", soulInstanceBootstrapTestConversationMessage)

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	tdb.qMintIdem.On("First", mock.AnythingOfType("*models.SoulMintConversationIdempotency")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulMintConversationIdempotency](t, args, 0)
		*dest = models.SoulMintConversationIdempotency{
			InstanceSlug:   soulInstanceBootstrapTestInstanceSlug,
			RegistrationID: reg.ID,
			AgentID:        reg.AgentID,
			ConversationID: mintConversationTestConversationID,
			TurnID:         "turn-existing",
			IdempotencyKey: idemKey,
			RequestHash:    reqHash,
			RequestID:      "req-original",
			CorrelationID:  "corr-original",
			Status:         models.SoulMintConversationIdempotencyStatusProcessing,
		}
	}).Once()
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"hello"}]`),
		Status:         models.SoulMintConversationStatusInProgress,
		LatestTurnID:   "turn-existing",
		RequestID:      "req-original",
		CorrelationID:  "corr-original",
		IdempotencyKey: idemKey,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	session := hostedGenesisSessionFromLegacyConversationForTest(tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Status:         models.SoulMintConversationStatusInProgress,
		LatestTurnID:   "turn-existing",
		RequestID:      "req-original",
		CorrelationID:  "corr-original",
		IdempotencyKey: idemKey,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	session.TurnLedger[0].RequestHash = reqHash
	tdb.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		*dest = session
	}).Once()
	expectSoulInstanceMintConversationProgression(t, tdb, hostedgenesis.StatusAssistantTurnReady)

	resp, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{ConversationID: mintConversationTestConversationID, Model: "anthropic:claude-sonnet-4-6", Message: soulInstanceBootstrapTestConversationMessage, IdempotencyKey: idemKey, CorrelationID: "corr-retry"}),
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200 progressed replay, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.ConversationID != mintConversationTestConversationID ||
		out.Conversation.Status != models.SoulMintConversationStatusAssistantTurnReady ||
		out.Conversation.MessageCount != 2 ||
		out.Conversation.TraceIDs.IdempotencyKey != idemKey {
		t.Fatalf("expected idempotent progression without duplicate user message, got %#v", out)
	}
	tdb.db.AssertNumberOfCalls(t, "TransactWrite", 1)
	tdb.qBudget.AssertNumberOfCalls(t, "First", 0)
}

func TestSoulInstanceMintConversation_ContinueRejectsModelChange(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Status:         models.SoulMintConversationStatusInProgress,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})

	_, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{ConversationID: mintConversationTestConversationID, Model: "openai:gpt-5.4", Message: soulInstanceBootstrapTestConversationMessage}),
		map[string]string{"id": reg.ID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeConflict || appErr.StatusCode != http.StatusConflict || appErr.Message != "cannot change model for an existing conversation" {
		t.Fatalf("expected model-change conflict 409, got %#v", appErr)
	}
}

func TestSoulInstanceMintConversation_ContinueUsesStoredModelAndMessages(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	stubHostedGenesisAssistantRunner(t, s, "second assistant", nil)
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("continued user-visible hosted genesis turn must not enqueue SQS authority: %#v", msg)
		return nil
	}

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"first"},{"role":"assistant","content":"reply"}]`),
		Status:         models.SoulMintConversationStatusAssistantTurnReady,
		Usage:          models.AIUsage{TotalTokens: 9},
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	expectSoulInstanceMintConversationDebit(t, tdb, reg.AgentID, false)
	expectSoulInstanceMintConversationProgression(t, tdb, hostedgenesis.StatusAssistantTurnReady)

	resp, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{ConversationID: mintConversationTestConversationID, Message: "second"}),
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.ConversationID != mintConversationTestConversationID || out.Conversation.Status != models.SoulMintConversationStatusAssistantTurnReady || out.Conversation.MessageCount != 4 {
		t.Fatalf("expected progressed assistant turn, got %#v", out)
	}
}

func TestSoulInstanceGetRegistrationMintConversation_ReturnsStatusEnvelopeWithoutRawTranscript(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:              reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Model:                "anthropic:claude-sonnet-4-6",
		Messages:             encodeMintConversationBlob(`[{"role":"user","content":"private transcript"}]`),
		ProducedDeclarations: encodeMintConversationBlob(`{"private":true}`),
		Status:               models.SoulMintConversationStatusCompleted,
		CreatedAt:            time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})

	resp, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", resp.Status, string(resp.Body))
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Version != "1" || out.Conversation.ConversationID != mintConversationTestConversationID || out.Conversation.Status != models.SoulMintConversationStatusFailed || out.Conversation.Failure == nil || out.Conversation.Failure.Code != hostedGenesisFailureInvalidProducedDeclarations {
		t.Fatalf("expected compact failed status for invalid legacy declarations, got %#v", out)
	}
	body := string(resp.Body)
	if strings.Contains(body, "private transcript") || strings.Contains(body, `"private":true`) || strings.Contains(body, mintConversationInstanceReadTestRawKey) {
		t.Fatalf("status envelope leaked private transcript/declarations/credential: %s", body)
	}
}

func TestSoulInstanceGetRegistrationMintConversation_RejectsInvalidConversationID(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)

	_, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": "bad/conversation"},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulMintInstanceReadCodeInvalidRequest || appErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid conversation id 400, got %#v", appErr)
	}
	if appErr.Details["field"] != soulMintInstanceReadFieldConversationID {
		t.Fatalf("expected conversationId details, got %#v", appErr.Details)
	}
}

func TestSoulInstanceCompleteMintConversation_PersistsDeclarationsAndFinalizeReady(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe yourself"},{"role":"assistant","content":"done"}]`),
		Status:         models.SoulMintConversationStatusAssistantTurnReady,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	expectSoulInstanceMintConversationCompletionWrite(t, tdb)

	resp, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, map[string]any{"declarations": testMintConversationDecl()}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", resp.Status, string(resp.Body))
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusDeclarationReady || out.Conversation.ProducedDeclarations == nil {
		t.Fatalf("expected completed conversation with declarations, got %#v", out)
	}
	body := string(resp.Body)
	if strings.Contains(body, "describe yourself") || strings.Contains(body, "done") || strings.Contains(body, mintConversationInstanceReadTestRawKey) {
		t.Fatalf("instance completion leaked transcript or credential material: %s", body)
	}
	tdb.qLifecycle.AssertCalled(t, "Create")
}

func TestSoulInstanceCompleteMintConversation_AssistantReadyWithoutDeclarationsDoesNotQueueExtraction(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("user-visible declaration extraction handoff must not enqueue SQS authority: %#v", msg)
		return nil
	}
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe yourself"},{"role":"assistant","content":"ready"}]`),
		Status:         models.SoulMintConversationStatusAssistantTurnReady,
		LatestTurnID:   "turn-ready",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	expectSoulInstanceMintConversationExtractionDebit(t, tdb)

	resp, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusDeclarationExtractionPending || out.Conversation.ProducedDeclarations != nil {
		t.Fatalf("expected declaration extraction progress without terminal declarations, got %#v", out)
	}
}

func TestSoulInstanceCompleteMintConversation_ReturnsCompletedConversationReplay(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	identity := testMintConversationIdentity()
	identity.AgentID = reg.AgentID
	identity.SelfDescriptionVersion = 1
	declBytes := mustMarshalJSON(t, testMintConversationDecl())
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:              reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Model:                "anthropic:claude-sonnet-4-6",
		ProducedDeclarations: encodeMintConversationBlob(string(declBytes)),
		Status:               models.SoulMintConversationStatusCompleted,
		CreatedAt:            time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		CompletedAt:          time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC),
	})
	stubMintConversationIdentity(t, tdb, identity, nil)

	resp, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", resp.Status, string(resp.Body))
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusDeclarationReady || out.Conversation.ProducedDeclarations == nil || out.Conversation.ProducedDeclarations.DeclarationHash == "" {
		t.Fatalf("expected stored completed declarations, got %#v", out)
	}
	if strings.Contains(string(resp.Body), `"messages"`) || strings.Contains(string(resp.Body), mintConversationInstanceReadTestRawKey) {
		t.Fatalf("instance completion replay leaked private fields: %s", string(resp.Body))
	}
	tdb.qConv.AssertNumberOfCalls(t, "Update", 0)
	tdb.qLifecycle.AssertNumberOfCalls(t, "Create", 0)
}

func TestSoulInstanceCompleteMintConversation_ReplaysHostedContractEmptyDeclarationArrays(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	identity := testMintConversationIdentity()
	identity.AgentID = reg.AgentID
	identity.SelfDescriptionVersion = 1
	decl := testMintConversationDecl()
	decl.Capabilities = []soul.CapabilityV2{}
	decl.Boundaries = []soul.BoundaryV2{}
	decl.Transparency = map[string]any{}
	declBytes := mustMarshalJSON(t, decl)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:              reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Model:                "anthropic:claude-sonnet-4-6",
		ProducedDeclarations: encodeMintConversationBlob(string(declBytes)),
		Status:               models.SoulMintConversationStatusCompleted,
		CreatedAt:            time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		CompletedAt:          time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC),
	})
	stubMintConversationIdentity(t, tdb, identity, nil)

	resp, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", resp.Status, string(resp.Body))
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusDeclarationReady || out.Conversation.ProducedDeclarations == nil || len(out.Conversation.ProducedDeclarations.Declarations.Capabilities) != 0 || len(out.Conversation.ProducedDeclarations.Declarations.Boundaries) != 0 {
		t.Fatalf("expected stored hosted declarations with empty arrays, got %#v", out)
	}
	tdb.qConv.AssertNumberOfCalls(t, "Update", 0)
	tdb.qLifecycle.AssertNumberOfCalls(t, "Create", 0)
}

func TestSoulInstanceCompleteMintConversation_RejectsTerminalInvalidStates(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                  string
		legacyStatus          string
		expectedSessionStatus string
		reason                string
	}{
		{
			name:                  "completed without declarations migrates fail closed",
			legacyStatus:          models.SoulMintConversationStatusCompleted,
			expectedSessionStatus: models.SoulMintConversationStatusFailed,
			reason:                soulMintConversationCompleteReasonMissingDeclarations,
		},
		{
			name:                  "failed remains fail closed with state details",
			legacyStatus:          models.SoulMintConversationStatusFailed,
			expectedSessionStatus: models.SoulMintConversationStatusFailed,
			reason:                soulMintConversationCompleteReasonInvalidState,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertSoulInstanceCompleteTerminalConflict(t, tc.legacyStatus, tc.expectedSessionStatus, tc.reason)
		})
	}
}

func TestSoulInstanceCompleteMintConversation_ReturnsProgressWithoutAssistantTurn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		messages string
	}{
		{name: "no messages", messages: ""},
		{name: "user only", messages: `[{"role":"user","content":"describe yourself"}]`},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertSoulInstanceCompleteProgressWithoutAssistant(t, tc.messages)
		})
	}
}

func TestSoulInstanceCompleteMintConversation_RejectsAlreadyPublishedAgent(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	identity := testMintConversationIdentity()
	identity.AgentID = reg.AgentID
	identity.SelfDescriptionVersion = 1

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         models.SoulMintConversationStatusInProgress,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	stubMintConversationIdentity(t, tdb, identity, nil)

	_, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, map[string]any{"declarations": testMintConversationDecl()}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeConflict || appErr.StatusCode != http.StatusConflict || appErr.Message != soulMintConversationAlreadyPublishedMessage {
		t.Fatalf("expected already-published conflict, got %#v", appErr)
	}
}

func TestSoulInstanceFinalizeMintConversation_BeginAndPreflightReturnCanonicalSigningMaterial(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		call func(*Server, *apptheory.Context) (*apptheory.Response, error)
	}{
		{
			name: "begin",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceBeginFinalizeMintConversation(ctx)
			},
		},
		{
			name: "preflight alias",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceFinalizeMintConversationPreflight(ctx)
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tdb := newMintConversationTestDB()
			s := newMintConversationServer(tdb)
			reg, identity, _, declBytes, boundarySigs := soulInstanceFinalizeFixture(t)
			stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, models.SoulAgentMintConversation{
				AgentID:              reg.AgentID,
				ConversationID:       mintConversationTestConversationID,
				Status:               models.SoulMintConversationStatusCompleted,
				ProducedDeclarations: string(declBytes),
				CreatedAt:            time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
			})

			resp, err := tc.call(s, newSoulInstanceBootstrapContext(
				map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
				mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: boundarySigs}),
				map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
			))
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			var out soulMintConversationFinalizeBeginResponse
			if err := json.Unmarshal(resp.Body, &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			assertSoulInstanceFinalizeSigningMaterial(t, out)
		})
	}
}

func TestSoulInstanceHostedInstanceTrustCompleteAcceptsDeclarations(t *testing.T) {
	t.Parallel()

	reg, identity, _ := soulInstanceHostedInstanceTrustFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         models.SoulMintConversationStatusAssistantTurnReady,
		Messages:       encodeMintConversationBlob(mintConversationDurableAssistantMessagesJSON()),
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	stubMintConversationIdentity(t, tdb, identity, nil)
	expectSoulInstanceMintConversationCompletionWrite(t, tdb)

	resp, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, map[string]any{"declarations": testMintConversationDecl()}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", resp.Status, string(resp.Body))
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusDeclarationReady || out.Conversation.ProducedDeclarations == nil {
		t.Fatalf("expected completed hosted conversation, got %#v", out)
	}
	if strings.Contains(string(resp.Body), `"messages"`) || strings.Contains(string(resp.Body), "describe yourself") || strings.Contains(string(resp.Body), "done") || strings.Contains(string(resp.Body), mintConversationInstanceReadTestRawKey) {
		t.Fatalf("hosted instance-trust complete leaked private fields: %s", string(resp.Body))
	}
}

func TestSoulInstanceHostedInstanceTrustFinalizePreflightOmitsWalletSignatures(t *testing.T) {
	t.Parallel()

	reg, identity, completedConv := soulInstanceHostedFinalizeFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("finalize preflight must use HostedGenesisSession gate, not SQS: %#v", msg)
		return nil
	}
	stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, completedConv)

	resp, err := s.handleSoulInstanceBeginFinalizeMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, map[string]any{}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var out soulMintConversationFinalizeBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertSoulInstanceHostedInstanceTrustFinalizePreflight(t, out)
}

func TestSoulInstanceHostedInstanceTrustFinalizePublishesHostedOffchain(t *testing.T) {
	t.Parallel()

	reg, identity, completedConv := soulInstanceHostedFinalizeFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, completedConv)
	expectSoulInstanceFinalizePublishWrites(t, tdb)

	resp, err := s.handleSoulInstanceFinalizeMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, map[string]any{}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var out soulMintConversationFinalizeResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertSoulInstanceHostedInstanceTrustFinalizeResponse(t, out)

	packs, ok := s.soulPacks.(*fakeSoulPackStoreForPublish)
	if !ok {
		t.Fatalf("unexpected soul pack store: %T", s.soulPacks)
	}
	published := packs.puts[soulRegistrationS3Key(reg.AgentID)]
	assertSoulInstanceHostedInstanceTrustRegistrationFile(t, published)
}

func TestSoulInstanceFinalizeMintConversation_RejectsInvalidStates(t *testing.T) {
	t.Parallel()
	assertSoulInstanceFinalizeRejectsNotCompleted(t)
	assertSoulInstanceFinalizeRejectsMissingDeclarations(t)
	assertSoulInstanceFinalizeRejectsMissingPrincipal(t)
	assertSoulInstanceFinalizeRejectsBadBoundarySignature(t)
	assertSoulInstanceFinalizeRejectsBadSelfAttestation(t)
	assertSoulInstanceFinalizeRejectsVersionMismatch(t)
}

type soulInstanceFinalizeCall struct {
	name string
	call func(*Server, *apptheory.Context) (*apptheory.Response, error)
}

func soulInstanceFinalizeStateCalls() []soulInstanceFinalizeCall {
	return []soulInstanceFinalizeCall{
		{
			name: "begin",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceBeginFinalizeMintConversation(ctx)
			},
		},
		{
			name: "preflight",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceFinalizeMintConversationPreflight(ctx)
			},
		},
		{
			name: "finalize",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceFinalizeMintConversation(ctx)
			},
		},
	}
}

func assertSoulInstanceFinalizeRejectsNotCompleted(t *testing.T) {
	t.Helper()
	for _, tc := range soulInstanceFinalizeStateCalls() {
		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		reg, identity, _, _, boundarySigs := soulInstanceFinalizeFixture(t)
		stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, models.SoulAgentMintConversation{
			AgentID:        reg.AgentID,
			ConversationID: mintConversationTestConversationID,
			Status:         models.SoulMintConversationStatusInProgress,
		})
		_, err := tc.call(s, newSoulInstanceBootstrapContext(
			map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
			mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: boundarySigs}),
			map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
		))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulInstanceBootstrapCodeConflict || appErr.StatusCode != http.StatusConflict || appErr.Message != "conversation is not completed" {
			t.Fatalf("%s: expected completed-state conflict, got %#v", tc.name, appErr)
		}
	}
}

func assertSoulInstanceFinalizeRejectsMissingDeclarations(t *testing.T) {
	t.Helper()
	for _, tc := range soulInstanceFinalizeStateCalls() {
		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		reg, identity, _, _, boundarySigs := soulInstanceFinalizeFixture(t)
		stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, models.SoulAgentMintConversation{
			AgentID:        reg.AgentID,
			ConversationID: mintConversationTestConversationID,
			Status:         models.SoulMintConversationStatusCompleted,
		})
		_, err := tc.call(s, newSoulInstanceBootstrapContext(
			map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
			mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: boundarySigs}),
			map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
		))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulInstanceBootstrapCodeConflict || appErr.StatusCode != http.StatusConflict || appErr.Message != "conversation has no produced declarations" {
			t.Fatalf("%s: expected missing-declarations conflict, got %#v", tc.name, appErr)
		}
	}
}

func assertSoulInstanceFinalizeRejectsMissingPrincipal(t *testing.T) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg, identity, _, declBytes, boundarySigs := soulInstanceFinalizeFixture(t)
	identity.PrincipalDeclaration = ""
	stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, models.SoulAgentMintConversation{
		AgentID:              reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Status:               models.SoulMintConversationStatusCompleted,
		ProducedDeclarations: string(declBytes),
	})
	_, err := s.handleSoulInstanceBeginFinalizeMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: boundarySigs}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeConflict || appErr.Message != "principal declaration is missing; re-verify registration" {
		t.Fatalf("expected missing-principal conflict, got %#v", appErr)
	}
}

func assertSoulInstanceFinalizeRejectsBadBoundarySignature(t *testing.T) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg, identity, _, declBytes, _ := soulInstanceFinalizeFixture(t)
	stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, models.SoulAgentMintConversation{
		AgentID:              reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Status:               models.SoulMintConversationStatusCompleted,
		ProducedDeclarations: string(declBytes),
	})
	_, err := s.handleSoulInstanceBeginFinalizeMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: map[string]string{"b1": "0x00"}}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeInvalidRequest || !strings.Contains(appErr.Message, "invalid boundary signature") {
		t.Fatalf("expected boundary signature rejection, got %#v", appErr)
	}
}

func assertSoulInstanceFinalizeRejectsBadSelfAttestation(t *testing.T) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg, identity, _, declBytes, boundarySigs := soulInstanceFinalizeFixture(t)
	stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, models.SoulAgentMintConversation{
		AgentID:              reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Status:               models.SoulMintConversationStatusCompleted,
		ProducedDeclarations: string(declBytes),
	})
	expectedVersion := 0
	_, err := s.handleSoulInstanceFinalizeMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationFinalizeRequest{
			BoundarySignatures: boundarySigs,
			IssuedAt:           time.Now().UTC().Format(time.RFC3339Nano),
			ExpectedVersion:    &expectedVersion,
			SelfAttestation:    "0x00",
		}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeInvalidRequest || appErr.Message != soulInstanceBootstrapTestInvalidRegSig {
		t.Fatalf("expected self-attestation rejection, got %#v", appErr)
	}
}

func assertSoulInstanceFinalizeRejectsVersionMismatch(t *testing.T) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg, identity, key, declBytes, boundarySigs := soulInstanceFinalizeFixture(t)
	stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, models.SoulAgentMintConversation{
		AgentID:              reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Status:               models.SoulMintConversationStatusCompleted,
		ProducedDeclarations: string(declBytes),
	})
	issuedAt := time.Now().UTC()
	expectedVersion := 1
	regMap, _, digest, _, _, appErr := s.buildMintConversationFinalizeV2Registration(reg.AgentID, mintConversationFinalizeIdentityForPublication(identity), testMintConversationDecl(), boundarySigs, issuedAt, expectedVersion+1, "0x00")
	if appErr != nil || regMap == nil {
		t.Fatalf("build registration: %#v", appErr)
	}
	selfSig := soulInstanceFinalizeSignDigest(t, key, digest)
	_, err := s.handleSoulInstanceFinalizeMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationFinalizeRequest{
			BoundarySignatures: boundarySigs,
			IssuedAt:           issuedAt.Format(time.RFC3339Nano),
			ExpectedVersion:    &expectedVersion,
			SelfAttestation:    selfSig,
		}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErrTheory := requireAppTheoryError(t, err)
	if appErrTheory.Code != soulInstanceBootstrapCodeConflict || appErrTheory.Message != "version conflict; reload and try again" {
		t.Fatalf("expected version conflict, got %#v", appErrTheory)
	}
}

func TestSoulInstanceFinalizeMintConversation_SuccessPublishesHostedOffchain(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	packs := &fakeSoulPackStore{}
	s.soulPacks = packs
	reg, identity, key, declBytes, boundarySigs := soulInstanceFinalizeFixture(t)

	stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, models.SoulAgentMintConversation{
		AgentID:              reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Status:               models.SoulMintConversationStatusCompleted,
		ProducedDeclarations: string(declBytes),
	})
	beginResp, err := s.handleSoulInstanceBeginFinalizeMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: boundarySigs}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("begin finalize: %v", err)
	}
	beginOut := mustBeginFinalizeResponse(t, beginResp)
	selfSig := soulInstanceFinalizeSignHexDigest(t, key, beginOut.DigestHex)

	expectSoulInstanceFinalizePublishWrites(t, tdb)
	stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, models.SoulAgentMintConversation{
		AgentID:              reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Status:               models.SoulMintConversationStatusCompleted,
		ProducedDeclarations: string(declBytes),
	})
	finalizeResp, err := s.handleSoulInstanceFinalizeMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationFinalizeRequest{
			BoundarySignatures: boundarySigs,
			IssuedAt:           beginOut.IssuedAt,
			ExpectedVersion:    &beginOut.ExpectedVersion,
			SelfAttestation:    selfSig,
		}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	out := mustFinalizeMintConversationResponse(t, finalizeResp)
	if out.AgentID != identity.AgentID || out.Publication.AgentID != identity.AgentID || out.Publication.PublishedVersion != 1 {
		t.Fatalf("expected explicit agent publication evidence, got %#v", out)
	}
	if out.Publication.RegistrationURI == "" || out.Publication.RegistrationS3Key != soulRegistrationS3Key(identity.AgentID) || out.Publication.VersionedRegistrationS3Key != soulRegistrationVersionedS3Key(identity.AgentID, 1) {
		t.Fatalf("expected registration publication locations, got %#v", out.Publication)
	}
	if out.Promotion == nil || out.Promotion.Stage != models.SoulAgentPromotionStageGraduated || out.Promotion.ReadinessStatus != models.SoulAgentPromotionReadinessGraduated || out.Promotion.LatestConversationID != mintConversationTestConversationID {
		t.Fatalf("expected graduation promotion evidence, got %#v", out.Promotion)
	}
	assertMintConversationFinalizePersisted(t, packs, identity.AgentID, out)
	assertMintConversationFinalizeHostedOffchain(t, out)
	assertMintConversationManagedENSMaterial(t, tdb.ensChannelModels, tdb.ensResolutionModels, identity)
	assertSoulInstanceFinalizeAuditAndLifecycle(t, tdb, identity.AgentID)
}

func TestSoulInstanceBootstrapHelperErrors(t *testing.T) {
	t.Parallel()

	if _, appErr := normalizeSoulInstanceBootstrapDomain("bad domain"); appErr == nil || appErr.Code != soulInstanceBootstrapCodeInvalidRequest {
		t.Fatalf("expected invalid domain error, got %#v", appErr)
	}

	if got := soulInstanceBootstrapErrorFromAppError(nil); got != nil {
		t.Fatalf("expected nil app error mapping, got %#v", got)
	}
	cases := []struct {
		name       string
		in         *apptheory.AppError
		wantCode   string
		wantStatus int
	}{
		{"unauthorized", &apptheory.AppError{Code: appErrCodeUnauthorized, Message: "nope"}, soulInstanceBootstrapCodeUnauthorized, http.StatusUnauthorized},
		{"bad request", &apptheory.AppError{Code: appErrCodeBadRequest, Message: "bad"}, soulInstanceBootstrapCodeInvalidRequest, http.StatusBadRequest},
		{"conflict", &apptheory.AppError{Code: soulMintAppErrCodeConflict, Message: "conflict"}, soulInstanceBootstrapCodeBoundaryViolation, http.StatusForbidden},
		{"internal default", &apptheory.AppError{Code: soulMintAppErrCodeInternal, Message: "boom"}, soulInstanceBootstrapCodeInternal, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			appErr := soulInstanceBootstrapErrorFromAppError(tc.in)
			if appErr.Code != tc.wantCode || appErr.StatusCode != tc.wantStatus {
				t.Fatalf("expected %s/%d, got %#v", tc.wantCode, tc.wantStatus, appErr)
			}
		})
	}

	mapped := soulInstanceBootstrapErrorFromError(&apptheory.AppError{Code: appErrCodeForbidden, Message: "forbidden"})
	if appErr := requireAppTheoryError(t, mapped); appErr.Code != soulInstanceBootstrapCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected forbidden mapping, got %#v", appErr)
	}
	mapped = soulInstanceBootstrapErrorFromError(&apptheory.AppError{Code: soulMintAppErrCodeNotFound, Message: "missing"})
	if appErr := requireAppTheoryError(t, mapped); appErr.Code != soulInstanceBootstrapCodeNotFound || appErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected not found mapping, got %#v", appErr)
	}
	original := soulInstanceBootstrapError(soulInstanceBootstrapCodeInvalidRequest, "bad", http.StatusBadRequest, nil)
	if got := soulInstanceBootstrapErrorFromError(original); got != original {
		t.Fatalf("expected AppTheoryError passthrough, got %#v", got)
	}
	if actor := soulInstanceBootstrapActor(" Demo "); actor != "instance:demo" {
		t.Fatalf("unexpected actor: %q", actor)
	}
	if actor := soulInstanceBootstrapActor(" "); actor != "instance:unknown" {
		t.Fatalf("unexpected blank actor: %q", actor)
	}
}

func TestSoulInstanceBootstrapSigningAndReplayHelperBranches(t *testing.T) {
	t.Parallel()

	t.Run("registration signing rejects nil and expired pending", func(t *testing.T) {
		t.Parallel()

		s := newMintConversationServer(newMintConversationTestDB())
		if appErr := s.requireSoulInstanceRegistrationUsableForSigning(&apptheory.Context{}, nil); appErr == nil || appErr.Code != soulInstanceBootstrapCodeInternal {
			t.Fatalf("expected nil registration internal error, got %#v", appErr)
		}
		expired := soulInstanceBootstrapTestRegistration("reg-expired", "0x00000000000000000000000000000000000000aa", "message")
		expired.ExpiresAt = time.Now().Add(-time.Minute).UTC()
		if appErr := s.requireSoulInstanceRegistrationUsableForSigning(&apptheory.Context{}, &expired); appErr == nil || appErr.Code != soulInstanceBootstrapCodeInvalidRequest {
			t.Fatalf("expected expired registration invalid request, got %#v", appErr)
		}
	})

	t.Run("completed expired registration can replay when identity is not active", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		completed := soulInstanceBootstrapTestRegistration("reg-completed", "0x00000000000000000000000000000000000000aa", "message")
		completed.Status = models.SoulAgentRegistrationStatusCompleted
		completed.ExpiresAt = time.Now().Add(-time.Hour).UTC()
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()
		if appErr := s.requireSoulInstanceRegistrationUsableForSigning(&apptheory.Context{}, &completed); appErr != nil {
			t.Fatalf("expected completed replay to pass expiry gate, got %#v", appErr)
		}
	})

	t.Run("active identity blocks replay", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		reg := soulInstanceBootstrapTestRegistration("reg-active", "0x00000000000000000000000000000000000000aa", "message")
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{Status: models.SoulAgentStatusActive}
		}).Once()
		if appErr := s.requireSoulInstanceRegistrationUsableForSigning(&apptheory.Context{}, &reg); appErr == nil || appErr.Code != soulInstanceBootstrapCodeBoundaryViolation {
			t.Fatalf("expected active identity boundary violation, got %#v", appErr)
		}
	})

	t.Run("promotion operation loader handles nil missing and internal errors", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		if op, appErr := s.loadSoulInstancePromotionOperation(&apptheory.Context{}, nil); op != nil || appErr != nil {
			t.Fatalf("expected nil promotion to skip lookup, got op=%#v err=%#v", op, appErr)
		}
		tdb.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(theoryErrors.ErrItemNotFound).Once()
		if op, appErr := s.loadSoulInstancePromotionOperation(&apptheory.Context{}, &models.SoulAgentPromotion{MintOperationID: "op-missing"}); op != nil || appErr != nil {
			t.Fatalf("expected missing operation to be replay-repairable, got op=%#v err=%#v", op, appErr)
		}
		tdb.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(errors.New("boom")).Once()
		if op, appErr := s.loadSoulInstancePromotionOperation(&apptheory.Context{}, &models.SoulAgentPromotion{MintOperationID: "op-error"}); op != nil || appErr == nil || appErr.Code != soulInstanceBootstrapCodeInternal {
			t.Fatalf("expected internal load error, got op=%#v err=%#v", op, appErr)
		}
	})
}

func TestSoulInstanceBootstrapContextAndDomainHelperBranches(t *testing.T) {
	t.Parallel()

	t.Run("nil context fails after config and store checks", func(t *testing.T) {
		t.Parallel()

		s := newMintConversationServer(newMintConversationTestDB())
		if _, appErr := s.requireSoulInstanceBootstrapContext(nil); appErr == nil || appErr.Code != soulInstanceBootstrapCodeInvalidRequest {
			t.Fatalf("expected invalid request for nil context, got %#v", appErr)
		}
	})

	t.Run("registration id is required after instance key auth", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
		_, appErr := s.requireSoulInstanceBootstrapRegistrationContext(newSoulInstanceBootstrapContext(
			map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
			nil,
			nil,
		))
		if appErr == nil || appErr.Code != soulInstanceBootstrapCodeInvalidRequest {
			t.Fatalf("expected missing id invalid request, got %#v", appErr)
		}
	})

	t.Run("registration not found maps to instance not found", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
		tdb.qReg.On("First", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(theoryErrors.ErrItemNotFound).Once()
		_, appErr := s.requireSoulInstanceBootstrapRegistrationContext(newSoulInstanceBootstrapContext(
			map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
			nil,
			map[string]string{"id": "missing"},
		))
		if appErr == nil || appErr.Code != soulInstanceBootstrapCodeNotFound {
			t.Fatalf("expected missing registration not found, got %#v", appErr)
		}
	})

	t.Run("domain access rejects unowned unverified and missing instance", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		ctx := newSoulInstanceBootstrapContext(nil, nil, nil)

		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
		if _, _, appErr := s.requireSoulInstanceBootstrapDomainAccess(ctx, soulInstanceBootstrapTestInstanceSlug, testDomainExampleCom); appErr == nil || appErr.Details["reason"] != "domain_not_owned" {
			t.Fatalf("expected domain_not_owned, got %#v", appErr)
		}

		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
			*dest = models.Domain{Domain: testDomainExampleCom, InstanceSlug: soulInstanceBootstrapTestInstanceSlug, Status: models.DomainStatusPending}
		}).Once()
		if _, _, appErr := s.requireSoulInstanceBootstrapDomainAccess(ctx, soulInstanceBootstrapTestInstanceSlug, testDomainExampleCom); appErr == nil || appErr.Details["reason"] != "domain_not_verified" {
			t.Fatalf("expected domain_not_verified, got %#v", appErr)
		}

		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
			*dest = models.Domain{Domain: testDomainExampleCom, InstanceSlug: soulInstanceBootstrapTestInstanceSlug, Status: models.DomainStatusVerified}
		}).Once()
		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()
		if _, _, appErr := s.requireSoulInstanceBootstrapDomainAccess(ctx, soulInstanceBootstrapTestInstanceSlug, testDomainExampleCom); appErr == nil || appErr.Details["reason"] != "instance_not_found" {
			t.Fatalf("expected instance_not_found, got %#v", appErr)
		}
	})
}

func newSoulInstanceBootstrapContext(headers map[string]string, body []byte, params map[string]string) *apptheory.Context {
	h := map[string][]string{}
	for k, v := range headers {
		h[strings.ToLower(strings.TrimSpace(k))] = []string{v}
	}
	return &apptheory.Context{
		RequestID: "req-instance-bootstrap",
		Params:    params,
		Request: apptheory.Request{
			Headers: h,
			Body:    body,
		},
	}
}

func soulInstanceBootstrapDomainOnlyBody(domain string) []byte {
	return []byte(`{"domain":"` + strings.TrimSpace(domain) + `"}`)
}

func soulInstanceBootstrapSignedWallet(t *testing.T, message string) (*ecdsa.PrivateKey, string, string) {
	t.Helper()
	walletKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	walletAddr := strings.ToLower(crypto.PubkeyToAddress(walletKey.PublicKey).Hex())
	walletSig, err := crypto.Sign(accounts.TextHash([]byte(message)), walletKey)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return walletKey, walletAddr, "0x" + hex.EncodeToString(walletSig)
}

func requireSoulInstanceVerifyCreatedResponse(t *testing.T, body []byte, walletAddr string) {
	t.Helper()
	var out soulAgentRegistrationVerifyResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Registration.Status != models.SoulAgentRegistrationStatusCompleted || !out.Registration.WalletVerified {
		t.Fatalf("expected completed verified registration, got %#v", out.Registration)
	}
	if out.Operation.OperationID == "" || out.Operation.Kind != models.SoulOperationKindMint {
		t.Fatalf("expected mint operation, got %#v", out.Operation)
	}
	if out.SafeTx == nil || out.SafeTx.To == "" || !strings.HasPrefix(out.SafeTx.Data, "0x") {
		t.Fatalf("expected safe tx payload, got %#v", out.SafeTx)
	}
	if out.Promotion == nil || out.Promotion.RequestStatus != models.SoulAgentPromotionRequestStatusVerified || out.Promotion.PrincipalAddress != walletAddr {
		t.Fatalf("expected verified promotion, got %#v", out.Promotion)
	}
}

func stubSoulInstanceBootstrapDomainAndInstance(t *testing.T, tdb *mintConversationTestDB, domain string, instanceSlug string) {
	t.Helper()
	stubMintConversationInstanceDomain(t, tdb, domain, instanceSlug)
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: instanceSlug, HostedBaseDomain: domain}
	}).Once()
}

func expectSoulInstanceMintConversationDebit(t *testing.T, tdb *mintConversationTestDB, agentID string, expectCreate bool) {
	t.Helper()

	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{
			InstanceSlug:    soulInstanceBootstrapTestInstanceSlug,
			Month:           time.Now().UTC().Format("2006-01"),
			IncludedCredits: 100,
			UsedCredits:     0,
		}
	}).Once()
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("Put", mock.AnythingOfType("*models.UsageLedgerEntry"), mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		entry := testutil.RequireMockArg[*models.UsageLedgerEntry](t, args, 0)
		if entry.InstanceSlug != soulInstanceBootstrapTestInstanceSlug || entry.Module != soulMintConversationStreamModule || entry.RequestedCredits != soulMintConversationStreamBaseCredits {
			t.Fatalf("unexpected mint conversation ledger entry: %#v", entry)
		}
	})
	if expectCreate {
		tb.On("Create", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
			session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
			if session.InstanceSlug != soulInstanceBootstrapTestInstanceSlug || session.AgentID != agentID || session.Status != string(hostedgenesis.StatusInProgress) {
				t.Fatalf("unexpected hosted genesis session create: %#v", session)
			}
		})
	} else {
		tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once()
	}
	tb.On("Create", mock.AnythingOfType("*models.SoulMintConversationIdempotency"), mock.Anything).Return(tb).Maybe()
	if expectCreate {
		tb.On("Create", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
			conv := testutil.RequireMockArg[*models.SoulAgentMintConversation](t, args, 0)
			if conv.AgentID != agentID || conv.ConversationID == "" || conv.Model == "" || conv.Status != models.SoulMintConversationStatusInProgress || conv.ChargedCredits != soulMintConversationStreamBaseCredits {
				t.Fatalf("unexpected durable conversation create: %#v", conv)
			}
		})
	} else {
		tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
	}
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.InstanceBudgetMonth"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
}

func expectSoulInstanceMintConversationProgression(t *testing.T, tdb *mintConversationTestDB, wantStatus hostedgenesis.Status) {
	t.Helper()
	tb, _ := tdb.db.TransactWriteBuilder.(*ttmocks.MockTransactionBuilder)
	if tb == nil {
		tb = new(ttmocks.MockTransactionBuilder)
		tdb.db.TransactWriteBuilder = tb
	}
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		if hostedgenesis.NormalizeStatus(session.Status) != wantStatus {
			t.Fatalf("expected hosted genesis progression status %s, got %#v", wantStatus, session)
		}
		switch wantStatus {
		case hostedgenesis.StatusAssistantTurnReady:
			if session.AssistantCheckpointRef == "" || session.MessageCount < 2 || session.Failure != nil {
				t.Fatalf("assistant-ready session must carry checkpoint/message count and no failure: %#v", session)
			}
		case hostedgenesis.StatusFailed:
			if session.Failure == nil || session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed || !session.Failure.Retryable {
				t.Fatalf("failed session must carry retryable assistant failure: %#v", session)
			}
		}
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
}

func expectSoulInstanceMintConversationExtractionDebit(t *testing.T, tdb *mintConversationTestDB) {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: soulInstanceBootstrapTestInstanceSlug, Month: time.Now().UTC().Format("2006-01"), IncludedCredits: 100, UsedCredits: 0}
	}).Once()
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("Put", mock.AnythingOfType("*models.UsageLedgerEntry"), mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		entry := testutil.RequireMockArg[*models.UsageLedgerEntry](t, args, 0)
		if entry.Module != soulMintConversationExtractModule || entry.RequestedCredits != soulMintConversationExtractBaseCredits {
			t.Fatalf("unexpected extraction ledger entry: %#v", entry)
		}
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.InstanceBudgetMonth"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
}

func expectSoulInstanceMintConversationCompletionWrite(t *testing.T, tdb *mintConversationTestDB) {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
}

func soulInstanceHostedInstanceTrustFixture(t *testing.T) (models.SoulAgentRegistration, *models.SoulAgentIdentity, models.SoulAgentPromotion) {
	t.Helper()
	agentID, err := soul.DeriveAgentIDHex(testDomainExampleCom, provisionTestAgentLocalID)
	if err != nil {
		t.Fatalf("derive agent id: %v", err)
	}
	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	reg := models.SoulAgentRegistration{
		ID:               "reg-hosted",
		Username:         soulInstanceBootstrapTestActor,
		DomainRaw:        testDomainExampleCom,
		DomainNormalized: testDomainExampleCom,
		LocalIDRaw:       provisionTestAgentLocalID,
		LocalID:          provisionTestAgentLocalID,
		AgentID:          agentID,
		AuthorityModel:   models.SoulAuthorityModelInstanceTrust,
		Capabilities:     []string{"travel_planning"},
		DNSVerified:      true,
		HTTPSVerified:    true,
		Status:           models.SoulAgentRegistrationStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	_ = reg.UpdateKeys()
	identity := &models.SoulAgentIdentity{
		AgentID:         agentID,
		Domain:          testDomainExampleCom,
		LocalID:         provisionTestAgentLocalID,
		AuthorityModel:  models.SoulAuthorityModelInstanceTrust,
		MetaURI:         "https://lesser.host/api/v1/soul/agents/" + agentID + "/registration",
		Capabilities:    []string{"travel_planning"},
		Status:          models.SoulAgentStatusPending,
		LifecycleStatus: models.SoulAgentStatusPending,
		AnchorState:     models.SoulAnchorStateHostedOffchain,
		UpdatedAt:       now,
	}
	applyHostedBoundSoulPolicyDefaults(identity)
	_ = identity.UpdateKeys()
	promotion := updateSoulAgentPromotionForInstanceTrustBegin(buildSoulAgentPromotionFromRegistration(&reg, now), now)
	return reg, identity, *promotion
}

func soulInstanceHostedFinalizeFixture(t *testing.T) (models.SoulAgentRegistration, *models.SoulAgentIdentity, models.SoulAgentMintConversation) {
	t.Helper()
	reg, identity, _ := soulInstanceHostedInstanceTrustFixture(t)
	declBytes := mustMarshalJSON(t, testMintConversationDecl())
	return reg, identity, models.SoulAgentMintConversation{
		AgentID:              reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Status:               models.SoulMintConversationStatusCompleted,
		ProducedDeclarations: string(declBytes),
		CreatedAt:            time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	}
}

func soulInstanceFinalizeFixture(t *testing.T) (models.SoulAgentRegistration, *models.SoulAgentIdentity, *ecdsa.PrivateKey, []byte, map[string]string) {
	t.Helper()
	identity, key := testMintConversationIdentityAndKey()
	identity.Domain = testDomainExampleCom
	identity.AgentID = strings.ToLower(strings.TrimSpace(identity.AgentID))
	reg := models.SoulAgentRegistration{
		ID:               "reg-1",
		Username:         soulInstanceBootstrapTestActor,
		DomainRaw:        testDomainExampleCom,
		DomainNormalized: testDomainExampleCom,
		LocalIDRaw:       identity.LocalID,
		LocalID:          identity.LocalID,
		AgentID:          identity.AgentID,
		Wallet:           identity.Wallet,
		DNSVerified:      true,
		HTTPSVerified:    true,
		Status:           models.SoulAgentRegistrationStatusCompleted,
		CompletedAt:      time.Date(2026, 3, 5, 12, 5, 0, 0, time.UTC),
		VerifiedAt:       time.Date(2026, 3, 5, 12, 5, 0, 0, time.UTC),
	}
	decl := testMintConversationDecl()
	declBytes, err := json.Marshal(decl)
	if err != nil {
		t.Fatalf("marshal declarations: %v", err)
	}
	return reg, identity, key, declBytes, soulInstanceFinalizeBoundarySignatures(t, key, decl)
}

func soulInstanceFinalizeBoundarySignatures(t *testing.T, key *ecdsa.PrivateKey, decl soulMintConversationProducedDeclarations) map[string]string {
	t.Helper()
	out := make(map[string]string, len(decl.Boundaries))
	for _, boundary := range decl.Boundaries {
		digest := crypto.Keccak256([]byte(strings.TrimSpace(boundary.Statement)))
		out[strings.TrimSpace(boundary.ID)] = soulInstanceFinalizeSignDigest(t, key, digest)
	}
	return out
}

func soulInstanceFinalizeSignHexDigest(t *testing.T, key *ecdsa.PrivateKey, digestHex string) string {
	t.Helper()
	digest, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(digestHex), "0x"))
	if err != nil {
		t.Fatalf("decode digest: %v", err)
	}
	return soulInstanceFinalizeSignDigest(t, key, digest)
}

func soulInstanceFinalizeSignDigest(t *testing.T, key *ecdsa.PrivateKey, digest []byte) string {
	t.Helper()
	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		t.Fatalf("sign digest: %v", err)
	}
	return "0x" + hex.EncodeToString(sig)
}

func stubSoulInstanceFinalizeReadContext(t *testing.T, tdb *mintConversationTestDB, reg models.SoulAgentRegistration, identity *models.SoulAgentIdentity, conv models.SoulAgentMintConversation) {
	t.Helper()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, conv)
	stubMintConversationIdentity(t, tdb, identity, nil)
}

func assertSoulInstanceFinalizeSigningMaterial(t *testing.T, out soulMintConversationFinalizeBeginResponse) {
	t.Helper()
	if out.Version != "1" || out.ExpectedVersion != 0 || out.NextVersion != 1 || out.RegistrationPreview == nil {
		t.Fatalf("unexpected finalize preflight response: %#v", out)
	}
	assertSoulInstanceFinalizeSelfAttestationSigning(t, out)
	assertSoulInstanceFinalizeCanonicalDigest(t, out)
	assertSoulInstanceFinalizeRequestTemplate(t, out)
	assertSoulInstanceFinalizeBoundaryRequirements(t, out)
}

func assertSoulInstanceFinalizeSelfAttestationSigning(t *testing.T, out soulMintConversationFinalizeBeginResponse) {
	t.Helper()
	if out.SelfAttestationSigning == nil ||
		out.SelfAttestationSigning.SigningMethod != soulInstanceBootstrapTestSigningMethodEIP191 ||
		out.SelfAttestationSigning.MessageEncoding != "hex_bytes" ||
		out.SelfAttestationSigning.MessageHex != out.DigestHex ||
		out.SelfAttestationSigning.DigestHex != out.DigestHex {
		t.Fatalf("unexpected self-attestation signing material: %#v", out.SelfAttestationSigning)
	}
}

func assertSoulInstanceFinalizeCanonicalDigest(t *testing.T, out soulMintConversationFinalizeBeginResponse) {
	t.Helper()
	canonicalJSON, appErr := buildMintConversationFinalizeCanonicalJSON(out.RegistrationPreview)
	if appErr != nil {
		t.Fatalf("canonical JSON: %#v", appErr)
	}
	if out.SelfAttestationSigning.CanonicalJSON != canonicalJSON {
		t.Fatalf("canonical JSON mismatch: got=%s want=%s", out.SelfAttestationSigning.CanonicalJSON, canonicalJSON)
	}
	digest, appErr := computeSoulRegistrationSelfAttestationDigest(out.RegistrationPreview)
	if appErr != nil {
		t.Fatalf("registration digest: %#v", appErr)
	}
	wantDigestHex := "0x" + hex.EncodeToString(digest)
	if out.DigestHex != wantDigestHex {
		t.Fatalf("digest mismatch: got=%s want=%s", out.DigestHex, wantDigestHex)
	}
}

func assertSoulInstanceFinalizeRequestTemplate(t *testing.T, out soulMintConversationFinalizeBeginResponse) {
	t.Helper()
	if out.FinalizeRequestTemplate.ExpectedVersion != out.ExpectedVersion ||
		out.FinalizeRequestTemplate.IssuedAt != out.IssuedAt ||
		out.FinalizeRequestTemplate.SelfAttestation != "" {
		t.Fatalf("unexpected finalize request template: %#v", out.FinalizeRequestTemplate)
	}
}

func assertSoulInstanceFinalizeBoundaryRequirements(t *testing.T, out soulMintConversationFinalizeBeginResponse) {
	t.Helper()
	if len(out.BoundaryRequirements) != 1 ||
		out.BoundaryRequirements[0].SigningMethod != soulInstanceBootstrapTestSigningMethodEIP191 ||
		out.BoundaryRequirements[0].MessageEncoding != "utf8" ||
		out.BoundaryRequirements[0].SignatureHex == "" {
		t.Fatalf("unexpected boundary requirements: %#v", out.BoundaryRequirements)
	}
}

func assertSoulInstanceMintConversationAcceptedResponse(t *testing.T, resp *apptheory.Response) hostedGenesisConversationResponse {
	t.Helper()
	if resp.Status != http.StatusOK || !strings.Contains(resp.Headers["content-type"][0], "application/json") {
		t.Fatalf("expected JSON 200 response, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.ConversationID == "" ||
		out.Conversation.Status != models.SoulMintConversationStatusAssistantTurnReady ||
		out.Conversation.RequestID != "req-instance-bootstrap" ||
		out.Conversation.MessageCount != 2 {
		t.Fatalf("expected progressed assistant-ready durable status, got %#v", out)
	}
	if out.Conversation.TraceIDs == nil ||
		out.Conversation.TraceIDs.IdempotencyKey != soulInstanceBootstrapTestIdempotencyKey ||
		out.Conversation.TraceIDs.CorrelationID != "corr-1" {
		t.Fatalf("expected trace ids, got %#v", out.Conversation.TraceIDs)
	}
	if strings.Contains(string(resp.Body), soulInstanceBootstrapTestConversationMessage) ||
		strings.Contains(string(resp.Body), "assistant reply") ||
		strings.Contains(string(resp.Body), mintConversationInstanceReadTestRawKey) {
		t.Fatalf("response leaked transcript or credential material: %s", string(resp.Body))
	}
	return out
}

func assertSoulInstanceCompleteTerminalConflict(t *testing.T, legacyStatus string, expectedSessionStatus string, reason string) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         legacyStatus,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)

	_, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, map[string]any{"declarations": testMintConversationDecl()}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	assertMintConversationCompletionConflictDetails(t, appErr, soulInstanceBootstrapCodeConflict, http.StatusConflict, expectedSessionStatus, false, false, reason)
}

func assertSoulInstanceCompleteProgressWithoutAssistant(t *testing.T, messages string) {
	t.Helper()
	reg, identity, _ := soulInstanceHostedInstanceTrustFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	conv := models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         models.SoulMintConversationStatusInProgress,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	}
	if strings.TrimSpace(messages) != "" {
		conv.Messages = encodeMintConversationBlob(messages)
	}
	stubMintConversationConversation(t, tdb, conv)
	stubMintConversationIdentity(t, tdb, identity, nil)

	resp, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, map[string]any{"declarations": testMintConversationDecl()}),
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected progress response error: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 progress response, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress ||
		out.Conversation.ConversationID != mintConversationTestConversationID ||
		out.Conversation.PollAfterSeconds == 0 {
		t.Fatalf("expected in-progress completion response, got %#v", out)
	}
	tdb.qConv.AssertNumberOfCalls(t, "Update", 0)
}

func assertSoulInstanceHostedInstanceTrustFinalizePreflight(t *testing.T, out soulMintConversationFinalizeBeginResponse) {
	t.Helper()
	if out.Version != "1" ||
		out.AuthorityModel != models.SoulAuthorityModelInstanceTrust ||
		out.AnchorState != models.SoulAnchorStateHostedOffchain ||
		out.DigestHex != "" ||
		out.SelfAttestationSigning != nil ||
		out.RegistrationPreview == nil {
		t.Fatalf("unexpected hosted preflight response: %#v", out)
	}
	if len(out.BoundaryRequirements) != 1 ||
		out.BoundaryRequirements[0].SigningMethod != models.SoulAuthorityModelInstanceTrust ||
		out.BoundaryRequirements[0].MessageEncoding != "none" ||
		out.BoundaryRequirements[0].SignatureHex != "" ||
		out.BoundaryRequirements[0].SignerWallet != "" ||
		out.BoundaryRequirements[0].DigestHex != "" {
		t.Fatalf("unexpected hosted boundary requirements: %#v", out.BoundaryRequirements)
	}
	assertSoulInstanceHostedInstanceTrustRegistrationMap(t, out.RegistrationPreview)
}

func assertSoulInstanceHostedInstanceTrustFinalizeResponse(t *testing.T, out soulMintConversationFinalizeResponse) {
	t.Helper()
	if out.Agent.AuthorityModel != models.SoulAuthorityModelInstanceTrust ||
		out.Agent.AnchorState != models.SoulAnchorStateHostedOffchain ||
		out.Agent.Wallet != "" ||
		out.Agent.PrincipalAddress != "" ||
		out.PublishedVersion != 1 ||
		out.Publication.AuthorityModel != models.SoulAuthorityModelInstanceTrust ||
		out.Publication.AnchorState != models.SoulAnchorStateHostedOffchain ||
		out.Promotion == nil ||
		out.Promotion.AuthorityModel != models.SoulAuthorityModelInstanceTrust {
		t.Fatalf("unexpected hosted finalize response: %#v", out)
	}
}

func assertSoulInstanceHostedInstanceTrustRegistrationMap(t *testing.T, reg map[string]any) {
	t.Helper()
	if reg["authorityModel"] != models.SoulAuthorityModelInstanceTrust ||
		reg["anchorState"] != models.SoulAnchorStateHostedOffchain ||
		reg["wallet"] != nil ||
		reg["principal"] != nil {
		t.Fatalf("unexpected hosted registration preview authority: %#v", reg)
	}
	att, ok := reg["attestations"].(map[string]any)
	if !ok || att["hostAuthority"] != models.SoulAuthorityModelInstanceTrust || att["selfAttestation"] != nil {
		t.Fatalf("unexpected hosted attestations: %#v", reg["attestations"])
	}
	boundaries, ok := reg["boundaries"].([]any)
	if !ok || len(boundaries) != 1 {
		t.Fatalf("unexpected hosted boundaries: %#v", reg["boundaries"])
	}
	first, ok := boundaries[0].(map[string]any)
	if !ok || first["signature"] != nil {
		t.Fatalf("hosted boundary must omit signature: %#v", boundaries[0])
	}
}

func assertSoulInstanceHostedInstanceTrustRegistrationFile(t *testing.T, body []byte) {
	t.Helper()
	if len(body) == 0 {
		t.Fatal("expected hosted registration artifact")
	}
	var reg map[string]any
	if err := json.Unmarshal(body, &reg); err != nil {
		t.Fatalf("unmarshal hosted registration artifact: %v", err)
	}
	assertSoulInstanceHostedInstanceTrustRegistrationMap(t, reg)
	parsed, err := soul.ParseRegistrationFileV2(body)
	if err != nil {
		t.Fatalf("parse hosted registration artifact: %v", err)
	}
	if err := parsed.Validate(); err != nil {
		t.Fatalf("validate hosted registration artifact: %v", err)
	}
}

func expectSoulInstanceFinalizePublishWrites(t *testing.T, tdb *mintConversationTestDB) {
	t.Helper()
	qVersion := new(ttmocks.MockQuery)
	qBoundary := new(ttmocks.MockQuery)
	qBoundIdx := new(ttmocks.MockQuery)
	for typeName, q := range map[string]*ttmocks.MockQuery{
		"*models.SoulAgentVersion":              qVersion,
		"*models.SoulAgentBoundary":             qBoundary,
		"*models.SoulBoundaryKeywordAgentIndex": qBoundIdx,
	} {
		tdb.db.On("Model", mock.AnythingOfType(typeName)).Return(q).Maybe()
		addStandardMockQueryStubs(q)
	}
	qVersion.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
		*dest = nil
	}).Once()
	qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Once()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("ConditionCheck", mock.AnythingOfType("*models.SoulAgentIdentity"), mock.Anything).Return(tb).Once()
	tb.On("Create", mock.AnythingOfType("*models.SoulAgentVersion"), mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
	tdb.qIdentity.On("Update", mock.Anything, mock.Anything).Return(nil).Maybe()
}

func assertSoulInstanceFinalizeAuditAndLifecycle(t *testing.T, tdb *mintConversationTestDB, agentID string) {
	t.Helper()
	assertSoulInstanceFinalizeAuditActor(t, tdb, agentID)
	assertSoulInstanceFinalizeLifecycleEvent(t, tdb, agentID)
}

func assertSoulInstanceFinalizeAuditActor(t *testing.T, tdb *mintConversationTestDB, agentID string) {
	t.Helper()
	for _, entry := range tdb.auditModels {
		if entry == nil {
			continue
		}
		if entry.Action == "soul.mint_conversation.finalize" && entry.Target == "soul_agent_identity:"+agentID {
			if entry.Actor != soulInstanceBootstrapTestActor {
				t.Fatalf("expected instance audit actor, got %#v", entry)
			}
			return
		}
	}
	t.Fatalf("expected finalize audit entry, got %#v", tdb.auditModels)
}

func assertSoulInstanceFinalizeLifecycleEvent(t *testing.T, tdb *mintConversationTestDB, agentID string) {
	t.Helper()
	for _, event := range tdb.lifecycleModels {
		if event == nil {
			continue
		}
		if event.EventType == models.SoulAgentPromotionEventTypeGraduated && event.AgentID == agentID {
			if event.ConversationID != mintConversationTestConversationID || event.AnchorState != models.SoulAnchorStateHostedOffchain {
				t.Fatalf("unexpected graduation lifecycle event: %#v", event)
			}
			return
		}
	}
	t.Fatalf("expected graduated lifecycle event, got %#v", tdb.lifecycleModels)
}

func soulInstanceBootstrapTestRegistration(id string, wallet string, walletMessage string) models.SoulAgentRegistration {
	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	return models.SoulAgentRegistration{
		ID:               id,
		Username:         soulInstanceBootstrapTestActor,
		DomainRaw:        testDomainExampleCom,
		DomainNormalized: testDomainExampleCom,
		LocalIDRaw:       provisionTestAgentLocalID,
		LocalID:          provisionTestAgentLocalID,
		AgentID:          "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab",
		Wallet:           strings.ToLower(strings.TrimSpace(wallet)),
		WalletMessage:    walletMessage,
		ProofToken:       "proof-token",
		DNSVerified:      true,
		HTTPSVerified:    true,
		Status:           models.SoulAgentRegistrationStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        time.Now().Add(time.Hour).UTC(),
	}
}
