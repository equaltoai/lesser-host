package controlplane

import (
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

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	soulInstanceBootstrapTestActor                = "instance:inst1"
	soulInstanceBootstrapTestPrincipalDeclaration = "I declare authority over this Lesser-hosted soul."
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
				InstanceSlug: "inst1",
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
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationInstanceDomain(t, tdb, testDomainExampleCom, "other-inst")

	_, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey}, soulInstanceBootstrapDomainOnlyBody(testDomainExampleCom), nil))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected boundary violation 403, got %#v", appErr)
	}
	if appErr.Details["field"] != "domain" || appErr.Details["reason"] != "tenant_domain_mismatch" {
		t.Fatalf("expected tenant-domain mismatch details, got %#v", appErr.Details)
	}
}

func TestSoulInstanceAgentRegistrationBegin_ScopesWritesToInstanceKey(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, testDomainExampleCom, "inst1")
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
	if out.Registration.Username != soulInstanceBootstrapTestActor || out.Wallet.Username != soulInstanceBootstrapTestActor {
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

func TestSoulInstanceBootstrapScaffold_RejectsCrossInstanceRegistration(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
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
	if appErr.Details["reason"] != "tenant_domain_mismatch" {
		t.Fatalf("expected tenant-domain mismatch details, got %#v", appErr.Details)
	}
}

func TestSoulInstanceAgentRegistrationPrincipalDeclarationPreflight_MatchesCanonicalMaterial(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := soulInstanceBootstrapTestRegistration("reg-preflight", "0x00000000000000000000000000000000000000aa", "wallet message")
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, "inst1")
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
	if out.SigningMethod != "eip191_personal_sign" || out.MessageEncoding != "hex_bytes" {
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

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, "inst1")
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

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, "inst1")
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
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, "inst1")
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()

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

func TestSoulInstanceBootstrapScaffold_ConversationRouteReachesScaffold(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, "inst1")
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         models.SoulMintConversationStatusCompleted,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})

	_, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeNotImplemented || appErr.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected scaffold 501, got %#v", appErr)
	}
	if appErr.Details["route"] != soulInstanceBootstrapRouteConversationComplete {
		t.Fatalf("expected route detail, got %#v", appErr.Details)
	}
}

func TestSoulInstanceBootstrapScaffold_AllRegistrationRoutesReachScaffold(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		route     string
		needsConv bool
		call      func(*Server, *apptheory.Context) (*apptheory.Response, error)
	}{
		{
			name:  "mint conversation",
			route: soulInstanceBootstrapRouteConversation,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceMintConversation(ctx)
			},
		},
		{
			name:      "get conversation",
			route:     soulInstanceBootstrapRouteConversationGet,
			needsConv: true,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceGetRegistrationMintConversation(ctx)
			},
		},
		{
			name:      "finalize preflight",
			route:     soulInstanceBootstrapRouteFinalizePreflight,
			needsConv: true,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceFinalizeMintConversationPreflight(ctx)
			},
		},
		{
			name:      "finalize begin",
			route:     soulInstanceBootstrapRouteFinalizeBegin,
			needsConv: true,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceBeginFinalizeMintConversation(ctx)
			},
		},
		{
			name:      "finalize",
			route:     soulInstanceBootstrapRouteFinalize,
			needsConv: true,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceFinalizeMintConversation(ctx)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tdb := newMintConversationTestDB()
			s := newMintConversationServer(tdb)
			reg := mintConversationHandleReg()
			expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
			stubMintConversationRegistration(t, tdb, reg)
			stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, "inst1")

			params := map[string]string{"id": reg.ID}
			if tc.needsConv {
				params["conversationId"] = mintConversationTestConversationID
				stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
					AgentID:        reg.AgentID,
					ConversationID: mintConversationTestConversationID,
					Status:         models.SoulMintConversationStatusCompleted,
					CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
				})
			}

			_, err := tc.call(s, newSoulInstanceBootstrapContext(
				map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
				nil,
				params,
			))
			appErr := requireAppTheoryError(t, err)
			if appErr.Code != soulInstanceBootstrapCodeNotImplemented || appErr.StatusCode != http.StatusNotImplemented {
				t.Fatalf("expected scaffold 501, got %#v", appErr)
			}
			if appErr.Details["route"] != tc.route {
				t.Fatalf("expected route %q detail, got %#v", tc.route, appErr.Details)
			}
		})
	}
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
		expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
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
		expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
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
		if _, _, appErr := s.requireSoulInstanceBootstrapDomainAccess(ctx, "inst1", testDomainExampleCom); appErr == nil || appErr.Details["reason"] != "domain_not_owned" {
			t.Fatalf("expected domain_not_owned, got %#v", appErr)
		}

		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
			*dest = models.Domain{Domain: testDomainExampleCom, InstanceSlug: "inst1", Status: models.DomainStatusPending}
		}).Once()
		if _, _, appErr := s.requireSoulInstanceBootstrapDomainAccess(ctx, "inst1", testDomainExampleCom); appErr == nil || appErr.Details["reason"] != "domain_not_verified" {
			t.Fatalf("expected domain_not_verified, got %#v", appErr)
		}

		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
			*dest = models.Domain{Domain: testDomainExampleCom, InstanceSlug: "inst1", Status: models.DomainStatusVerified}
		}).Once()
		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()
		if _, _, appErr := s.requireSoulInstanceBootstrapDomainAccess(ctx, "inst1", testDomainExampleCom); appErr == nil || appErr.Details["reason"] != "instance_not_found" {
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
