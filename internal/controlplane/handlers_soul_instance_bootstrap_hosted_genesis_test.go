package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestSoulInstanceHostedInstanceTrustFinalizePublishesEmptyCapabilities(t *testing.T) {
	t.Parallel()

	reg, identity, completedConv := soulInstanceHostedFinalizeFixture(t)
	reg.Capabilities = []string{"simulacrum.hosted-first-default"}
	identity.Capabilities = append([]string(nil), reg.Capabilities...)
	decl := testMintConversationDecl()
	decl.Capabilities = []soul.CapabilityV2{}
	completedConv.Status = models.SoulMintConversationStatusDeclarationReady
	completedConv.ProducedDeclarations = string(mustMarshalJSON(t, decl))
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
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", resp.Status, string(resp.Body))
	}

	packs, ok := s.soulPacks.(*fakeSoulPackStoreForPublish)
	if !ok {
		t.Fatalf("unexpected soul pack store: %T", s.soulPacks)
	}
	published := packs.puts[soulRegistrationS3Key(reg.AgentID)]
	parsed, err := soul.ParseRegistrationFileV2(published)
	if err != nil {
		t.Fatalf("parse hosted registration artifact: %v", err)
	}
	if len(parsed.Capabilities) != 0 {
		t.Fatalf("expected hosted registration to publish empty capabilities without fallback, got %#v", parsed.Capabilities)
	}
	if err := parsed.Validate(); err != nil {
		t.Fatalf("validate hosted registration artifact: %v", err)
	}
}

func TestSoulInstanceHostedInstanceTrustFinalizeAllowsNoPublishableCapabilities(t *testing.T) {
	t.Parallel()

	for _, tc := range soulInstanceFinalizeStateCalls() {
		t.Run(tc.name, func(t *testing.T) {
			tdb := newMintConversationTestDB()
			s := newMintConversationServer(tdb)
			reg, identity, completedConv := soulInstanceHostedFinalizeFixture(t)
			reg.Capabilities = nil
			identity.Capabilities = nil
			decl := testMintConversationDecl()
			decl.Capabilities = []soul.CapabilityV2{}
			completedConv.Status = models.SoulMintConversationStatusDeclarationReady
			completedConv.ProducedDeclarations = string(mustMarshalJSON(t, decl))
			stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, completedConv)
			if tc.name == "finalize" {
				expectSoulInstanceFinalizePublishWrites(t, tdb)
			}

			resp, err := tc.call(s, newSoulInstanceBootstrapContext(
				map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
				mustMarshalJSON(t, map[string]any{}),
				map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
			))
			if err != nil {
				t.Fatalf("unexpected empty-capabilities finalize error: %v", err)
			}
			if resp.Status != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%q", resp.Status, string(resp.Body))
			}
		})
	}
}

func TestSoulInstanceGetRegistrationMintConversation_ReturnsReadyWithoutAutoFinalize(t *testing.T) {
	t.Parallel()

	reg, identity, completedConv := soulInstanceHostedFinalizeFixture(t)
	decl := testMintConversationDecl()
	decl.Capabilities = []soul.CapabilityV2{}
	completedConv.Status = models.SoulMintConversationStatusDeclarationReady
	completedConv.ProducedDeclarations = string(mustMarshalJSON(t, decl))
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, completedConv)

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
	if out.Conversation.Status != models.SoulMintConversationStatusDeclarationReady || out.Conversation.ProducedDeclarations == nil {
		t.Fatalf("expected declaration-ready status envelope, got %#v", out.Conversation)
	}
	if len(out.Conversation.ProducedDeclarations.Declarations.Capabilities) != 0 {
		t.Fatalf("status read must project produced evidence without synthesizing publish fields, got %#v", out.Conversation.ProducedDeclarations.Declarations.Capabilities)
	}

	packs, ok := s.soulPacks.(*fakeSoulPackStoreForPublish)
	if !ok {
		t.Fatalf("unexpected soul pack store: %T", s.soulPacks)
	}
	if len(packs.puts) != 0 {
		t.Fatalf("GET status read must not publish registration artifacts: %#v", packs.puts)
	}
	tdb.qIdentity.AssertNotCalled(t, "Update", mock.Anything)
	for _, entry := range tdb.auditModels {
		if entry != nil && entry.Action == "soul.mint_conversation.finalize" {
			t.Fatalf("GET status read must not emit finalize audit mutations: audit=%#v", tdb.auditModels)
		}
	}
	if len(tdb.lifecycleModels) != 0 {
		t.Fatalf("GET status read must not emit finalize lifecycle mutations: lifecycle=%#v", tdb.lifecycleModels)
	}
}
