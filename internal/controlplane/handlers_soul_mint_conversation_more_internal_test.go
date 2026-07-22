package controlplane

import (
	"bytes"
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
	apptheory "github.com/theory-cloud/apptheory/runtime"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const mintConversationTestConversationID = "conv-1"

type mintConversationTestDB struct {
	db           *ttmocks.MockExtendedDB
	qReg         *ttmocks.MockQuery
	qOp          *ttmocks.MockQuery
	qDomain      *ttmocks.MockQuery
	qInstance    *ttmocks.MockQuery
	qKey         *ttmocks.MockQuery
	qConv        *ttmocks.MockQuery
	qHosted      *ttmocks.MockQuery
	qMintIdem    *ttmocks.MockQuery
	qBudget      *ttmocks.MockQuery
	qIdentity    *ttmocks.MockQuery
	qAudit       *ttmocks.MockQuery
	qWalletIdx   *ttmocks.MockQuery
	qPromotion   *ttmocks.MockQuery
	qLifecycle   *ttmocks.MockQuery
	qWalletAgent *ttmocks.MockQuery
	qDomainAgent *ttmocks.MockQuery
	qCapAgent    *ttmocks.MockQuery
	qUser        *ttmocks.MockQuery
	qChannel     *ttmocks.MockQuery
	qENS         *ttmocks.MockQuery

	convModels          []*models.SoulAgentMintConversation
	auditModels         []*models.AuditLogEntry
	lifecycleModels     []*models.SoulAgentPromotionLifecycleEvent
	ensChannelModels    []*models.SoulAgentChannel
	ensResolutionModels []*models.SoulAgentENSResolution
	lastReg             *models.SoulAgentRegistration
}

func newMintConversationTestDB() *mintConversationTestDB {
	db := ttmocks.NewMockExtendedDB()
	tdb := &mintConversationTestDB{
		db:           db,
		qReg:         new(ttmocks.MockQuery),
		qOp:          new(ttmocks.MockQuery),
		qDomain:      new(ttmocks.MockQuery),
		qInstance:    new(ttmocks.MockQuery),
		qKey:         new(ttmocks.MockQuery),
		qConv:        new(ttmocks.MockQuery),
		qHosted:      new(ttmocks.MockQuery),
		qMintIdem:    new(ttmocks.MockQuery),
		qBudget:      new(ttmocks.MockQuery),
		qIdentity:    new(ttmocks.MockQuery),
		qAudit:       new(ttmocks.MockQuery),
		qWalletIdx:   new(ttmocks.MockQuery),
		qPromotion:   new(ttmocks.MockQuery),
		qLifecycle:   new(ttmocks.MockQuery),
		qWalletAgent: new(ttmocks.MockQuery),
		qDomainAgent: new(ttmocks.MockQuery),
		qCapAgent:    new(ttmocks.MockQuery),
		qUser:        new(ttmocks.MockQuery),
		qChannel:     new(ttmocks.MockQuery),
		qENS:         new(ttmocks.MockQuery),
	}

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(tdb.qReg).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(tdb.qOp).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(tdb.qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(tdb.qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(tdb.qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(tdb.qConv).Maybe().Run(func(args mock.Arguments) {
		if conv, ok := args.Get(0).(*models.SoulAgentMintConversation); ok && conv != nil {
			copy := *conv
			tdb.convModels = append(tdb.convModels, &copy)
		}
	})
	db.On("Model", mock.AnythingOfType("*models.HostedGenesisSession")).Return(tdb.qHosted).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulMintConversationIdempotency")).Return(tdb.qMintIdem).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(tdb.qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(tdb.qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(tdb.qAudit).Maybe().Run(captureMintConversationAuditModel(tdb))
	db.On("Model", mock.AnythingOfType("*models.WalletIndex")).Return(tdb.qWalletIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(tdb.qPromotion).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentPromotionLifecycleEvent")).Return(tdb.qLifecycle).Maybe().Run(captureMintConversationLifecycleModel(tdb))
	db.On("Model", mock.AnythingOfType("*models.SoulWalletAgentIndex")).Return(tdb.qWalletAgent).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulDomainAgentIndex")).Return(tdb.qDomainAgent).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(tdb.qCapAgent).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(tdb.qUser).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(tdb.qChannel).Maybe().Run(func(args mock.Arguments) {
		if ch, ok := args.Get(0).(*models.SoulAgentChannel); ok && ch != nil && strings.TrimSpace(ch.Identifier) != "" {
			copy := *ch
			tdb.ensChannelModels = append(tdb.ensChannelModels, &copy)
		}
	})
	db.On("Model", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(tdb.qENS).Maybe().Run(func(args mock.Arguments) {
		if res, ok := args.Get(0).(*models.SoulAgentENSResolution); ok && res != nil && strings.TrimSpace(res.ENSName) != "" {
			copy := *res
			tdb.ensResolutionModels = append(tdb.ensResolutionModels, &copy)
		}
	})

	for _, q := range []*ttmocks.MockQuery{tdb.qReg, tdb.qOp, tdb.qDomain, tdb.qInstance, tdb.qKey, tdb.qConv, tdb.qHosted, tdb.qMintIdem, tdb.qBudget, tdb.qIdentity, tdb.qAudit, tdb.qWalletIdx, tdb.qPromotion, tdb.qLifecycle, tdb.qWalletAgent, tdb.qDomainAgent, tdb.qCapAgent, tdb.qUser, tdb.qChannel, tdb.qENS} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		if q != tdb.qConv {
			q.On("All", mock.Anything).Return(nil).Maybe()
		}
	}
	tdb.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qWalletAgent.On("First", mock.AnythingOfType("*models.SoulWalletAgentIndex")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qDomainAgent.On("First", mock.AnythingOfType("*models.SoulDomainAgentIndex")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qCapAgent.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Maybe().Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.User)
		if !ok || dest == nil {
			return
		}
		*dest = models.User{
			Username:       "alice",
			Role:           models.RoleCustomer,
			Approved:       true,
			ApprovalStatus: models.UserApprovalStatusApproved,
		}
	})

	return tdb
}

func captureMintConversationAuditModel(tdb *mintConversationTestDB) func(mock.Arguments) {
	return func(args mock.Arguments) {
		if entry, ok := args.Get(0).(*models.AuditLogEntry); ok && entry != nil {
			copy := *entry
			tdb.auditModels = append(tdb.auditModels, &copy)
		}
	}
}

func captureMintConversationLifecycleModel(tdb *mintConversationTestDB) func(mock.Arguments) {
	return func(args mock.Arguments) {
		if event, ok := args.Get(0).(*models.SoulAgentPromotionLifecycleEvent); ok && event != nil {
			copy := *event
			tdb.lifecycleModels = append(tdb.lifecycleModels, &copy)
		}
	}
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

func stubHostedGenesisAssistantRunner(t *testing.T, s *Server, response string, runErr error) {
	t.Helper()
	s.hostedGenesisAssistantRunner = func(_ context.Context, in hostedGenesisAssistantRunInput) (hostedGenesisAssistantRunResult, error) {
		if strings.TrimSpace(in.apiKey) == "" || strings.TrimSpace(in.modelSet) == "" || strings.TrimSpace(in.systemPrompt) == "" {
			t.Fatalf("hosted genesis assistant runner received incomplete safe input: %#v", in)
		}
		if len(in.messages) == 0 || strings.TrimSpace(in.messages[len(in.messages)-1].Content) == "" {
			t.Fatalf("hosted genesis assistant runner received no accepted user turn: %#v", in.messages)
		}
		if strings.Contains(in.systemPrompt, mintConversationInstanceReadTestRawKey) {
			t.Fatalf("hosted genesis assistant prompt leaked instance key")
		}
		return hostedGenesisAssistantRunResult{
			fullResponse: response,
			usage:        models.AIUsage{Provider: "test", Model: in.modelSet, InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		}, runErr
	}
}

// stubHostedGenesisMicroVMDispatcher wires a stub MicroVMDispatcher that records
// a valid MicroVM dispatcher plus queue enqueue seam. It asserts the sync
// assistant runner is NOT invoked so the accept path is proven
// queue-handoff-only.
func stubHostedGenesisMicroVMDispatcher(t *testing.T, s *Server) *stubMicroVMDispatcher {
	t.Helper()
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		dispatcher.queueCalls++
		dispatcher.lastQueue = msg
		return nil
	}
	// Guard the synchronous runner seam: H1.2 makes the production accept path
	// queue-handoff-only, so the sync assistant runner must never be reached.
	s.hostedGenesisAssistantRunner = func(_ context.Context, _ hostedGenesisAssistantRunInput) (hostedGenesisAssistantRunResult, error) {
		t.Fatalf("synchronous assistant runner must not be invoked on the dispatch-only accept path")
		return hostedGenesisAssistantRunResult{}, nil
	}
	return dispatcher
}

type stubMicroVMDispatcher struct {
	t                 *testing.T
	calls             int
	reconcileCalls    int
	prepareFreshCalls int
	invokeCalls       int
	lastBinding       hostedgenesis.MicroVMSessionBinding
	dispatchErr       error
	reconcileErr      error
	prepareFreshErr   error
	invokeErr         error
	queueCalls        int
	lastQueue         hostedgenesis.QueueMessage
	// observedState is the lifecycle state the stub reports from a controller
	// get reconciliation (defaults to running/non-terminal).
	observedState runtimemicrovm.LifecycleState
	// expired forces the stub's reconcile to report an expired (dead) session:
	// Terminal=true even when observedState is non-terminal, mirroring the
	// production seam's ExpiresAt-in-the-past mapping. H1.4 covers dead/expired
	// VM sessions, not only terminated/failed lifecycle states.
	expired bool
}

func (d *stubMicroVMDispatcher) PrepareFreshMicroVMRun(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding, previous hostedgenesis.MicroVMLifecycleRef) (hostedgenesis.MicroVMDispatchResult, error) {
	d.t.Helper()
	d.prepareFreshCalls++
	d.lastBinding = binding
	if d.prepareFreshErr != nil {
		return hostedgenesis.MicroVMDispatchResult{}, d.prepareFreshErr
	}
	if strings.TrimSpace(requestID) == "" || previous.MicroVMID == "" {
		d.t.Fatalf("fresh preparation requires request id and previous MicroVM id")
	}
	resp := runtimemicrovm.ControllerResponse{
		Command:           runtimemicrovm.CommandGet,
		RequestID:         requestID,
		TenantID:          binding.TenantID(),
		Namespace:         hostedgenesis.MicroVMNamespace,
		SessionID:         strings.TrimSpace(binding.ConversationID),
		State:             runtimemicrovm.StateRunning,
		DesiredState:      runtimemicrovm.StateRunning,
		LifecycleState:    runtimemicrovm.StateRunning,
		MicroVMID:         "mv-fresh-" + strings.TrimSpace(binding.ConversationID),
		ProviderMicroVMID: "mv-fresh-" + strings.TrimSpace(binding.ConversationID),
		LastAction:        runtimemicrovm.CommandGet,
		LastTransition:    time.Now().UTC(),
		RegistryVersion:   previous.RegistryVersion + 1,
	}
	ref, err := hostedgenesis.MicroVMLifecycleRefFromResponse(binding, resp, time.Now().UTC())
	if err != nil {
		d.t.Fatalf("stub fresh dispatcher failed to build lifecycle ref: %v", err)
	}
	ref.ImageRef = "arn:aws:lambda:us-east-1:123456789012:microvm-image/hosted-genesis"
	ref.ImageVersion = "29"
	ref.ExecutionRoleARN = "arn:aws:iam::123456789012:role/hosted-genesis-current"
	ref.MaximumDurationSeconds = 28800
	ref.RuntimeLogGroup = "/aws/lambda/microvms/hosted-genesis-current"
	return hostedgenesis.MicroVMDispatchResult{LifecycleRef: ref, SessionID: resp.SessionID}, nil
}

func (d *stubMicroVMDispatcher) InvokeMicroVMTurn(_ context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) error {
	d.t.Helper()
	d.invokeCalls++
	d.lastBinding = binding
	if strings.TrimSpace(requestID) == "" {
		d.t.Fatalf("stub fresh invoke received empty request id")
	}
	return d.invokeErr
}

func (d *stubMicroVMDispatcher) DispatchMicroVMRun(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.t.Helper()
	d.calls++
	d.lastBinding = binding
	if d.dispatchErr != nil {
		return hostedgenesis.MicroVMDispatchResult{}, d.dispatchErr
	}
	if err := binding.Validate(); err != nil {
		d.t.Fatalf("stub dispatcher received invalid binding: %v", err)
	}
	if strings.TrimSpace(requestID) == "" {
		d.t.Fatalf("stub dispatcher received empty request id")
	}
	resp := runtimemicrovm.ControllerResponse{
		Command:           runtimemicrovm.CommandRun,
		RequestID:         requestID,
		TenantID:          binding.TenantID(),
		Namespace:         hostedgenesis.MicroVMNamespace,
		SessionID:         strings.TrimSpace(binding.ConversationID),
		State:             runtimemicrovm.StateRunning,
		DesiredState:      runtimemicrovm.StateRunning,
		LifecycleState:    runtimemicrovm.StateRunning,
		MicroVMID:         "mv-stub-" + strings.TrimSpace(binding.ConversationID),
		ProviderMicroVMID: "mv-stub-" + strings.TrimSpace(binding.ConversationID),
		LastAction:        runtimemicrovm.CommandRun,
		LastTransition:    time.Now().UTC(),
		RegistryVersion:   1,
	}
	ref, err := hostedgenesis.MicroVMLifecycleRefFromResponse(binding, resp, time.Now().UTC())
	if err != nil {
		d.t.Fatalf("stub dispatcher failed to build lifecycle ref: %v", err)
	}
	return hostedgenesis.MicroVMDispatchResult{LifecycleRef: ref, SessionID: resp.SessionID}, nil
}

// ReconcileMicroVM is the stub's controller get reconciliation. It mirrors the
// production seam: it fails closed on a configured reconcileErr, otherwise it
// reports the configured observedState (defaulting to running) and reconciles
// the lifecycle ref via the canonical ReconcileMicroVMRegistryStatus so the
// control-plane reconciliation path observes the same shape production does.
func (d *stubMicroVMDispatcher) ReconcileMicroVM(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding, ref hostedgenesis.MicroVMLifecycleRef) (hostedgenesis.MicroVMReconcileResult, error) {
	d.t.Helper()
	d.reconcileCalls++
	d.lastBinding = binding
	if d.reconcileErr != nil {
		return hostedgenesis.MicroVMReconcileResult{}, d.reconcileErr
	}
	if err := binding.Validate(); err != nil {
		d.t.Fatalf("stub dispatcher received invalid binding: %v", err)
	}
	if strings.TrimSpace(requestID) == "" {
		d.t.Fatalf("stub dispatcher received empty request id")
	}
	observed := d.observedState
	if observed == "" {
		observed = runtimemicrovm.StateRunning
	}
	status := runtimemicrovm.SessionStatus{
		TenantID:        binding.TenantID(),
		Namespace:       hostedgenesis.MicroVMNamespace,
		SessionID:       strings.TrimSpace(binding.ConversationID),
		State:           observed,
		DesiredState:    observed,
		LifecycleState:  observed,
		MicroVMID:       ref.MicroVMID,
		LastAction:      runtimemicrovm.CommandGet,
		LastTransition:  time.Now().UTC(),
		RegistryVersion: ref.RegistryVersion,
	}
	reconciled, err := hostedgenesis.ReconcileMicroVMRegistryStatus(binding, ref, status)
	if err != nil {
		d.t.Fatalf("stub dispatcher failed to reconcile lifecycle ref: %v", err)
	}
	terminal := runtimemicrovm.IsTerminalState(reconciled.LifecycleState)
	if d.expired {
		// Mirror the production seam: an expired session is terminal (dead) even
		// when its observed lifecycle state is non-terminal.
		terminal = true
	}
	return hostedgenesis.MicroVMReconcileResult{
		LifecycleRef: reconciled,
		SessionID:    strings.TrimSpace(binding.ConversationID),
		Terminal:     terminal,
	}, nil
}

func stubMintConversationRegistration(t *testing.T, tdb *mintConversationTestDB, reg models.SoulAgentRegistration) {
	t.Helper()

	tdb.qReg.On("First", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentRegistration)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentRegistration, got %#v", args.Get(0))
		}
		*dest = reg
		cp := reg
		tdb.lastReg = &cp
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
		SchemaVersion:   hostedgenesis.DeclarationSchemaVersionV2,
		GuidanceVersion: hostedgenesis.GuidanceVersionV2,
		SelfDescription: soul.SelfDescriptionV2{
			Purpose:      "Help users plan travel with explicit limitations.",
			AuthoredBy:   "agent",
			MintingModel: "openai:gpt-5.4",
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

func TestRequireMintConversationFinalizeActiveIdentityAllowsPendingHosted(t *testing.T) {
	t.Parallel()

	identity := testMintConversationIdentity()
	if appErr := requireMintConversationFinalizeActiveIdentity(identity); appErr != nil {
		t.Fatalf("expected pending hosted identity to pass, got %#v", appErr)
	}

	identity.Status = models.SoulAgentStatusActive
	identity.LifecycleStatus = models.SoulAgentStatusActive
	if appErr := requireMintConversationFinalizeActiveIdentity(identity); appErr != nil {
		t.Fatalf("expected active identity to pass, got %#v", appErr)
	}

	identity.Status = models.SoulAgentStatusSuspended
	identity.LifecycleStatus = models.SoulAgentStatusSuspended
	if appErr := requireMintConversationFinalizeActiveIdentity(identity); appErr == nil || appErr.Code != "app.conflict" {
		t.Fatalf("expected suspended identity conflict, got %#v", appErr)
	}
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

	t.Run("detached stream context ignores request cancellation", func(t *testing.T) {
		testMintConversationDetachedContext(t)
	})

	t.Run("emit event never blocks when stream is unavailable", func(t *testing.T) {
		testMintConversationEmitEvent(t)
	})
}

func testMintConversationProducedDeclarationsBranches(t *testing.T, now time.Time) {
	t.Helper()

	if _, appErr := buildMintConversationProducedDeclarationsWithOptions(llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{Purpose: "short", AuthoredBy: "agent"},
	}, now, "openai:gpt-5.4", nil, false); appErr == nil || appErr.Message != string(hostedgenesis.DeclarationCodeSelfDescription) {
		t.Fatalf("expected selfDescription error, got %#v", appErr)
	}
	decl, appErr := buildMintConversationProducedDeclarationsWithOptions(llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{Purpose: "A sufficiently long purpose string.", AuthoredBy: "agent"},
		Capabilities:    []soul.CapabilityV2{{Capability: "", Scope: "skip"}},
		Boundaries:      []llm.MintConversationBoundaryDraft{{Category: "refusal", Statement: "I will not impersonate humans."}},
	}, now, "openai:gpt-5.4", []string{"travel_planning"}, false)
	if appErr != nil {
		t.Fatalf("expected declared capabilities to fill empty extracted capabilities, got %#v", appErr)
	}
	if len(decl.Capabilities) != 1 || decl.Capabilities[0].Capability != "travel_planning" || len(decl.Boundaries) != 1 {
		t.Fatalf("expected declared capability with retained valid boundary, got %#v", decl)
	}

	_, appErr = buildMintConversationProducedDeclarationsWithOptions(llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{Purpose: "A sufficiently long purpose string.", AuthoredBy: "agent"},
		Capabilities:    []soul.CapabilityV2{{Capability: "", Scope: "skip"}},
		Boundaries:      []llm.MintConversationBoundaryDraft{{Category: "refusal", Statement: "I will not impersonate humans."}},
	}, now, "openai:gpt-5.4", nil, false)
	if appErr == nil || appErr.Message != string(hostedgenesis.DeclarationCodeCapabilities) {
		t.Fatalf("expected required capabilities error, got %#v", appErr)
	}

	_, appErr = buildMintConversationProducedDeclarationsWithOptions(llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{Purpose: "A sufficiently long purpose string.", AuthoredBy: "agent"},
		Capabilities:    []soul.CapabilityV2{{Capability: "travel_planning", Scope: "Draft itineraries."}},
		Boundaries:      []llm.MintConversationBoundaryDraft{{Category: "", Statement: "skip"}},
	}, now, "openai:gpt-5.4", nil, false)
	if appErr == nil || appErr.Message != string(hostedgenesis.DeclarationCodeBoundariesBad) {
		t.Fatalf("expected invalid boundaries error, got %#v", appErr)
	}

	decl, appErr = buildMintConversationProducedDeclarationsWithOptions(llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{Purpose: "A sufficiently long purpose string.", AuthoredBy: "agent"},
		Capabilities:    []soul.CapabilityV2{{Capability: "travel_planning", Scope: "Draft itineraries."}},
		Boundaries:      []llm.MintConversationBoundaryDraft{{Category: "refusal", Statement: "I will not impersonate humans."}},
	}, now, "openai:gpt-5.4", nil, false)
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
	if _, appErr := parseAndValidateMintConversationDeclarations("{"); appErr == nil || appErr.Message != string(hostedgenesis.DeclarationCodeInvalid) {
		t.Fatalf("expected json error, got %#v", appErr)
	}
	if _, appErr := parseAndValidateMintConversationDeclarations(`{"selfDescription":{"purpose":"A sufficiently long purpose string.","authoredBy":"agent"},"boundaries":[{"id":"b1","category":"refusal","statement":"I will not impersonate humans.","addedAt":"2026-03-05T12:00:00Z","addedInVersion":"1","signature":"0x00"}],"transparency":{}}`); appErr == nil || appErr.Message != string(hostedgenesis.DeclarationCodeCapabilities) {
		t.Fatalf("expected capabilities error, got %#v", appErr)
	}
	if _, appErr := parseAndValidateMintConversationDeclarations(`{"selfDescription":{"purpose":"A sufficiently long purpose string.","authoredBy":"agent"},"capabilities":[],"boundaries":[{"id":"b1","category":"refusal","statement":"I will not impersonate humans.","addedAt":"2026-03-05T12:00:00Z","addedInVersion":"1","signature":"0x00"}],"transparency":{}}`); appErr != nil {
		t.Fatalf("expected empty capabilities array to validate, got %#v", appErr)
	}
	if _, appErr := parseAndValidateMintConversationDeclarations(`{"selfDescription":{"purpose":"A sufficiently long purpose string.","authoredBy":"agent"},"capabilities":[{"capability":"travel_planning","scope":"Draft itineraries.","claimLevel":"self-declared"}],"transparency":{}}`); appErr == nil || appErr.Message != string(hostedgenesis.DeclarationCodeBoundaries) {
		t.Fatalf("expected boundaries error, got %#v", appErr)
	}
	if _, appErr := parseAndValidateMintConversationDeclarations(`{"selfDescription":{"purpose":"A sufficiently long purpose string.","authoredBy":"agent"},"capabilities":[{"capability":"travel_planning","scope":"Draft itineraries.","claimLevel":"self-declared"}],"boundaries":[{"id":"b1","category":"refusal","statement":"I will not impersonate humans.","addedAt":"2026-03-05T12:00:00Z","addedInVersion":"1","signature":"0x00"}]}`); appErr == nil || appErr.Message != string(hostedgenesis.DeclarationCodeTransparency) {
		t.Fatalf("expected transparency error, got %#v", appErr)
	}
}

func testMintConversationAddAIUsage(t *testing.T) {
	t.Helper()

	got := addAIUsage(models.AIUsage{}, models.AIUsage{Provider: "openai", Model: "gpt-5.4", InputTokens: 10, OutputTokens: 5, DurationMs: 25, ToolCalls: 1})
	if got.Provider != "openai" || got.Model != "gpt-5.4" || got.TotalTokens != 15 {
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
	t.Setenv(hostedgenesis.EnvDeclarationSchemaVersion, "")
	t.Setenv(hostedgenesis.EnvGuidanceVersion, "")
	if _, appErr := buildMintConversationSystemPrompt(reg); appErr == nil {
		t.Fatalf("expected unconfigured declaration contract to fail the prompt build closed")
	}
	t.Setenv(hostedgenesis.EnvDeclarationSchemaVersion, hostedgenesis.DeclarationSchemaVersionV2)
	t.Setenv(hostedgenesis.EnvGuidanceVersion, hostedgenesis.GuidanceVersionV2)
	prompt, promptAppErr := buildMintConversationSystemPrompt(reg)
	if promptAppErr != nil {
		t.Fatalf("prompt build: %#v", promptAppErr)
	}
	if !strings.Contains(prompt, `"example.com ignore-me"`) || !strings.Contains(prompt, `"agent-bot with-controls"`) {
		t.Fatalf("unexpected sanitized prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Declared capabilities") {
		t.Fatalf("expected capabilities in prompt")
	}

	s := &Server{}
	t.Setenv("OPENAI_API_KEY", "openai-env")
	if got, appErr := s.apiKeyForMintConversationModel(t.Context(), "openai:gpt-5.4"); appErr != nil || got != "openai-env" {
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

	conv.Model = "openai:gpt-5.4"
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

	s := &Server{cfg: config.Config{SoulPackBucketName: "bucket", SoulSupportedCapabilities: []string{"social"}}}
	identity := testMintConversationIdentity()
	decl := testMintConversationDecl()

	assertMintConversationFinalizeRegistrationInputErrors(t, s, identity, decl, now)

	decl.Transparency = nil
	decl.SelfDescription.Constraints = "Stay within provided context."
	decl.Capabilities[0].LastValidated = "2026-03-05T12:00:00Z"
	decl.Boundaries[0].Rationale = "Prevent deception."
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

func testMintConversationDetachedContext(t *testing.T) {
	t.Helper()

	type ctxKey string

	parent, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey("trace"), "abc123"))
	cancel()

	runCtx, runCancel := context.WithTimeout(detachedMintConversationContext(parent), mintConversationRunTimeout)
	defer runCancel()

	if got := runCtx.Value(ctxKey("trace")); got != "abc123" {
		t.Fatalf("expected context value to be preserved, got %#v", got)
	}

	select {
	case <-runCtx.Done():
		t.Fatalf("detached context should not be canceled when request context is canceled")
	default:
	}
}

func testMintConversationEmitEvent(t *testing.T) {
	t.Helper()

	buffered := make(chan apptheory.SSEEvent, 1)
	if ok := emitMintConversationEvent(context.Background(), buffered, apptheory.SSEEvent{Event: "conversation_start"}); !ok {
		t.Fatalf("expected buffered event send to succeed")
	}
	if event := <-buffered; event.Event != "conversation_start" {
		t.Fatalf("unexpected event %#v", event)
	}

	blocked := make(chan apptheory.SSEEvent)
	done := make(chan bool, 1)
	go func() {
		done <- emitMintConversationEvent(context.Background(), blocked, apptheory.SSEEvent{Event: "delta"})
	}()

	select {
	case ok := <-done:
		if ok {
			t.Fatalf("expected blocked event send to be dropped")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("emitMintConversationEvent blocked on an unavailable consumer")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if ok := emitMintConversationEvent(canceled, blocked, apptheory.SSEEvent{Event: "error"}); ok {
		t.Fatalf("expected canceled context send to return false")
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
	}, models.AIUsage{Provider: "openai", Model: "gpt-5.4", TotalTokens: 12})
	s.updateMintConversationStatus(t.Context(), " 0xABC ", " "+mintConversationTestConversationID+" ", " Completed ", []soulMintConversationMessage{{Role: "assistant", Content: "done"}}, ` {"ok":true} `)

	if len(tdb.convModels) != 3 {
		t.Fatalf("expected 3 captured models, got %d", len(tdb.convModels))
	}
	if tdb.convModels[0].AgentID != "0xabc" || tdb.convModels[0].ConversationID != mintConversationTestConversationID || !strings.Contains(decodeMintConversationBlob(tdb.convModels[0].Messages), `"content":"hello"`) || tdb.convModels[0].Status != models.SoulMintConversationStatusInProgress {
		t.Fatalf("unexpected messages update model: %#v", tdb.convModels[0])
	}
	if tdb.convModels[1].Usage.TotalTokens != 12 || !strings.Contains(decodeMintConversationBlob(tdb.convModels[1].Messages), `"assistant"`) || tdb.convModels[1].Status != models.SoulMintConversationStatusInProgress {
		t.Fatalf("unexpected turn update model: %#v", tdb.convModels[1])
	}
	if tdb.convModels[2].Status != models.SoulMintConversationStatusCompleted || tdb.convModels[2].CompletedAt.IsZero() || decodeMintConversationBlob(tdb.convModels[2].ProducedDeclarations) != `{"ok":true}` {
		t.Fatalf("unexpected status update model: %#v", tdb.convModels[2])
	}
}

func TestMintConversationHandleGuardsAndModelBranches(t *testing.T) {
	t.Parallel()
	testMintConversationHandleRequiresRegistrationID(t)
	testMintConversationHandleRequiresMessage(t)
	testMintConversationHandleRejectsLongMessage(t)
	testMintConversationHandleRejectsPublishedRegistration(t)
	testMintConversationHandleRejectsUnsupportedModel(t)
	testMintConversationHandleRejectsModelChangeForExistingConversation(t)
}

func TestMintConversationGetCompleteAndFinalizeGuards(t *testing.T) {
	t.Parallel()
	testMintConversationGetRequiresConversationID(t)
	testMintConversationGetConversationSuccess(t)
	testMintConversationGetRequiresDomainOwnership(t)
	testMintConversationCompleteRequiresConversationID(t)
	testMintConversationCompleteRejectsFailedConversationState(t)
	testMintConversationCompleteReturnsCompletedConversationReplay(t)
	testMintConversationCompleteRejectsPublishedRegistration(t)
	testMintConversationCompleteRejectsMissingAssistantTurn(t)
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

func mintConversationDurableAssistantMessagesJSON() string {
	return `[{"role":"user","content":"describe yourself"},{"role":"assistant","content":"done"}]`
}

func testMintConversationHandleRequiresRegistrationID(t *testing.T) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	_, err := s.handleSoulMintConversation(adminCtx())
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	ctx.Request.Body = []byte(`{}`)
	_, err := s.handleSoulMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	body, _ := json.Marshal(soulMintConversationRequest{Message: strings.Repeat("x", 8193)})
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	ctx.Request.Body = body
	_, err := s.handleSoulMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok || appErr.Message != "message is too long" {
		t.Fatalf("expected message length error, got %#v", err)
	}
}

func testMintConversationHandleRejectsPublishedRegistration(t *testing.T) {
	t.Helper()
	reg := mintConversationHandleReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()
	identity.AgentID = reg.AgentID
	identity.SelfDescriptionVersion = 1

	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationIdentity(t, tdb, identity, nil)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	ctx.Request.Body = mustMarshalJSON(t, soulMintConversationRequest{Message: "hello"})

	_, err := s.handleSoulMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok || appErr.Message != soulMintConversationAlreadyPublishedMessage {
		t.Fatalf("expected published registration conflict, got %#v", err)
	}
}

func testMintConversationHandleRejectsUnsupportedModel(t *testing.T) {
	t.Helper()
	reg := mintConversationHandleReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
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
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{
			AgentID:        reg.AgentID,
			ConversationID: mintConversationTestConversationID,
			Model:          "anthropic:claude-sonnet-4-6",
			Status:         models.SoulMintConversationStatusInProgress,
		}
	}).Once()
	body := mustMarshalJSON(t, soulMintConversationRequest{ConversationID: mintConversationTestConversationID, Model: "openai:gpt-5.4", Message: "hello"})
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	ctx.Request.Body = body
	_, err := s.handleSoulMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	_, err := s.handleSoulGetMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
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

func testMintConversationGetRequiresDomainOwnership(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.Domain)
		if !ok || dest == nil {
			t.Fatalf("expected *models.Domain, got %#v", args.Get(0))
		}
		*dest = models.Domain{
			Domain:       reg.DomainNormalized,
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
			Owner: "bob",
		}
	}).Once()

	ctx := &apptheory.Context{
		AuthIdentity: reg.Username,
		Params: map[string]string{
			"id":             reg.ID,
			"conversationId": mintConversationTestConversationID,
		},
	}

	_, err := s.handleSoulGetMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok || appErr.Code != "app.forbidden" {
		t.Fatalf("expected domain ownership failure, got %#v", err)
	}
}

func testMintConversationCompleteRequiresConversationID(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID}
	_, err := s.handleSoulCompleteMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok || appErr.Message != "conversationId is required" {
		t.Fatalf("expected missing conversation id error, got %#v", err)
	}
}

func testMintConversationCompleteRejectsFailedConversationState(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{AgentID: reg.AgentID, ConversationID: mintConversationTestConversationID, Status: models.SoulMintConversationStatusFailed}
	}).Once()
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID}
	_, err := s.handleSoulCompleteMintConversation(ctx)
	appErr := requireAppTheoryError(t, err)
	assertMintConversationCompletionConflictDetails(t, appErr, appErrCodeConflict, http.StatusConflict, models.SoulMintConversationStatusFailed, false, false, soulMintConversationCompleteReasonInvalidState)
}

func testMintConversationCompleteReturnsCompletedConversationReplay(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()
	identity.AgentID = reg.AgentID
	identity.SelfDescriptionVersion = 1
	declBytes := mustMarshalJSON(t, testMintConversationDecl())
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationIdentity(t, tdb, identity, nil)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{
			AgentID:              reg.AgentID,
			ConversationID:       mintConversationTestConversationID,
			Status:               models.SoulMintConversationStatusCompleted,
			ProducedDeclarations: encodeMintConversationBlob(string(declBytes)),
		}
	}).Once()
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID}
	resp, err := s.handleSoulCompleteMintConversation(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out models.SoulAgentMintConversation
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Status != http.StatusOK || out.Status != models.SoulMintConversationStatusCompleted || out.ProducedDeclarations != string(declBytes) {
		t.Fatalf("expected completed conversation replay, status=%d out=%#v", resp.Status, out)
	}
	tdb.qConv.AssertNumberOfCalls(t, "Update", 0)
}

func testMintConversationCompleteRejectsPublishedRegistration(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()
	identity.AgentID = reg.AgentID
	identity.SelfDescriptionVersion = 1

	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationIdentity(t, tdb, identity, nil)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID}

	_, err := s.handleSoulCompleteMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok || appErr.Message != soulMintConversationAlreadyPublishedMessage {
		t.Fatalf("expected published registration conflict, got %#v", err)
	}
}

func testMintConversationCompleteRejectsMissingAssistantTurn(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{
			AgentID:        reg.AgentID,
			ConversationID: mintConversationTestConversationID,
			Status:         models.SoulMintConversationStatusInProgress,
			Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe yourself"}]`),
		}
	}).Once()
	body := mustMarshalJSON(t, map[string]any{"declarations": testMintConversationDecl()})
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID}
	ctx.Request.Body = body
	_, err := s.handleSoulCompleteMintConversation(ctx)
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok || appErr.Code != appErrCodeConflict || appErr.Message != "conversation has no completed assistant turn" {
		t.Fatalf("expected durable assistant-turn conflict, got %#v", err)
	}
	tdb.qConv.AssertNumberOfCalls(t, "Update", 0)
}

func testMintConversationCompleteAcceptsStringDeclarations(t *testing.T) {
	t.Helper()
	reg := mintConversationGuardReg()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationDomainAccess(t, tdb, reg.DomainNormalized)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{
			AgentID:        reg.AgentID,
			ConversationID: mintConversationTestConversationID,
			Status:         models.SoulMintConversationStatusInProgress,
			Messages:       encodeMintConversationBlob(mintConversationDurableAssistantMessagesJSON()),
		}
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
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = models.SoulAgentMintConversation{
			AgentID:        reg.AgentID,
			ConversationID: "conv-2",
			Status:         models.SoulMintConversationStatusInProgress,
			Messages:       encodeMintConversationBlob(mintConversationDurableAssistantMessagesJSON()),
		}
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
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok || appErr.Message != "soul registry bucket is not configured" {
		t.Fatalf("expected bucket error, got %#v", err)
	}
}

func testMintConversationFinalizeRequiresRegistrationIDWithBucketConfigured(t *testing.T) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)

	_, err := s.handleSoulFinalizeMintConversation(adminCtx())
	appErr, ok := err.(*apptheory.AppTheoryError)
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

	t.Run("legacy stream debit creates and updates conversations", func(t *testing.T) {
		testDebitMintConversationStreamCredits(t)
	})
}

func newMintConversationDebitServer() (*Server, *ttmocks.MockExtendedDB, *ttmocks.MockQuery, *ttmocks.MockTransactionBuilder) {
	db, queries := newTestDBWithModelQueries("*models.InstanceBudgetMonth")
	tb := new(ttmocks.MockTransactionBuilder)
	db.TransactWriteBuilder = tb
	return &Server{store: store.New(db)}, db, queries[0], tb
}

func testDebitMintConversationStreamCredits(t *testing.T) {
	t.Helper()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	inst := &models.Instance{Slug: "inst1"}
	for _, tc := range []struct {
		name  string
		fresh bool
	}{
		{name: "create", fresh: true},
		{name: "update", fresh: false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s, db, qBudget, tb := newMintConversationDebitServer()
			qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
				dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
				*dest = models.InstanceBudgetMonth{InstanceSlug: "inst1", Month: "2026-03", IncludedCredits: 50, UsedCredits: 5}
			}).Once()
			db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
			tb.On("Put", mock.AnythingOfType("*models.UsageLedgerEntry"), mock.Anything).Return(tb).Once()
			tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.InstanceBudgetMonth"), mock.Anything, mock.Anything).Return(tb).Once()
			if tc.fresh {
				tb.On("Create", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
					conv := testutil.RequireMockArg[*models.SoulAgentMintConversation](t, args, 0)
					if conv.Status != models.SoulMintConversationStatusInProgress || conv.ChargedCredits != soulMintConversationStreamBaseCredits {
						t.Fatalf("unexpected created conversation: %#v", conv)
					}
				})
			} else {
				tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
			}
			tb.On("Execute").Return(nil).Once()

			session := mintConversationSession{conversationID: mintConversationTestConversationID, modelSet: defaultSoulMintConversationModel, isNew: tc.fresh}
			if appErr := s.debitMintConversationStreamCredits(t.Context(), inst, "0xabc", session, "req-stream", now); appErr != nil {
				t.Fatalf("stream debit failed: %#v", appErr)
			}
		})
	}
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

	qVersion.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
		*dest = nil
	}).Once()
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
	assertMintConversationFinalizeHostedOffchain(t, out)
	assertMintConversationManagedENSMaterial(t, tdb.ensChannelModels, tdb.ensResolutionModels, identity)
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
	appErr *apptheory.AppTheoryError,
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
	if len(out.BoundaryRequirements) == 0 || out.SelfAttestationSigning == nil || out.SelfAttestationSigning.CanonicalJSON == "" {
		t.Fatalf("expected explicit preflight details, got %#v", out)
	}
	return out
}

func mustFinalizeMintConversationResponse(t *testing.T, resp *apptheory.Response) soulMintConversationFinalizeResponse {
	t.Helper()
	if bytes.Contains(resp.Body, []byte("minted_at")) || bytes.Contains(resp.Body, []byte("mint_tx_hash")) {
		t.Fatalf("hosted finalize response should omit mint transaction fields: %s", string(resp.Body))
	}
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

func assertMintConversationFinalizeHostedOffchain(t *testing.T, out soulMintConversationFinalizeResponse) {
	t.Helper()
	if out.Agent.Status != models.SoulAgentStatusActive || out.Agent.LifecycleStatus != models.SoulAgentStatusActive {
		t.Fatalf("expected hosted finalize to activate identity, got %#v", out.Agent)
	}
	if out.Agent.AnchorState != models.SoulAnchorStateHostedOffchain {
		t.Fatalf("expected hosted off-chain anchor state, got %#v", out.Agent)
	}
	if out.Agent.MintTxHash != "" || !out.Agent.MintedAt.IsZero() {
		t.Fatalf("expected no mint transaction fields on hosted finalize, got %#v", out.Agent)
	}
}

func assertMintConversationManagedENSMaterial(t *testing.T, channels []*models.SoulAgentChannel, resolutions []*models.SoulAgentENSResolution, identity *models.SoulAgentIdentity) {
	t.Helper()
	channel := firstManagedENSChannel(channels)
	resolution := firstManagedENSResolution(resolutions)
	requireManagedENSMaterial(t, channel, resolution, identity)
}

func firstManagedENSChannel(channels []*models.SoulAgentChannel) *models.SoulAgentChannel {
	for _, ch := range channels {
		if ch != nil && strings.TrimSpace(ch.Identifier) != "" {
			return ch
		}
	}
	return nil
}

func firstManagedENSResolution(resolutions []*models.SoulAgentENSResolution) *models.SoulAgentENSResolution {
	for _, res := range resolutions {
		if res != nil && strings.TrimSpace(res.ENSName) != "" {
			return res
		}
	}
	return nil
}

func requireManagedENSMaterial(t *testing.T, channel *models.SoulAgentChannel, resolution *models.SoulAgentENSResolution, identity *models.SoulAgentIdentity) {
	t.Helper()
	if channel == nil {
		t.Fatalf("expected managed ENS channel")
		return
	}
	if resolution == nil {
		t.Fatalf("expected managed ENS resolution")
		return
	}
	const wantENS = "agent-bot.inst1.lessersoul.eth"
	if channel.Identifier != wantENS || channel.ChannelType != models.SoulChannelTypeENS || !channel.Verified || channel.Status != models.SoulChannelStatusActive {
		t.Fatalf("unexpected ENS channel: %#v", channel)
	}
	if resolution.ENSName != wantENS ||
		resolution.AgentID != identity.AgentID ||
		resolution.LocalID != identity.LocalID ||
		resolution.Domain != identity.Domain ||
		resolution.Wallet != strings.ToLower(identity.Wallet) ||
		resolution.Status != models.SoulAgentStatusActive {
		t.Fatalf("unexpected ENS resolution: %#v", resolution)
	}
	if soul.IsLegacyBareManagedENSName(channel.Identifier) || soul.IsLegacyBareManagedENSName(resolution.ENSName) {
		t.Fatalf("managed ENS material used legacy bare name: channel=%q resolution=%q", channel.Identifier, resolution.ENSName)
	}
}
