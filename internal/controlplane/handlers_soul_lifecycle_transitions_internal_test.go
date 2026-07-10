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
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulArchiveAgent_ArchivesAndWritesAudit(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	tb := prepareSoulLifecycleTransitionServer(t, tdb)

	agentIDHex := soulLifecycleTestAgentIDHex
	seedSoulLifecycleTransitionAccess(t, tdb, agentIDHex, wallet)

	timestamp := canonicalSoulSignedTimestamp(time.Now().Add(-time.Minute).UTC())
	continuityNonce := "test-archive-nonce"
	challenge := newSoulLifecycleChallenge(agentIDHex, continuityNonce, soulLifecycleChallengePurposeArchiveAgent, "", time.Now().UTC())
	expectSoulLifecycleChallengeLookup(t, tdb, challenge)
	digest, appErr := computeSoulContinuityEntryDigest(
		models.SoulContinuityEntryTypeArchived,
		timestamp,
		"Archived",
		"",
		[]string{"agent:" + agentIDHex},
		continuityNonce,
	)
	if appErr != nil {
		t.Fatalf("digest: %v", appErr)
	}
	sig, sigErr := crypto.Sign(accounts.TextHash(digest), key)
	if sigErr != nil {
		t.Fatalf("sign: %v", sigErr)
	}
	sigHex := "0x" + hex.EncodeToString(sig)

	expectSoulLifecycleChallengeDelete(t, tb, agentIDHex, continuityNonce, soulLifecycleChallengePurposeArchiveAgent, "")
	expectArchiveIdentityUpdate(t, tb)
	expectArchiveContinuityCreate(t, tb, agentIDHex, sigHex, timestamp)
	tb.On("Put", mock.Anything, mock.Anything).Return(tb).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"reason":"done","timestamp":"` + timestamp + `","signature":"` + sigHex + `","continuity_nonce":"` + continuityNonce + `"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	resp, err := newSoulLifecycleTransitionServer(tdb).handleSoulArchiveAgent(ctx)
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
	tb := prepareSoulLifecycleTransitionServer(t, tdb)

	agentIDHex := soulLifecycleTestAgentIDHex
	successorIDHex := "0x" + strings.Repeat("22", 32)
	seedSoulLifecycleTransitionAccess(t, tdb, agentIDHex, walletPred)
	seedSoulSuccessorIdentity(t, tdb, successorIDHex, walletSucc)

	timestamp := canonicalSoulSignedTimestamp(time.Now().Add(-time.Minute).UTC())
	continuityNonce := "test-succession-nonce"
	challenge := newSoulLifecycleChallenge(agentIDHex, continuityNonce, soulLifecycleChallengePurposeDesignateSuccessor, successorIDHex, time.Now().UTC())
	expectSoulLifecycleChallengeLookup(t, tdb, challenge)
	declaredDigest, appErr := computeSoulContinuityEntryDigest(
		models.SoulContinuityEntryTypeSuccessionDeclared,
		timestamp,
		"Succession declared",
		"",
		[]string{"agent:" + agentIDHex, "successor:" + successorIDHex},
		continuityNonce,
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
		continuityNonce,
	)
	if appErr != nil {
		t.Fatalf("received digest: %v", appErr)
	}
	receivedSigBytes, sigErr := crypto.Sign(accounts.TextHash(receivedDigest), keySucc)
	if sigErr != nil {
		t.Fatalf("sign received: %v", sigErr)
	}
	receivedSigHex := "0x" + hex.EncodeToString(receivedSigBytes)

	expectSoulLifecycleChallengeDelete(t, tb, agentIDHex, continuityNonce, soulLifecycleChallengePurposeDesignateSuccessor, successorIDHex)
	expectSuccessorUpdates(t, tb, agentIDHex, successorIDHex)
	createKinds := expectSuccessorContinuityCreates(t, tb, agentIDHex, successorIDHex, declaredSigHex, receivedSigHex, timestamp)

	tb.On("Put", mock.Anything, mock.Anything).Return(tb).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"successor_agent_id":"` + successorIDHex + `","reason":"upgrade","timestamp":"` + timestamp + `","predecessor_signature":"` + declaredSigHex + `","successor_signature":"` + receivedSigHex + `","continuity_nonce":"` + continuityNonce + `"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	resp, err := newSoulLifecycleTransitionServer(tdb).handleSoulDesignateSuccessor(ctx)
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

func newSoulLifecycleTransitionServer(tdb soulLifecycleTestDB) *Server {
	return &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}
}

func prepareSoulLifecycleTransitionServer(t *testing.T, tdb soulLifecycleTestDB) *ttmocks.MockTransactionBuilder {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	return tb
}

func expectSoulLifecycleChallengeLookup(t *testing.T, tdb soulLifecycleTestDB, challenge *models.SoulLifecycleChallenge) {
	t.Helper()
	tdb.qChallenge.On("First", mock.AnythingOfType("*models.SoulLifecycleChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulLifecycleChallenge](t, args, 0)
		*dest = *challenge
	}).Once()
}

func expectSoulLifecycleChallengeNotFound(tdb soulLifecycleTestDB) {
	tdb.qChallenge.On("First", mock.AnythingOfType("*models.SoulLifecycleChallenge")).Return(theoryErrors.ErrItemNotFound).Once()
}

func expectSoulLifecycleChallengeCreate(t *testing.T, tdb soulLifecycleTestDB, agentIDHex string, purpose string, successorIDHex string) *ttmocks.MockTransactionBuilder {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("Create", mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		challenge := testutil.RequireMockArg[*models.SoulLifecycleChallenge](t, args, 0)
		if challenge.AgentID != strings.ToLower(strings.TrimSpace(agentIDHex)) {
			t.Fatalf("expected challenge agent id %q, got %q", strings.ToLower(strings.TrimSpace(agentIDHex)), challenge.AgentID)
		}
		if challenge.Purpose != strings.ToLower(strings.TrimSpace(purpose)) {
			t.Fatalf("expected challenge purpose %q, got %q", strings.ToLower(strings.TrimSpace(purpose)), challenge.Purpose)
		}
		if challenge.SuccessorAgentID != strings.ToLower(strings.TrimSpace(successorIDHex)) {
			t.Fatalf("expected challenge successor %q, got %q", strings.ToLower(strings.TrimSpace(successorIDHex)), challenge.SuccessorAgentID)
		}
		if strings.TrimSpace(challenge.Nonce) == "" {
			t.Fatalf("expected issued nonce")
		}
		if challenge.ExpiresAt.Sub(challenge.IssuedAt) != soulLifecycleChallengeTTL {
			t.Fatalf("expected ttl %s, got %s", soulLifecycleChallengeTTL, challenge.ExpiresAt.Sub(challenge.IssuedAt))
		}
	})
	return tb
}

func expectSoulLifecycleChallengeDelete(t *testing.T, tb *ttmocks.MockTransactionBuilder, agentIDHex string, nonce string, purpose string, successorIDHex string) {
	t.Helper()
	tb.On("Delete", mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		challenge := testutil.RequireMockArg[*models.SoulLifecycleChallenge](t, args, 0)
		if challenge.AgentID != strings.ToLower(strings.TrimSpace(agentIDHex)) {
			t.Fatalf("expected challenge agent id %q, got %q", strings.ToLower(strings.TrimSpace(agentIDHex)), challenge.AgentID)
		}
		if challenge.Nonce != strings.TrimSpace(nonce) {
			t.Fatalf("expected challenge nonce %q, got %q", strings.TrimSpace(nonce), challenge.Nonce)
		}
		if challenge.Purpose != strings.ToLower(strings.TrimSpace(purpose)) {
			t.Fatalf("expected challenge purpose %q, got %q", strings.ToLower(strings.TrimSpace(purpose)), challenge.Purpose)
		}
		if challenge.SuccessorAgentID != strings.ToLower(strings.TrimSpace(successorIDHex)) {
			t.Fatalf("expected challenge successor %q, got %q", strings.ToLower(strings.TrimSpace(successorIDHex)), challenge.SuccessorAgentID)
		}

		conditions, ok := args.Get(1).([]core.TransactCondition)
		if !ok {
			t.Fatalf("expected transactional delete conditions, got %T", args.Get(1))
		}
		if len(conditions) != 2 {
			t.Fatalf("expected IfExists + TTL conditions, got %#v", conditions)
		}
		if conditions[0].Kind != core.TransactConditionKindPrimaryKeyExists {
			t.Fatalf("expected primary-key-exists condition, got %#v", conditions[0])
		}
		if conditions[1].Kind != core.TransactConditionKindField || conditions[1].Field != "TTL" || conditions[1].Operator != ">" {
			t.Fatalf("expected TTL freshness condition, got %#v", conditions[1])
		}
	})
}

func signSoulArchiveContinuityForTest(t *testing.T, privateKey *ecdsa.PrivateKey, agentIDHex string, timestamp string, continuityNonce string) string {
	t.Helper()
	digest, appErr := computeSoulContinuityEntryDigest(
		models.SoulContinuityEntryTypeArchived,
		timestamp,
		"Archived",
		"",
		[]string{"agent:" + agentIDHex},
		continuityNonce,
	)
	if appErr != nil {
		t.Fatalf("digest: %v", appErr)
	}
	sig, sigErr := crypto.Sign(accounts.TextHash(digest), privateKey)
	if sigErr != nil {
		t.Fatalf("sign: %v", sigErr)
	}
	return "0x" + hex.EncodeToString(sig)
}

func newSoulArchiveCompletionContext(agentIDHex string, timestamp string, sigHex string, continuityNonce string) *apptheory.Context {
	ctx := &apptheory.Context{
		RequestID:    "r-lh06",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"reason":"done","timestamp":"` + timestamp + `","signature":"` + sigHex + `","continuity_nonce":"` + continuityNonce + `"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)
	return ctx
}

func seedSoulLifecycleTransitionAccess(t *testing.T, tdb soulLifecycleTestDB, agentIDHex string, wallet string) {
	t.Helper()
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
}

func seedSoulSuccessorIdentity(t *testing.T, tdb soulLifecycleTestDB, successorIDHex string, wallet string) {
	t.Helper()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: successorIDHex,
			Domain:  "example.com",
			LocalID: "agent-bob",
			Wallet:  wallet,
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()
}

func expectArchiveIdentityUpdate(t *testing.T, tb *ttmocks.MockTransactionBuilder) {
	t.Helper()
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
}

func expectArchiveContinuityCreate(t *testing.T, tb *ttmocks.MockTransactionBuilder, agentIDHex string, sigHex string, timestamp string) {
	t.Helper()
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
		if canonicalSoulSignedTimestamp(entry.Timestamp.UTC()) != timestamp {
			t.Fatalf("expected timestamp %q, got %q", timestamp, canonicalSoulSignedTimestamp(entry.Timestamp.UTC()))
		}
	})
}

func expectSuccessorUpdates(t *testing.T, tb *ttmocks.MockTransactionBuilder, agentIDHex string, successorIDHex string) {
	t.Helper()
	updateCalls := 0
	tb.On("Update", mock.Anything, mock.Anything, mock.Anything).Return(tb).Twice().Run(func(args mock.Arguments) {
		ident := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		if updateCalls == 0 {
			if ident.Status != models.SoulAgentStatusSucceeded {
				t.Fatalf("expected status succeeded, got %q", ident.Status)
			}
			if ident.SuccessorAgentID != successorIDHex {
				t.Fatalf("expected successor agent id %q, got %q", successorIDHex, ident.SuccessorAgentID)
			}
		} else {
			if ident.AgentID != successorIDHex {
				t.Fatalf("expected successor agent id %q, got %q", successorIDHex, ident.AgentID)
			}
			if ident.PredecessorAgentID != agentIDHex {
				t.Fatalf("expected predecessor agent id %q, got %q", agentIDHex, ident.PredecessorAgentID)
			}
		}
		updateCalls++
	})
}

func expectSuccessorContinuityCreates(t *testing.T, tb *ttmocks.MockTransactionBuilder, agentIDHex string, successorIDHex string, declaredSigHex string, receivedSigHex string, timestamp string) map[string]bool {
	t.Helper()
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
		if canonicalSoulSignedTimestamp(entry.Timestamp.UTC()) != timestamp {
			t.Fatalf("expected timestamp %q, got %q", timestamp, canonicalSoulSignedTimestamp(entry.Timestamp.UTC()))
		}
	})
	return createKinds
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
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != appErrCodeConflict {
		t.Fatalf("expected %s, got %s", appErrCodeConflict, appErr.Code)
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
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != appErrCodeConflict {
		t.Fatalf("expected %s, got %s", appErrCodeConflict, appErr.Code)
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

	timestamp := canonicalSoulSignedTimestamp(time.Now().Add(-time.Minute).UTC())
	continuityNonce := "test-archive-nonce-2"
	challenge := newSoulLifecycleChallenge(agentIDHex, continuityNonce, soulLifecycleChallengePurposeArchiveAgent, "", time.Now().UTC())
	expectSoulLifecycleChallengeLookup(t, tdb, challenge)
	digest, appErr := computeSoulContinuityEntryDigest(
		models.SoulContinuityEntryTypeArchived,
		timestamp,
		"Archived",
		"",
		[]string{"agent:" + agentIDHex},
		continuityNonce,
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
			Body: []byte(`{"reason":"done","timestamp":"` + timestamp + `","signature":"` + sigHex + `","continuity_nonce":"` + continuityNonce + `"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	_, callErr := s.handleSoulArchiveAgent(ctx)
	if callErr == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := callErr.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppError, got %T", callErr)
	}
	if appErr.Code != appErrCodeInternal {
		t.Fatalf("expected %s, got %s", appErrCodeInternal, appErr.Code)
	}
}

func TestHandleSoulArchiveAgent_RejectsConsumedNonceReplay(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	tb := prepareSoulLifecycleTransitionServer(t, tdb)

	agentIDHex := soulLifecycleTestAgentIDHex
	timestamp := canonicalSoulSignedTimestamp(time.Now().Add(-time.Minute).UTC())
	continuityNonce := "replayed-archive-nonce"
	sigHex := signSoulArchiveContinuityForTest(t, key, agentIDHex, timestamp, continuityNonce)

	seedSoulLifecycleTransitionAccess(t, tdb, agentIDHex, wallet)
	challenge := newSoulLifecycleChallenge(agentIDHex, continuityNonce, soulLifecycleChallengePurposeArchiveAgent, "", time.Now().UTC())
	expectSoulLifecycleChallengeLookup(t, tdb, challenge)
	expectSoulLifecycleChallengeDelete(t, tb, agentIDHex, continuityNonce, soulLifecycleChallengePurposeArchiveAgent, "")
	expectArchiveIdentityUpdate(t, tb)
	expectArchiveContinuityCreate(t, tb, agentIDHex, sigHex, timestamp)
	tb.On("Put", mock.Anything, mock.Anything).Return(tb).Once()

	firstCtx := newSoulArchiveCompletionContext(agentIDHex, timestamp, sigHex, continuityNonce)
	resp, callErr := newSoulLifecycleTransitionServer(tdb).handleSoulArchiveAgent(firstCtx)
	if callErr != nil {
		t.Fatalf("first archive completion unexpected err: %v", callErr)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected first completion 200, got %d", resp.Status)
	}

	seedSoulLifecycleTransitionAccess(t, tdb, agentIDHex, wallet)
	expectSoulLifecycleChallengeNotFound(tdb)

	secondCtx := newSoulArchiveCompletionContext(agentIDHex, timestamp, sigHex, continuityNonce)
	_, callErr = newSoulLifecycleTransitionServer(tdb).handleSoulArchiveAgent(secondCtx)
	if callErr == nil {
		t.Fatalf("expected replayed continuity_nonce to be rejected")
	}
	appErr, ok := callErr.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppError, got %T", callErr)
	}
	if appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected %s, got %s", appErrCodeBadRequest, appErr.Code)
	}
}

func TestHandleSoulArchiveAgent_RejectsUnknownNonce(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	agentIDHex := soulLifecycleTestAgentIDHex
	timestamp := canonicalSoulSignedTimestamp(time.Now().Add(-time.Minute).UTC())
	continuityNonce := "never-issued-archive-nonce"
	sigHex := signSoulArchiveContinuityForTest(t, key, agentIDHex, timestamp, continuityNonce)

	seedSoulLifecycleTransitionAccess(t, tdb, agentIDHex, wallet)
	expectSoulLifecycleChallengeNotFound(tdb)

	ctx := newSoulArchiveCompletionContext(agentIDHex, timestamp, sigHex, continuityNonce)
	_, callErr := newSoulLifecycleTransitionServer(tdb).handleSoulArchiveAgent(ctx)
	if callErr == nil {
		t.Fatalf("expected unknown continuity_nonce to be rejected")
	}
	appErr, ok := callErr.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppError, got %T", callErr)
	}
	if appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected %s, got %s", appErrCodeBadRequest, appErr.Code)
	}
}

func TestHandleSoulArchiveAgent_RejectsExpiredNonce(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	agentIDHex := soulLifecycleTestAgentIDHex
	timestamp := canonicalSoulSignedTimestamp(time.Now().Add(-time.Minute).UTC())
	continuityNonce := "expired-archive-nonce"
	sigHex := signSoulArchiveContinuityForTest(t, key, agentIDHex, timestamp, continuityNonce)

	seedSoulLifecycleTransitionAccess(t, tdb, agentIDHex, wallet)
	expiredChallenge := newSoulLifecycleChallenge(agentIDHex, continuityNonce, soulLifecycleChallengePurposeArchiveAgent, "", time.Now().Add(-10*time.Minute).UTC())
	expectSoulLifecycleChallengeLookup(t, tdb, expiredChallenge)

	ctx := newSoulArchiveCompletionContext(agentIDHex, timestamp, sigHex, continuityNonce)
	_, callErr := newSoulLifecycleTransitionServer(tdb).handleSoulArchiveAgent(ctx)
	if callErr == nil {
		t.Fatalf("expected expired continuity_nonce to be rejected")
	}
	appErr, ok := callErr.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppError, got %T", callErr)
	}
	if appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected %s, got %s", appErrCodeBadRequest, appErr.Code)
	}
	if !strings.Contains(appErr.Message, "expired") {
		t.Fatalf("expected expired nonce message, got %q", appErr.Message)
	}
}

// CSR-013 regression: archive commit must require continuity_nonce (replay protection).
func TestHandleSoulArchiveAgent_RejectMissingNonce(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	_ = prepareSoulLifecycleTransitionServer(t, tdb)

	agentIDHex := soulLifecycleTestAgentIDHex
	seedSoulLifecycleTransitionAccess(t, tdb, agentIDHex, wallet)

	timestamp := canonicalSoulSignedTimestamp(time.Now().Add(-time.Minute).UTC())
	digest, appErr := computeSoulContinuityEntryDigest(
		models.SoulContinuityEntryTypeArchived,
		timestamp,
		"Archived",
		"",
		[]string{"agent:" + agentIDHex},
		"",
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
		RequestID:    "r-csr013",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			// Missing continuity_nonce — should be rejected.
			Body: []byte(`{"reason":"done","timestamp":"` + timestamp + `","signature":"` + sigHex + `"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	_, callErr := newSoulLifecycleTransitionServer(tdb).handleSoulArchiveAgent(ctx)
	if callErr == nil {
		t.Fatalf("expected error for missing continuity_nonce")
	}
	appErr2, ok := callErr.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppError, got %T", callErr)
	}
	if appErr2.Code != appErrCodeBadRequest {
		t.Fatalf("expected %s, got %s", appErrCodeBadRequest, appErr2.Code)
	}
}

// CSR-013 regression: designate-successor commit must require continuity_nonce (replay protection).
func TestHandleSoulDesignateSuccessor_RejectMissingNonce(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()

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
	_ = prepareSoulLifecycleTransitionServer(t, tdb)

	agentIDHex := soulLifecycleTestAgentIDHex
	successorIDHex := "0x" + strings.Repeat("22", 32)
	seedSoulLifecycleTransitionAccess(t, tdb, agentIDHex, walletPred)
	seedSoulSuccessorIdentity(t, tdb, successorIDHex, walletSucc)

	timestamp := canonicalSoulSignedTimestamp(time.Now().Add(-time.Minute).UTC())
	declaredDigest, appErr := computeSoulContinuityEntryDigest(
		models.SoulContinuityEntryTypeSuccessionDeclared,
		timestamp,
		"Succession declared",
		"",
		[]string{"agent:" + agentIDHex, "successor:" + successorIDHex},
		"",
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
		"",
	)
	if appErr != nil {
		t.Fatalf("received digest: %v", appErr)
	}
	receivedSigBytes, sigErr := crypto.Sign(accounts.TextHash(receivedDigest), keySucc)
	if sigErr != nil {
		t.Fatalf("sign received: %v", sigErr)
	}
	receivedSigHex := "0x" + hex.EncodeToString(receivedSigBytes)

	ctx := &apptheory.Context{
		RequestID:    "r-csr013",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			// Missing continuity_nonce — should be rejected.
			Body: []byte(`{"successor_agent_id":"` + successorIDHex + `","reason":"upgrade","timestamp":"` + timestamp + `","predecessor_signature":"` + declaredSigHex + `","successor_signature":"` + receivedSigHex + `"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	_, callErr := newSoulLifecycleTransitionServer(tdb).handleSoulDesignateSuccessor(ctx)
	if callErr == nil {
		t.Fatalf("expected error for missing continuity_nonce")
	}
	appErr2, ok := callErr.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppError, got %T", callErr)
	}
	if appErr2.Code != appErrCodeBadRequest {
		t.Fatalf("expected %s, got %s", appErrCodeBadRequest, appErr2.Code)
	}
}
