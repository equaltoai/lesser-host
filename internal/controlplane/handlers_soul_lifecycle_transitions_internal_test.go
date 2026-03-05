package controlplane

import (
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
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulArchiveAgent_ArchivesAndWritesAudit(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()

	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Wallet:  wallet,
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()

	timestamp := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	digest, appErr := computeSoulContinuityEntryDigest(
		models.SoulContinuityEntryTypeArchived,
		timestamp,
		"Archived",
		"",
		[]string{"agent:" + agentIDHex},
	)
	if appErr != nil {
		t.Fatalf("digest: %v", appErr)
	}
	sig, sigErr := crypto.Sign(accounts.TextHash(digest), key)
	if sigErr != nil {
		t.Fatalf("sign: %v", sigErr)
	}
	sigHex := "0x" + hex.EncodeToString(sig)

	tb.On("Update", mock.Anything, mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		ident := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		if ident.Status != models.SoulAgentStatusArchived {
			t.Fatalf("expected status archived, got %q", ident.Status)
		}
		if ident.LifecycleStatus != models.SoulAgentStatusArchived {
			t.Fatalf("expected lifecycle status archived, got %q", ident.LifecycleStatus)
		}
		if strings.TrimSpace(ident.LifecycleReason) != "done" {
			t.Fatalf("expected lifecycle reason, got %q", ident.LifecycleReason)
		}
	})
	tb.On("Create", mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		entry := testutil.RequireMockArg[*models.SoulAgentContinuity](t, args, 0)
		if entry.AgentID != agentIDHex {
			t.Fatalf("expected agent id %q, got %q", agentIDHex, entry.AgentID)
		}
		if entry.Type != models.SoulContinuityEntryTypeArchived {
			t.Fatalf("expected type %q, got %q", models.SoulContinuityEntryTypeArchived, entry.Type)
		}
		if entry.Summary != "Archived" {
			t.Fatalf("expected summary %q, got %q", "Archived", entry.Summary)
		}
		if entry.Signature != strings.ToLower(sigHex) {
			t.Fatalf("expected signature %q, got %q", strings.ToLower(sigHex), entry.Signature)
		}
		if entry.Timestamp.UTC().Format(time.RFC3339) != timestamp {
			t.Fatalf("expected timestamp %q, got %q", timestamp, entry.Timestamp.UTC().Format(time.RFC3339))
		}
	})
	tb.On("Put", mock.Anything, mock.Anything).Return(tb).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"reason":"done","timestamp":"` + timestamp + `","signature":"` + sigHex + `"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	resp, err := s.handleSoulArchiveAgent(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out models.SoulAgentIdentity
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != models.SoulAgentStatusArchived {
		t.Fatalf("expected archived status, got %q", out.Status)
	}
}

func TestHandleSoulDesignateSuccessor_SucceedsAndCreatesEntries(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()

	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	keyPred, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate predecessor key: %v", err)
	}
	walletPred := strings.ToLower(crypto.PubkeyToAddress(keyPred.PublicKey).Hex())

	keySucc, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate successor key: %v", err)
	}
	walletSucc := strings.ToLower(crypto.PubkeyToAddress(keySucc.PublicKey).Hex())

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex
	successorIDHex := "0x" + strings.Repeat("22", 32)

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	// First identity read: the current agent.
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Wallet:  walletPred,
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()
	// Second identity read: the successor.
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: successorIDHex,
			Domain:  "example.com",
			LocalID: "agent-bob",
			Wallet:  walletSucc,
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()

	timestamp := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	declaredDigest, appErr := computeSoulContinuityEntryDigest(
		models.SoulContinuityEntryTypeSuccessionDeclared,
		timestamp,
		"Succession declared",
		"",
		[]string{"agent:" + agentIDHex, "successor:" + successorIDHex},
	)
	if appErr != nil {
		t.Fatalf("declared digest: %v", appErr)
	}
	declaredSigBytes, sigErr := crypto.Sign(accounts.TextHash(declaredDigest), keyPred)
	if sigErr != nil {
		t.Fatalf("sign declared: %v", sigErr)
	}
	declaredSigHex := "0x" + hex.EncodeToString(declaredSigBytes)

	receivedDigest, appErr := computeSoulContinuityEntryDigest(
		models.SoulContinuityEntryTypeSuccessionReceived,
		timestamp,
		"Succession received",
		"",
		[]string{"agent:" + successorIDHex, "predecessor:" + agentIDHex},
	)
	if appErr != nil {
		t.Fatalf("received digest: %v", appErr)
	}
	receivedSigBytes, sigErr := crypto.Sign(accounts.TextHash(receivedDigest), keySucc)
	if sigErr != nil {
		t.Fatalf("sign received: %v", sigErr)
	}
	receivedSigHex := "0x" + hex.EncodeToString(receivedSigBytes)

	updateCalls := 0
	tb.On("Update", mock.Anything, mock.Anything, mock.Anything).Return(tb).Twice().Run(func(args mock.Arguments) {
		ident := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		if updateCalls == 0 {
			if ident.Status != models.SoulAgentStatusSucceeded {
				t.Fatalf("expected status succeeded, got %q", ident.Status)
			}
			if ident.SuccessorAgentId != successorIDHex {
				t.Fatalf("expected successor agent id %q, got %q", successorIDHex, ident.SuccessorAgentId)
			}
		} else {
			if ident.AgentID != successorIDHex {
				t.Fatalf("expected successor agent id %q, got %q", successorIDHex, ident.AgentID)
			}
			if ident.PredecessorAgentId != agentIDHex {
				t.Fatalf("expected predecessor agent id %q, got %q", agentIDHex, ident.PredecessorAgentId)
			}
		}
		updateCalls++
	})

	createKinds := map[string]bool{}
	tb.On("Create", mock.Anything, mock.Anything).Return(tb).Twice().Run(func(args mock.Arguments) {
		entry := testutil.RequireMockArg[*models.SoulAgentContinuity](t, args, 0)
		switch entry.Type {
		case models.SoulContinuityEntryTypeSuccessionDeclared:
			createKinds["declared"] = true
			if entry.AgentID != agentIDHex {
				t.Fatalf("expected declared agent id %q, got %q", agentIDHex, entry.AgentID)
			}
			if entry.Signature != strings.ToLower(declaredSigHex) {
				t.Fatalf("expected declared signature %q, got %q", strings.ToLower(declaredSigHex), entry.Signature)
			}
		case models.SoulContinuityEntryTypeSuccessionReceived:
			createKinds["received"] = true
			if entry.AgentID != successorIDHex {
				t.Fatalf("expected received agent id %q, got %q", successorIDHex, entry.AgentID)
			}
			if entry.Signature != strings.ToLower(receivedSigHex) {
				t.Fatalf("expected received signature %q, got %q", strings.ToLower(receivedSigHex), entry.Signature)
			}
		default:
			t.Fatalf("unexpected continuity entry type %q", entry.Type)
		}
		if entry.Timestamp.UTC().Format(time.RFC3339) != timestamp {
			t.Fatalf("expected timestamp %q, got %q", timestamp, entry.Timestamp.UTC().Format(time.RFC3339))
		}
	})

	tb.On("Put", mock.Anything, mock.Anything).Return(tb).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"successor_agent_id":"` + successorIDHex + `","reason":"upgrade","timestamp":"` + timestamp + `","predecessor_signature":"` + declaredSigHex + `","successor_signature":"` + receivedSigHex + `"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	resp, err := s.handleSoulDesignateSuccessor(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	if !createKinds["declared"] || !createKinds["received"] {
		t.Fatalf("expected both continuity entries to be created, got %#v", createKinds)
	}
}

func TestHandleSoulArchiveAgent_RejectsInvalidTransition(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Status:  models.SoulAgentStatusArchived,
		}
	}).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	_, err := s.handleSoulArchiveAgent(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "app.conflict" {
		t.Fatalf("expected app.conflict, got %s", appErr.Code)
	}
}

func TestHandleSoulAgentUpdateRegistration_ArchivedAgentRejected(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store:     store.New(tdb.db),
		soulPacks: &fakeSoulPackStore{},
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRPCURL:                  "http://rpc.local",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Wallet:  "0x000000000000000000000000000000000000beef",
			Status:  models.SoulAgentStatusArchived,
		}
	}).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"version":"2"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	_, err := s.handleSoulAgentUpdateRegistration(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "app.conflict" {
		t.Fatalf("expected app.conflict, got %s", appErr.Code)
	}
}

func TestHandleSoulArchiveAgent_TransactionFailureReturnsInternalError(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tb.On("Execute").Return(errors.New("boom")).Once()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Status:  models.SoulAgentStatusActive,
			Wallet:  wallet,
		}
	}).Once()

	timestamp := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	digest, appErr := computeSoulContinuityEntryDigest(
		models.SoulContinuityEntryTypeArchived,
		timestamp,
		"Archived",
		"",
		[]string{"agent:" + agentIDHex},
	)
	if appErr != nil {
		t.Fatalf("digest: %v", appErr)
	}
	sig, sigErr := crypto.Sign(accounts.TextHash(digest), key)
	if sigErr != nil {
		t.Fatalf("sign: %v", sigErr)
	}
	sigHex := "0x" + hex.EncodeToString(sig)

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"reason":"done","timestamp":"` + timestamp + `","signature":"` + sigHex + `"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	_, callErr := s.handleSoulArchiveAgent(ctx)
	if callErr == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := callErr.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", callErr)
	}
	if appErr.Code != "app.internal" {
		t.Fatalf("expected app.internal, got %s", appErr.Code)
	}
}
