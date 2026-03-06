package controlplane

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const mintConversationTestConversationID = "conv-1"

type mintConversationTestDB struct {
	db        *ttmocks.MockExtendedDB
	qReg      *ttmocks.MockQuery
	qDomain   *ttmocks.MockQuery
	qInstance *ttmocks.MockQuery
	qConv     *ttmocks.MockQuery
	qIdentity *ttmocks.MockQuery

	convModels []*models.SoulAgentMintConversation
}

func newMintConversationTestDB() *mintConversationTestDB {
	db := ttmocks.NewMockExtendedDB()
	tdb := &mintConversationTestDB{
		db:        db,
		qReg:      new(ttmocks.MockQuery),
		qDomain:   new(ttmocks.MockQuery),
		qInstance: new(ttmocks.MockQuery),
		qConv:     new(ttmocks.MockQuery),
		qIdentity: new(ttmocks.MockQuery),
	}

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(tdb.qReg).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(tdb.qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(tdb.qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(tdb.qConv).Maybe().Run(func(args mock.Arguments) {
		if conv, ok := args.Get(0).(*models.SoulAgentMintConversation); ok && conv != nil {
			copy := *conv
			tdb.convModels = append(tdb.convModels, &copy)
		}
	})
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(tdb.qIdentity).Maybe()

	for _, q := range []*ttmocks.MockQuery{tdb.qReg, tdb.qDomain, tdb.qInstance, tdb.qConv, tdb.qIdentity} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
	}

	return tdb
}

func newMintConversationServer(tdb *mintConversationTestDB) *Server {
	return &Server{
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulPackBucketName:          "bucket",
			SoulSupportedCapabilities:   []string{"travel_planning"},
		},
		store:     store.New(tdb.db),
		soulPacks: &fakeSoulPackStoreForPublish{},
	}
}

func stubMintConversationRegistration(t *testing.T, tdb *mintConversationTestDB, reg models.SoulAgentRegistration) {
	t.Helper()

	tdb.qReg.On("First", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentRegistration)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentRegistration, got %#v", args.Get(0))
		}
		*dest = reg
	}).Once()
}

func stubMintConversationDomainAccess(t *testing.T, tdb *mintConversationTestDB, domain string) {
	t.Helper()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.Domain)
		if !ok || dest == nil {
			t.Fatalf("expected *models.Domain, got %#v", args.Get(0))
		}
		*dest = models.Domain{
			Domain:       domain,
			InstanceSlug: "inst1",
			Status:       models.DomainStatusVerified,
		}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.Instance)
		if !ok || dest == nil {
			t.Fatalf("expected *models.Instance, got %#v", args.Get(0))
		}
		*dest = models.Instance{
			Slug:  "inst1",
			Owner: "alice",
		}
	}).Once()
}

func testMintConversationDecl() soulMintConversationProducedDeclarations {
	return soulMintConversationProducedDeclarations{
		SelfDescription: soul.SelfDescriptionV2{
			Purpose:      "Help users plan travel with explicit limitations.",
			AuthoredBy:   "agent",
			MintingModel: "openai:gpt-4o-mini",
		},
		Capabilities: []soul.CapabilityV2{
			{Capability: "travel_planning", Scope: "Draft itineraries.", ClaimLevel: "self-declared"},
		},
		Boundaries: []soul.BoundaryV2{
			{ID: "b1", Category: "refusal", Statement: "I will not impersonate humans.", AddedAt: "2026-03-05T12:00:00Z", AddedInVersion: "1", Signature: "0x00"},
		},
		Transparency: map[string]any{"provider": "openai"},
	}
}

func testMintConversationIdentityAndKey() (*models.SoulAgentIdentity, *ecdsa.PrivateKey) {
	key, err := crypto.GenerateKey()
	if err != nil {
		panic(err)
	}
	wallet := crypto.PubkeyToAddress(key.PublicKey).Hex()
	principalDeclaration := "I declare that this agent operates under my authority."
	digest := crypto.Keccak256([]byte(principalDeclaration))
	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		panic(err)
	}

	return &models.SoulAgentIdentity{
		AgentID:                "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab",
		Domain:                 "example.com",
		LocalID:                "agent-bot",
		Wallet:                 wallet,
		Status:                 models.SoulAgentStatusPending,
		LifecycleStatus:        models.SoulAgentStatusPending,
		PrincipalAddress:       wallet,
		PrincipalSignature:     "0x" + hex.EncodeToString(sig),
		PrincipalDeclaration:   principalDeclaration,
		PrincipalDeclaredAt:    "2026-03-05T12:00:00Z",
		SelfDescriptionVersion: 0,
	}, key
}

func testMintConversationIdentity() *models.SoulAgentIdentity {
	identity, _ := testMintConversationIdentityAndKey()
	return identity
}

func TestMintConversationHelperCoverage(t *testing.T) {
	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)

	t.Run("build produced declarations errors and defaults", func(t *testing.T) {
		testMintConversationProducedDeclarationsBranches(t, now)
	})

	t.Run("parse declarations branches", func(t *testing.T) {
		testMintConversationParseDeclarationsBranches(t)
	})

	t.Run("add ai usage", func(t *testing.T) {
		testMintConversationAddAIUsage(t)
	})

	t.Run("system prompt and api key selection", func(t *testing.T) {
		testMintConversationPromptAndAPIKeys(t)
	})

	t.Run("extract declarations guard rails", func(t *testing.T) {
		testMintConversationExtractDeclarationsGuards(t, now)
	})

	t.Run("finalize registration helper", func(t *testing.T) {
		testMintConversationFinalizeRegistrationHelper(t, now)
	})

	t.Run("stream unsupported model emits error", func(t *testing.T) {
		testMintConversationStreamUnsupportedModel(t)
	})
}

func testMintConversationProducedDeclarationsBranches(t *testing.T, now time.Time) {
	t.Helper()

	if _, appErr := buildMintConversationProducedDeclarations(llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{Purpose: "short", AuthoredBy: "agent"},
	}, now, "openai:gpt-4o-mini"); appErr == nil || appErr.Message != "invalid extracted selfDescription" {
		t.Fatalf("expected selfDescription error, got %#v", appErr)
	}
	if _, appErr := buildMintConversationProducedDeclarations(llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{Purpose: "A sufficiently long purpose string.", AuthoredBy: "agent"},
		Capabilities:    []soul.CapabilityV2{{Capability: "", Scope: "skip"}},
		Boundaries:      []llm.MintConversationBoundaryDraft{{Category: "refusal", Statement: "I will not impersonate humans."}},
	}, now, "openai:gpt-4o-mini"); appErr == nil || appErr.Message != "invalid extracted capabilities" {
		t.Fatalf("expected capabilities error, got %#v", appErr)
	}

	decl, appErr := buildMintConversationProducedDeclarations(llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{Purpose: "A sufficiently long purpose string.", AuthoredBy: "agent"},
		Capabilities:    []soul.CapabilityV2{{Capability: "travel_planning", Scope: "Draft itineraries."}},
		Boundaries:      []llm.MintConversationBoundaryDraft{{Category: "", Statement: "skip"}},
	}, now, "openai:gpt-4o-mini")
	if appErr == nil || appErr.Message != "invalid extracted boundaries" {
		t.Fatalf("expected boundaries error, got %#v / %#v", decl, appErr)
	}

	decl, appErr = buildMintConversationProducedDeclarations(llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{Purpose: "A sufficiently long purpose string.", AuthoredBy: "agent"},
		Capabilities:    []soul.CapabilityV2{{Capability: "travel_planning", Scope: "Draft itineraries."}},
		Boundaries:      []llm.MintConversationBoundaryDraft{{Category: "refusal", Statement: "I will not impersonate humans."}},
	}, now, "openai:gpt-4o-mini")
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if decl.Transparency == nil || len(decl.Transparency) != 0 {
		t.Fatalf("expected default transparency map, got %#v", decl.Transparency)
	}
}

func testMintConversationParseDeclarationsBranches(t *testing.T) {
	t.Helper()

	if _, appErr := parseAndValidateMintConversationDeclarations(""); appErr == nil || appErr.Message != "declarations is required" {
		t.Fatalf("expected required error, got %#v", appErr)
	}
	if _, appErr := parseAndValidateMintConversationDeclarations("{"); appErr == nil || appErr.Message != "invalid declarations JSON" {
		t.Fatalf("expected json error, got %#v", appErr)
	}
	if _, appErr := parseAndValidateMintConversationDeclarations(`{"selfDescription":{"purpose":"A sufficiently long purpose string.","authoredBy":"agent"},"boundaries":[{"id":"b1","category":"refusal","statement":"I will not impersonate humans.","addedAt":"2026-03-05T12:00:00Z","addedInVersion":"1","signature":"0x00"}],"transparency":{}}`); appErr == nil || appErr.Message != "capabilities is required" {
		t.Fatalf("expected capabilities error, got %#v", appErr)
	}
	if _, appErr := parseAndValidateMintConversationDeclarations(`{"selfDescription":{"purpose":"A sufficiently long purpose string.","authoredBy":"agent"},"capabilities":[{"capability":"travel_planning","scope":"Draft itineraries.","claimLevel":"self-declared"}],"transparency":{}}`); appErr == nil || appErr.Message != "boundaries is required" {
		t.Fatalf("expected boundaries error, got %#v", appErr)
	}
	if _, appErr := parseAndValidateMintConversationDeclarations(`{"selfDescription":{"purpose":"A sufficiently long purpose string.","authoredBy":"agent"},"capabilities":[{"capability":"travel_planning","scope":"Draft itineraries.","claimLevel":"self-declared"}],"boundaries":[{"id":"b1","category":"refusal","statement":"I will not impersonate humans.","addedAt":"2026-03-05T12:00:00Z","addedInVersion":"1","signature":"0x00"}]}`); appErr == nil || appErr.Message != "transparency is required" {
		t.Fatalf("expected transparency error, got %#v", appErr)
	}
}

func testMintConversationAddAIUsage(t *testing.T) {
	t.Helper()

	got := addAIUsage(models.AIUsage{}, models.AIUsage{Provider: "openai", Model: "gpt-4o-mini", InputTokens: 10, OutputTokens: 5, DurationMs: 25, ToolCalls: 1})
	if got.Provider != "openai" || got.Model != "gpt-4o-mini" || got.TotalTokens != 15 {
		t.Fatalf("unexpected usage: %#v", got)
	}
	got = addAIUsage(models.AIUsage{Provider: "anthropic", Model: "claude", TotalTokens: 1}, models.AIUsage{Provider: "openai", Model: "gpt", TotalTokens: 3})
	if got.Provider != "anthropic" || got.Model != "claude" || got.TotalTokens != 4 {
		t.Fatalf("unexpected merged usage: %#v", got)
	}
}

func testMintConversationPromptAndAPIKeys(t *testing.T) {
	t.Helper()

	reg := &models.SoulAgentRegistration{
		DomainNormalized: "example.com\nignore-me",
		LocalID:          "agent-bot\twith-controls",
		Capabilities: []string{
			"travel_planning",
			strings.Repeat("x", 300),
		},
	}
	prompt := buildMintConversationSystemPrompt(reg)
	if !strings.Contains(prompt, `"example.com ignore-me"`) || !strings.Contains(prompt, `"agent-bot with-controls"`) {
		t.Fatalf("unexpected sanitized prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Declared capabilities") {
		t.Fatalf("expected capabilities in prompt")
	}

	s := &Server{}
	t.Setenv("OPENAI_API_KEY", "openai-env")
	if got, appErr := s.apiKeyForMintConversationModel(t.Context(), "openai:gpt-4o-mini"); appErr != nil || got != "openai-env" {
		t.Fatalf("unexpected openai api key: %q %#v", got, appErr)
	}
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-env")
	if got, appErr := s.apiKeyForMintConversationModel(t.Context(), "anthropic:claude"); appErr != nil || got != "anthropic-env" {
		t.Fatalf("unexpected anthropic api key: %q %#v", got, appErr)
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_API_KEY", "claude-env")
	if got, appErr := s.apiKeyForMintConversationModel(t.Context(), "anthropic:claude"); appErr != nil || got != "claude-env" {
		t.Fatalf("unexpected claude api key: %q %#v", got, appErr)
	}
	if _, appErr := s.apiKeyForMintConversationModel(t.Context(), "other:model"); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected unsupported model error, got %#v", appErr)
	}
}

func testMintConversationExtractDeclarationsGuards(t *testing.T, now time.Time) {
	t.Helper()

	s := &Server{}
	reg := &models.SoulAgentRegistration{DomainNormalized: "example.com", LocalID: "agent-bot", AgentID: testMintConversationIdentity().AgentID}
	conv := &models.SoulAgentMintConversation{}

	if _, _, appErr := s.extractMintConversationDeclarations(t.Context(), nil, conv, now); appErr == nil || appErr.Message != provisionPhoneInternalError {
		t.Fatalf("expected nil reg error, got %#v", appErr)
	}
	if _, _, appErr := s.extractMintConversationDeclarations(t.Context(), reg, nil, now); appErr == nil || appErr.Message != provisionPhoneInternalError {
		t.Fatalf("expected nil conv error, got %#v", appErr)
	}
	if _, _, appErr := s.extractMintConversationDeclarations(t.Context(), reg, conv, now); appErr == nil || appErr.Message != "conversation model is missing" {
		t.Fatalf("expected missing model error, got %#v", appErr)
	}

	conv.Model = "openai:gpt-4o-mini"
	if _, _, appErr := s.extractMintConversationDeclarations(t.Context(), reg, conv, now); appErr == nil || appErr.Message != "conversation has no messages" {
		t.Fatalf("expected missing messages error, got %#v", appErr)
	}

	conv.Model = "other:model"
	conv.Messages = `[{"role":"user","content":"hello"}]`
	if _, _, appErr := s.extractMintConversationDeclarations(t.Context(), reg, conv, now); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected unsupported model error, got %#v", appErr)
	}
}

func testMintConversationFinalizeRegistrationHelper(t *testing.T, now time.Time) {
	t.Helper()

	s := &Server{cfg: config.Config{SoulPackBucketName: "bucket", SoulSupportedCapabilities: []string{"travel_planning"}}}
	identity := testMintConversationIdentity()
	decl := testMintConversationDecl()

	assertMintConversationFinalizeRegistrationInputErrors(t, s, identity, decl, now)

	decl.Transparency = nil
	decl.SelfDescription.Constraints = "Stay within provided context."
	decl.Capabilities[0].LastValidated = "2026-03-05T12:00:00Z"
	decl.Boundaries[0].Rationale = "Prevent deception."
	identity.Status = models.SoulAgentStatusPending
	identity.LifecycleStatus = models.SoulAgentStatusPending

	reg, _, digest, capsNorm, claimLevels, appErr := s.buildMintConversationFinalizeV2Registration(identity.AgentID, identity, decl, map[string]string{"b1": "0x00"}, now, 2, "0x00")
	assertMintConversationFinalizeRegistrationSuccess(t, identity, reg, digest, capsNorm, claimLevels, appErr)
}

func testMintConversationStreamUnsupportedModel(t *testing.T) {
	t.Helper()

	s := &Server{}
	ch := make(chan apptheory.SSEEvent, 4)
	s.streamMintConversation(t.Context(), ch, streamMintConversationParams{
		modelSet:       "other:model",
		agentIDHex:     "0xabc",
		conversationID: mintConversationTestConversationID,
	})

	var events []apptheory.SSEEvent
	for event := range ch {
		events = append(events, event)
	}
	if len(events) != 2 || events[0].Event != "conversation_start" || events[1].Event != "error" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestMintConversationPersistenceHelpers_UpdateStoredFields(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)

	tdb.qConv.On("Update", []string{"Messages"}).Return(nil).Once()
	tdb.qConv.On("Update", []string{"Messages", "Usage"}).Return(nil).Once()
	tdb.qConv.On("Update", []string{"Messages", "ProducedDeclarations", "Status", "CompletedAt"}).Return(nil).Once()

	s.updateMintConversationMessages(t.Context(), " 0xABC ", " "+mintConversationTestConversationID+" ", []soulMintConversationMessage{{Role: "user", Content: "hello"}})
	s.updateMintConversationTurn(t.Context(), " 0xABC ", " "+mintConversationTestConversationID+" ", []soulMintConversationMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}, models.AIUsage{Provider: "openai", Model: "gpt-4o-mini", TotalTokens: 12})
	s.updateMintConversationStatus(t.Context(), " 0xABC ", " "+mintConversationTestConversationID+" ", " Completed ", []soulMintConversationMessage{{Role: "assistant", Content: "done"}}, ` {"ok":true} `)

	if len(tdb.convModels) != 3 {
		t.Fatalf("expected 3 captured models, got %d", len(tdb.convModels))
	}
	if tdb.convModels[0].AgentID != "0xabc" || tdb.convModels[0].ConversationID != mintConversationTestConversationID || !strings.Contains(tdb.convModels[0].Messages, `"content":"hello"`) {
		t.Fatalf("unexpected messages update model: %#v", tdb.convModels[0])
	}
	if tdb.convModels[1].Usage.TotalTokens != 12 || !strings.Contains(tdb.convModels[1].Messages, `"assistant"`) {
		t.Fatalf("unexpected turn update model: %#v", tdb.convModels[1])
	}
	if tdb.convModels[2].Status != models.SoulMintConversationStatusCompleted || tdb.convModels[2].CompletedAt.IsZero() || tdb.convModels[2].ProducedDeclarations != `{"ok":true}` {
		t.Fatalf("unexpected status update model: %#v", tdb.convModels[2])
	}
}

func TestMintConversationHandleGuardsAndModelBranches(t *testing.T) {
	t.Parallel()
	testMintConversationHandleRequiresRegistrationID(t)
	testMintConversationHandleRequiresMessage(t)
	testMintConversationHandleRejectsLongMessage(t)
	testMintConversationHandleRejectsUnsupportedModel(t)
	testMintConversationHandleRejectsModelChangeForExistingConversation(t)
}

func TestMintConversationGetCompleteAndFinalizeGuards(t *testing.T) {
	t.Parallel()
	testMintConversationGetRequiresConversationID(t)
	testMintConversationGetConversationSuccess(t)
	testMintConversationCompleteRequiresConversationID(t)
	testMintConversationCompleteRejectsConversationNotInProgress(t)
	testMintConversationCompleteAcceptsStringDeclarations(t)
	testMintConversationCompleteAcceptsObjectDeclarations(t)
	testMintConversationBeginFinalizeRequiresBucketConfiguration(t)
	testMintConversationFinalizeRequiresRegistrationIDWithBucketConfigured(t)
}

func mintConversationGuardReg() models.SoulAgentRegistration {
	return models.SoulAgentRegistration{
		ID:               "reg-1",
		Username:         "alice",
		DomainNormalized: "example.com",
		AgentID:          "0x" + strings.Repeat("22", 32),
	}
}

func mintConversationHandleReg() models.SoulAgentRegistration {
	return models.SoulAgentRegistration{
		ID:               "reg-1",
		Username:         "alice",
		DomainNormalized: "example.com",
		AgentID:          "0x" + strings.Repeat("11", 32),
	}
}

func testMintConversationHandleRequiresRegistrationID(t *testing.T) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	_, err := s.handleSoulMintConversation(adminCtx())
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "registration id is required" {
		t.Fatalf("expected registration id error, got %#v", err)
	}
}

func testMintConversationHandleRequiresMessage(t *testing.T) {
	t.Helper()
	reg := mintConversationHandleReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	ctx.Request.Body = []byte(`{}`)
	_, err := s.handleSoulMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "message is required" {
		t.Fatalf("expected message required error, got %#v", err)
	}
}

func testMintConversationHandleRejectsLongMessage(t *testing.T) {
	t.Helper()
	reg := mintConversationHandleReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	body, _ := json.Marshal(soulMintConversationRequest{Message: strings.Repeat("x", 8193)})
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	ctx.Request.Body = body
	_, err := s.handleSoulMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "message is too long" {
		t.Fatalf("expected message length error, got %#v", err)
	}
}

func testMintConversationHandleRejectsUnsupportedModel(t *testing.T) {
	t.Helper()
	reg := mintConversationHandleReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{AgentID: reg.AgentID, ConversationID: mintConversationTestConversationID, Status: models.SoulMintConversationStatusInProgress}
	}).Once()
	body := mustMarshalJSON(t, soulMintConversationRequest{ConversationID: mintConversationTestConversationID, Model: "other:model", Message: "hello"})
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	ctx.Request.Body = body
	_, err := s.handleSoulMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "unsupported model set" {
		t.Fatalf("expected unsupported model error, got %#v", err)
	}
}

func testMintConversationHandleRejectsModelChangeForExistingConversation(t *testing.T) {
	t.Helper()
	reg := mintConversationHandleReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{
			AgentID:        reg.AgentID,
			ConversationID: mintConversationTestConversationID,
			Model:          "anthropic:claude-sonnet-4-20250514",
			Status:         models.SoulMintConversationStatusInProgress,
		}
	}).Once()
	body := mustMarshalJSON(t, soulMintConversationRequest{ConversationID: mintConversationTestConversationID, Model: "openai:gpt-4o-mini", Message: "hello"})
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	ctx.Request.Body = body
	_, err := s.handleSoulMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "cannot change model for an existing conversation" {
		t.Fatalf("expected model change error, got %#v", err)
	}
}

func testMintConversationGetRequiresConversationID(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	_, err := s.handleSoulGetMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "conversationId is required" {
		t.Fatalf("expected missing conversation id error, got %#v", err)
	}
}

func testMintConversationGetConversationSuccess(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{AgentID: reg.AgentID, ConversationID: mintConversationTestConversationID, Status: models.SoulMintConversationStatusInProgress}
	}).Once()
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID}
	resp, err := s.handleSoulGetMintConversation(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out models.SoulAgentMintConversation
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.ConversationID != mintConversationTestConversationID {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func testMintConversationCompleteRequiresConversationID(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	_, err := s.handleSoulCompleteMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "conversationId is required" {
		t.Fatalf("expected missing conversation id error, got %#v", err)
	}
}

func testMintConversationCompleteRejectsConversationNotInProgress(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{AgentID: reg.AgentID, ConversationID: mintConversationTestConversationID, Status: models.SoulMintConversationStatusCompleted}
	}).Once()
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID}
	_, err := s.handleSoulCompleteMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "conversation is not in progress" {
		t.Fatalf("expected conflict error, got %#v", err)
	}
}

func testMintConversationCompleteAcceptsStringDeclarations(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{AgentID: reg.AgentID, ConversationID: mintConversationTestConversationID, Status: models.SoulMintConversationStatusInProgress}
	}).Once()
	tdb.qConv.On("Update", []string{"Status", "ProducedDeclarations", "CompletedAt", "Usage"}).Return(nil).Once()
	declBytes := mustMarshalJSON(t, testMintConversationDecl())
	body := mustMarshalJSON(t, map[string]string{"declarations": string(declBytes)})
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID}
	ctx.Request.Body = body
	resp, err := s.handleSoulCompleteMintConversation(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out models.SoulAgentMintConversation
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Status != models.SoulMintConversationStatusCompleted || out.ProducedDeclarations != string(declBytes) {
		t.Fatalf("unexpected completed conversation: %#v", out)
	}
}

func testMintConversationCompleteAcceptsObjectDeclarations(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{AgentID: reg.AgentID, ConversationID: "conv-2", Status: models.SoulMintConversationStatusInProgress}
	}).Once()
	tdb.qConv.On("Update", []string{"Status", "ProducedDeclarations", "CompletedAt", "Usage"}).Return(nil).Once()
	body := mustMarshalJSON(t, map[string]any{"declarations": testMintConversationDecl()})
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID, "conversationId": "conv-2"}
	ctx.Request.Body = body
	resp, err := s.handleSoulCompleteMintConversation(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out models.SoulAgentMintConversation
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Status != models.SoulMintConversationStatusCompleted || !strings.Contains(out.ProducedDeclarations, `"selfDescription"`) {
		t.Fatalf("unexpected completed conversation: %#v", out)
	}
}

func testMintConversationBeginFinalizeRequiresBucketConfiguration(t *testing.T) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.soulPacks = nil
	_, err := s.handleSoulBeginFinalizeMintConversation(adminCtx())
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "soul registry bucket is not configured" {
		t.Fatalf("expected bucket error, got %#v", err)
	}
}

func testMintConversationFinalizeRequiresRegistrationIDWithBucketConfigured(t *testing.T) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)

	_, err := s.handleSoulFinalizeMintConversation(adminCtx())
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "registration id is required" {
		t.Fatalf("expected registration id error, got %#v", err)
	}
}

func TestMintConversationNoOpPersistenceHelpers(t *testing.T) {
	s := &Server{}
	s.updateMintConversationMessages(t.Context(), "0xabc", "conv", nil)
	s.updateMintConversationTurn(t.Context(), "0xabc", "conv", nil, models.AIUsage{})
	s.updateMintConversationStatus(t.Context(), "0xabc", "conv", "failed", nil, "")
}

func TestMintConversationDeclarationRoundTrip(t *testing.T) {
	decl := testMintConversationDecl()
	body, err := json.Marshal(decl)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, appErr := parseAndValidateMintConversationDeclarations(string(body))
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if got.SelfDescription.Purpose != decl.SelfDescription.Purpose {
		t.Fatalf("unexpected round-trip declaration: %#v", got)
	}
}

func TestDebitSoulMintConversationCredits_Branches(t *testing.T) {
	t.Parallel()

	t.Run("guard rails and zero credits", func(t *testing.T) {
		testDebitSoulMintConversationGuardRails(t)
	})

	t.Run("budget lookup and preflight conflicts", func(t *testing.T) {
		testDebitSoulMintConversationBudgetConflicts(t)
	})

	t.Run("successful debit uses target as default request id", func(t *testing.T) {
		testDebitSoulMintConversationSuccess(t)
	})

	t.Run("overage path condition failures and callback errors", func(t *testing.T) {
		testDebitSoulMintConversationOverageAndFailures(t)
	})
}

func newMintConversationDebitServer() (*Server, *ttmocks.MockExtendedDB, *ttmocks.MockQuery, *ttmocks.MockTransactionBuilder) {
	db, queries := newTestDBWithModelQueries("*models.InstanceBudgetMonth")
	tb := new(ttmocks.MockTransactionBuilder)
	db.TransactWriteBuilder = tb
	return &Server{store: store.New(db)}, db, queries[0], tb
}

func testDebitSoulMintConversationGuardRails(t *testing.T) {
	t.Helper()

	inst := &models.Instance{Slug: "inst1"}
	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	if _, appErr := (*Server)(nil).debitSoulMintConversationCredits(t.Context(), inst, "module", "target", "req", 1, now, nil); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected nil server error, got %#v", appErr)
	}

	s, _, _, _ := newMintConversationDebitServer()
	if _, appErr := s.debitSoulMintConversationCredits(t.Context(), nil, "module", "target", "req", 1, now, nil); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected nil instance error, got %#v", appErr)
	}
	if _, appErr := s.debitSoulMintConversationCredits(t.Context(), &models.Instance{Slug: " "}, "module", "target", "req", 1, now, nil); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected blank slug error, got %#v", appErr)
	}
	if _, appErr := s.debitSoulMintConversationCredits(t.Context(), inst, " ", "target", "req", 1, now, nil); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected blank module error, got %#v", appErr)
	}
	if credits, appErr := s.debitSoulMintConversationCredits(t.Context(), inst, "module", "target", "req", 0, now, nil); appErr != nil || credits != 0 {
		t.Fatalf("expected zero-credit noop, got credits=%d appErr=%#v", credits, appErr)
	}
}

func testDebitSoulMintConversationBudgetConflicts(t *testing.T) {
	t.Helper()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	inst := &models.Instance{Slug: "inst1"}

	s, _, qBudget, _ := newMintConversationDebitServer()
	qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, appErr := s.debitSoulMintConversationCredits(t.Context(), inst, "module", "target", "req", 5, now, nil); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected missing budget conflict, got %#v", appErr)
	}

	s, _, qBudget, _ = newMintConversationDebitServer()
	qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrConditionFailed).Once()
	if _, appErr := s.debitSoulMintConversationCredits(t.Context(), inst, "module", "target", "req", 5, now, nil); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected budget load failure, got %#v", appErr)
	}

	s, _, qBudget, _ = newMintConversationDebitServer()
	qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "inst1", Month: "2026-03", IncludedCredits: 4, UsedCredits: 3}
	}).Once()
	if _, appErr := s.debitSoulMintConversationCredits(t.Context(), inst, "module", "target", "req", 5, now, nil); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected insufficient credits conflict, got %#v", appErr)
	}
}

func testDebitSoulMintConversationSuccess(t *testing.T) {
	t.Helper()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	inst := &models.Instance{Slug: "Inst1"}
	s, db, qBudget, tb := newMintConversationDebitServer()

	qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "inst1", Month: "2026-03", IncludedCredits: 20, UsedCredits: 5}
	}).Once()
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("Put", mock.AnythingOfType("*models.UsageLedgerEntry"), mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		entry := testutil.RequireMockArg[*models.UsageLedgerEntry](t, args, 0)
		if entry.InstanceSlug != "inst1" || entry.RequestID != mintConversationTestConversationID || entry.Target != mintConversationTestConversationID {
			t.Fatalf("unexpected ledger entry routing fields: %#v", entry)
		}
		if entry.RequestedCredits != 5 || entry.IncludedDebitedCredits != 5 || entry.OverageDebitedCredits != 0 {
			t.Fatalf("unexpected debit split: %#v", entry)
		}
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.InstanceBudgetMonth"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()

	extraWritesCalled := false
	credits, appErr := s.debitSoulMintConversationCredits(t.Context(), inst, " soul.module ", mintConversationTestConversationID, "", 5, now, func(_ core.TransactionBuilder, requested int64) error {
		extraWritesCalled = true
		if requested != 5 {
			t.Fatalf("expected requested credits 5, got %d", requested)
		}
		return nil
	})
	if appErr != nil || credits != 5 || !extraWritesCalled {
		t.Fatalf("unexpected debit result: credits=%d appErr=%#v extraWritesCalled=%v", credits, appErr, extraWritesCalled)
	}
}

func testDebitSoulMintConversationOverageAndFailures(t *testing.T) {
	t.Helper()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)

	t.Run("allow overage succeeds", func(t *testing.T) {
		s, db, qBudget, tb := newMintConversationDebitServer()
		inst := &models.Instance{Slug: "inst1", OveragePolicy: "allow"}

		qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
			*dest = models.InstanceBudgetMonth{InstanceSlug: "inst1", Month: "2026-03", IncludedCredits: 2, UsedCredits: 2}
		}).Once()
		db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
		tb.On("Put", mock.AnythingOfType("*models.UsageLedgerEntry"), mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
			entry := testutil.RequireMockArg[*models.UsageLedgerEntry](t, args, 0)
			if entry.IncludedDebitedCredits != 0 || entry.OverageDebitedCredits != 5 || entry.BillingType != models.BillingTypeOverage {
				t.Fatalf("expected pure overage debit, got %#v", entry)
			}
		})
		tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.InstanceBudgetMonth"), mock.Anything, mock.Anything).Return(tb).Once()
		tb.On("Execute").Return(nil).Once()

		if credits, appErr := s.debitSoulMintConversationCredits(t.Context(), inst, "module", "target", "req-1", 5, now, nil); appErr != nil || credits != 5 {
			t.Fatalf("unexpected overage debit result: credits=%d appErr=%#v", credits, appErr)
		}
	})

	t.Run("transaction condition failure becomes conflict", func(t *testing.T) {
		s, db, qBudget, tb := newMintConversationDebitServer()
		inst := &models.Instance{Slug: "inst1"}

		qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
			*dest = models.InstanceBudgetMonth{InstanceSlug: "inst1", Month: "2026-03", IncludedCredits: 10, UsedCredits: 4}
		}).Once()
		db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
		tb.On("Put", mock.AnythingOfType("*models.UsageLedgerEntry"), mock.Anything).Return(tb).Once()
		tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.InstanceBudgetMonth"), mock.Anything, mock.Anything).Return(tb).Once()
		tb.On("Execute").Return(theoryErrors.ErrConditionFailed).Once()

		if _, appErr := s.debitSoulMintConversationCredits(t.Context(), inst, "module", "target", "req-1", 5, now, nil); appErr == nil || appErr.Code != appErrCodeConflict {
			t.Fatalf("expected condition-failed conflict, got %#v", appErr)
		}
	})

	t.Run("extra writes and execute errors become internal", func(t *testing.T) {
		s, db, qBudget, tb := newMintConversationDebitServer()
		inst := &models.Instance{Slug: "inst1"}

		qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
			*dest = models.InstanceBudgetMonth{InstanceSlug: "inst1", Month: "2026-03", IncludedCredits: 20, UsedCredits: 2}
		}).Twice()

		db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
		tb.On("Put", mock.AnythingOfType("*models.UsageLedgerEntry"), mock.Anything).Return(tb).Once()
		if _, appErr := s.debitSoulMintConversationCredits(t.Context(), inst, "module", "target", "req-1", 5, now, func(_ core.TransactionBuilder, _ int64) error {
			return errors.New("boom")
		}); appErr == nil || appErr.Code != appErrCodeInternal {
			t.Fatalf("expected extra write failure to map to internal error, got %#v", appErr)
		}

		db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
		tb.On("Put", mock.AnythingOfType("*models.UsageLedgerEntry"), mock.Anything).Return(tb).Once()
		tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.InstanceBudgetMonth"), mock.Anything, mock.Anything).Return(tb).Once()
		tb.On("Execute").Return(assertNotFound()).Once()
		if _, appErr := s.debitSoulMintConversationCredits(t.Context(), inst, "module", "target", "req-2", 5, now, nil); appErr == nil || appErr.Code != appErrCodeInternal {
			t.Fatalf("expected transaction execute failure to map to internal error, got %#v", appErr)
		}
	})
}

func TestMintConversationBeginAndFinalize_Success(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	qVersion := new(ttmocks.MockQuery)
	qCap := new(ttmocks.MockQuery)
	qBoundary := new(ttmocks.MockQuery)
	qBoundIdx := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb

	for typeName, q := range map[string]*ttmocks.MockQuery{
		"*models.SoulAgentVersion":              qVersion,
		"*models.SoulCapabilityAgentIndex":      qCap,
		"*models.SoulAgentBoundary":             qBoundary,
		"*models.SoulBoundaryKeywordAgentIndex": qBoundIdx,
		"*models.AuditLogEntry":                 qAudit,
	} {
		tdb.db.On("Model", mock.AnythingOfType(typeName)).Return(q).Maybe()
		addStandardMockQueryStubs(q)
	}

	qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("ConditionCheck", mock.AnythingOfType("*models.SoulAgentIdentity"), mock.Anything).Return(tb).Once()
	tb.On("Create", mock.AnythingOfType("*models.SoulAgentVersion"), mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
	qCap.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	s := newMintConversationServer(tdb)
	packs := &fakeSoulPackStore{}
	s.soulPacks = packs
	tdb.qIdentity.On("Update", mock.Anything, mock.Anything).Return(nil).Maybe()

	identity, key := testMintConversationIdentityAndKey()
	reg := models.SoulAgentRegistration{
		ID:               "reg-1",
		Username:         "alice",
		DomainNormalized: "example.com",
		AgentID:          identity.AgentID,
	}
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)

	decl := testMintConversationDecl()
	declBytes, err := json.Marshal(decl)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentMintConversation](t, args, 0)
		*dest = models.SoulAgentMintConversation{
			AgentID:              identity.AgentID,
			ConversationID:       "conv-1",
			Status:               models.SoulMintConversationStatusCompleted,
			ProducedDeclarations: string(declBytes),
		}
	}).Twice()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = *identity
	}).Twice()

	boundaryDigest := crypto.Keccak256([]byte(strings.TrimSpace(decl.Boundaries[0].Statement)))
	boundarySig, err := crypto.Sign(accounts.TextHash(boundaryDigest), key)
	if err != nil {
		t.Fatalf("Sign boundary: %v", err)
	}
	boundarySigHex := "0x" + hex.EncodeToString(boundarySig)
	boundarySigs := map[string]string{"b1": boundarySigHex}

	beginBody := mustMintConversationJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: boundarySigs})
	beginCtx := adminCtx()
	beginCtx.Params = map[string]string{"id": reg.ID, "conversationId": "conv-1"}
	beginCtx.Request.Body = beginBody

	beginResp, err := s.handleSoulBeginFinalizeMintConversation(beginCtx)
	if err != nil {
		t.Fatalf("begin finalize: %v", err)
	}
	beginOut := mustBeginFinalizeResponse(t, beginResp)

	digest, err := hex.DecodeString(strings.TrimPrefix(beginOut.DigestHex, "0x"))
	if err != nil {
		t.Fatalf("Decode digest: %v", err)
	}
	selfSig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		t.Fatalf("Sign finalize digest: %v", err)
	}
	selfSigHex := "0x" + hex.EncodeToString(selfSig)

	finalizeBody := mustMintConversationJSON(t, soulMintConversationFinalizeRequest{
		BoundarySignatures: boundarySigs,
		IssuedAt:           beginOut.IssuedAt,
		ExpectedVersion:    &beginOut.ExpectedVersion,
		SelfAttestation:    selfSigHex,
	})
	finalizeCtx := adminCtx()
	finalizeCtx.RequestID = "r2"
	finalizeCtx.Params = map[string]string{"id": reg.ID, "conversationId": "conv-1"}
	finalizeCtx.Request.Body = finalizeBody

	resp, err := s.handleSoulFinalizeMintConversation(finalizeCtx)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	out := mustFinalizeMintConversationResponse(t, resp)
	assertMintConversationFinalizePersisted(t, packs, identity.AgentID, out)
}

func assertMintConversationFinalizeRegistrationInputErrors(
	t *testing.T,
	s *Server,
	identity *models.SoulAgentIdentity,
	decl soulMintConversationProducedDeclarations,
	now time.Time,
) {
	t.Helper()
	if _, _, _, _, _, appErr := (*Server)(nil).buildMintConversationFinalizeV2Registration(identity.AgentID, identity, decl, nil, now, 1, "0x00"); appErr == nil || appErr.Message != provisionPhoneInternalError {
		t.Fatalf("expected nil server error, got %#v", appErr)
	}
	if _, _, _, _, _, appErr := s.buildMintConversationFinalizeV2Registration("", identity, decl, nil, now, 1, "0x00"); appErr == nil || appErr.Message != provisionPhoneInternalError {
		t.Fatalf("expected empty agent id error, got %#v", appErr)
	}
	if _, _, _, _, _, appErr := s.buildMintConversationFinalizeV2Registration(identity.AgentID, nil, decl, nil, now, 1, "0x00"); appErr == nil || appErr.Message != provisionPhoneInternalError {
		t.Fatalf("expected nil identity error, got %#v", appErr)
	}
	if _, _, _, _, _, appErr := s.buildMintConversationFinalizeV2Registration(identity.AgentID, identity, decl, nil, now, 0, "0x00"); appErr == nil || appErr.Message != "invalid version" {
		t.Fatalf("expected invalid version error, got %#v", appErr)
	}
}

func assertMintConversationFinalizeRegistrationSuccess(
	t *testing.T,
	identity *models.SoulAgentIdentity,
	reg map[string]any,
	digest []byte,
	capsNorm []string,
	claimLevels map[string]string,
	appErr *apptheory.AppError,
) {
	t.Helper()
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if reg["previousVersionUri"] != "s3://bucket/"+soulRegistrationVersionedS3Key(identity.AgentID, 1) {
		t.Fatalf("unexpected previousVersionUri: %#v", reg["previousVersionUri"])
	}
	lifecycle, _ := reg["lifecycle"].(map[string]any)
	if lifecycle["status"] != models.SoulAgentStatusActive {
		t.Fatalf("expected pending lifecycle to map to active, got %#v", lifecycle)
	}
	if reg["transparency"] == nil {
		t.Fatalf("expected default transparency object")
	}
	if len(digest) == 0 || len(capsNorm) != 1 || capsNorm[0] != "travel_planning" || claimLevels["travel_planning"] != soulClaimLevelSelfDeclared {
		t.Fatalf("unexpected finalize outputs: digest=%x caps=%#v claimLevels=%#v", digest, capsNorm, claimLevels)
	}
}

func mustMintConversationJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return body
}

func mustBeginFinalizeResponse(t *testing.T, resp *apptheory.Response) soulMintConversationFinalizeBeginResponse {
	t.Helper()
	var out soulMintConversationFinalizeBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("Unmarshal begin response: %v", err)
	}
	if out.ExpectedVersion != 0 || out.NextVersion != 1 || out.DigestHex == "" {
		t.Fatalf("unexpected begin finalize response: %#v", out)
	}
	return out
}

func mustFinalizeMintConversationResponse(t *testing.T, resp *apptheory.Response) soulMintConversationFinalizeResponse {
	t.Helper()
	var out soulMintConversationFinalizeResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("Unmarshal finalize response: %v", err)
	}
	if out.PublishedVersion != 1 || out.Agent.SelfDescriptionVersion != 1 {
		t.Fatalf("unexpected finalize response: %#v", out)
	}
	return out
}

func assertMintConversationFinalizePersisted(t *testing.T, packs *fakeSoulPackStore, agentID string, out soulMintConversationFinalizeResponse) {
	t.Helper()
	if _, ok := packs.objects[soulRegistrationS3Key(agentID)]; !ok {
		t.Fatalf("expected current registration artifact to be written: %#v", out)
	}
	if _, ok := packs.objects[soulRegistrationVersionedS3Key(agentID, 1)]; !ok {
		t.Fatalf("expected versioned registration artifact to be written: %#v", out)
	}
}
