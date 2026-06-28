package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestSoulInstanceHostedOffchainBegin_DoesNotRequireRegistryContract(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.cfg.SoulRegistryContractAddress = ""
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, testDomainExampleCom, soulInstanceBootstrapTestInstanceSlug)
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Twice()

	resp, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulAgentRegistrationBeginRequest{
			Domain:         "Example.COM",
			LocalID:        provisionTestAgentLocalID,
			AuthorityModel: models.SoulAuthorityModelInstanceTrust,
			Capabilities:   []any{"travel_planning"},
		}),
		nil,
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%q", resp.Status, string(resp.Body))
	}

	var out soulAgentRegistrationBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertSoulInstanceHostedBeginOmitsWallet(t, out, resp.Body)
	assertSoulInstanceHostedBeginRegistration(t, out.Registration)
	assertSoulInstanceHostedBeginPromotion(t, out.Promotion)
}

func TestSoulInstanceHostedOffchainMintConversation_DoesNotRequireRegistryContract(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.cfg.SoulRegistryContractAddress = ""
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	stubHostedGenesisAssistantRunner(t, s, "assistant reply", nil)
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("hosted/off-chain mint conversation must not enqueue SQS authority: %#v", msg)
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
	if out.Conversation.RegistrationID != reg.ID || out.Conversation.AgentID != reg.AgentID {
		t.Fatalf("expected hosted session for registration/agent, got %#v", out.Conversation)
	}
}

func TestSoulInstanceHostedOffchainRead_DoesNotRequireRegistryContract(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.cfg.SoulRegistryContractAddress = ""
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Status:         models.SoulMintConversationStatusInProgress,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
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
	if out.Conversation.RegistrationID != reg.ID || out.Conversation.ConversationID != mintConversationTestConversationID {
		t.Fatalf("expected hosted conversation projection, got %#v", out.Conversation)
	}
}

func TestSoulInstanceHostedOffchainComplete_DoesNotRequireRegistryContract(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.cfg.SoulRegistryContractAddress = ""
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
		t.Fatalf("expected declaration-ready hosted conversation, got %#v", out.Conversation)
	}
}

func TestSoulInstanceRegistryRequiredRoute_FailsClosedWithoutRegistryContract(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.cfg.SoulRegistryContractAddress = ""
	reg := soulInstanceBootstrapTestRegistration("reg-preflight", "0x00000000000000000000000000000000000000aa", "wallet message")
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

	_, err := s.handleSoulInstanceAgentRegistrationPrincipalDeclarationPreflight(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulAgentRegistrationPrincipalDeclarationPreflightRequest{
			PrincipalAddress:     "0x00000000000000000000000000000000000000bb",
			PrincipalDeclaration: soulInstanceBootstrapTestPrincipalDeclaration,
			DeclaredAt:           canonicalSoulSignedTimestamp(time.Now().UTC()),
		}),
		map[string]string{"id": reg.ID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden || appErr.Message != "soul registry is not configured" {
		t.Fatalf("expected registry-required preflight to fail closed, got %#v", appErr)
	}
}
