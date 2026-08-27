package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// assertSoulMintConversationBuilderWritesGSI4 invokes a captured
// UpdateWithBuilder closure against a recording builder and asserts it writes
// the gsi4 keys (issue #1067 part C2). This mirrors the C1 lifecycle-test
// convention (field lists carry GSI3PK/GSI3SK) for the UpdateWithBuilder sites:
// a regression that drops the gsi4 key maintenance from any conversation
// UpdateWithBuilder site fails here, and the update model must itself carry the
// computed keys (wantPK/wantSK, derived from the caller's CreatedAt threading).
func assertSoulMintConversationBuilderWritesGSI4(t *testing.T, buildFn func(core.UpdateBuilder) error, wantPK, wantSK string) {
	t.Helper()
	if buildFn == nil {
		t.Fatalf("update builder closure is nil")
	}
	if wantPK == "" || wantSK == "" {
		t.Fatalf("conversation update model carries no gsi4 keys (pk=%q sk=%q); CreatedAt was not threaded", wantPK, wantSK)
	}
	ub := new(ttmocks.MockUpdateBuilder)
	ub.On("Set", "GSI4PK", wantPK).Return(ub).Once()
	ub.On("Set", "GSI4SK", wantSK).Return(ub).Once()
	ub.On("Set", mock.Anything, mock.Anything).Return(ub).Maybe()
	ub.On("Add", mock.Anything, mock.Anything).Return(ub).Maybe()
	ub.On("Remove", mock.Anything).Return(ub).Maybe()
	ub.On("SetIfNotExists", mock.Anything, mock.Anything, mock.Anything).Return(ub).Maybe()
	require.NoError(t, buildFn(ub))
	ub.AssertExpectations(t)
}

// expectConversationGSI4BuilderCapture registers the SoulAgentMintConversation
// UpdateWithBuilder expectation that asserts the update model carries the gsi4
// keys and the closure writes them.
func expectConversationGSI4BuilderCapture(t *testing.T, tb *ttmocks.MockTransactionBuilder) {
	t.Helper()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		conv := testutil.RequireMockArg[*models.SoulAgentMintConversation](t, args, 0)
		buildFn := testutil.RequireMockArg[func(core.UpdateBuilder) error](t, args, 1)
		assertSoulMintConversationBuilderWritesGSI4(t, buildFn, conv.GSI4PK, conv.GSI4SK)
	})
}

// newGSI4WriterFixture builds a server over the standard mint-conversation mock
// DB with a transaction builder wired for UpdateWithBuilder capture, plus a
// valid conversation + hosted-genesis session pair (the session is built by the
// in-package legacy fixture, which runs BeforeCreate so every validation in the
// write path passes).
func newGSI4WriterFixture(t *testing.T) (*mintConversationTestDB, *Server, *ttmocks.MockTransactionBuilder, time.Time, string, *models.SoulAgentMintConversation, models.HostedGenesisSession) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	ub := new(ttmocks.MockUpdateBuilder)
	ub.On("Set", mock.Anything, mock.Anything).Return(ub).Maybe()
	ub.On("Add", mock.Anything, mock.Anything).Return(ub).Maybe()
	ub.On("Remove", mock.Anything).Return(ub).Maybe()
	tb.UpdateBuilder = ub

	agentHex := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	conv := &models.SoulAgentMintConversation{
		AgentID:        agentHex,
		ConversationID: "conv-1",
		Model:          "claude-sonnet-5",
		Status:         models.SoulMintConversationStatusInProgress,
		CreatedAt:      now,
	}
	sessionModel := hostedGenesisSessionFromLegacyConversationForTest(tdb, *conv)
	// The legacy fixture binds a declaration candidate derived from a full
	// conversation; this test's minimal conversation has no turn/candidate, so
	// clear the candidate (optional per bindDeclarationCandidate) to keep the
	// session valid for the progression/retry write paths.
	sessionModel.DeclarationCandidate = nil
	sessionModel.CandidateRevision = 0
	sessionModel.CandidateHash = ""
	sessionModel.CandidatePhase = ""
	return tdb, s, tb, now, agentHex, conv, sessionModel
}

// TestSoulMintConversationGSI4_ProgressionUpdateWithBuilder covers
// persistHostedGenesisProgression (handlers_soul_mint_conversation_async.go,
// the per-turn progression write): the conversation update model must carry the
// gsi4 keys and the closure must write them.
func TestSoulMintConversationGSI4_ProgressionUpdateWithBuilder(t *testing.T) {
	t.Parallel()

	tdb, s, tb, now, _, conv, sessionModel := newGSI4WriterFixture(t)
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once()
	expectConversationGSI4BuilderCapture(t, tb)
	tb.On("Execute").Return(nil).Once()

	accepted := hostedGenesisTurnSession{
		expectedVersion: sessionModel.Version,
		sessionIsNew:    false,
		session:         &sessionModel,
	}
	appErr := s.persistHostedGenesisProgression(context.Background(), accepted, &sessionModel, conv,
		`[{"role":"user","content":"hi"}]`, "", models.AIUsage{}, now)
	require.Nil(t, appErr)
}

// TestSoulMintConversationGSI4_AcceptedTurnUpdateWithBuilder covers
// persistHostedGenesisAcceptedTurn (handlers_soul_mint_conversation_async.go,
// the accepted-turn persist for an existing conversation): the update model
// must carry the gsi4 keys and the closure must write them.
func TestSoulMintConversationGSI4_AcceptedTurnUpdateWithBuilder(t *testing.T) {
	t.Parallel()

	tdb, s, tb, now, agentHex, conv, sessionModel := newGSI4WriterFixture(t)
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "inst1", Month: "2026-03", IncludedCredits: 50, UsedCredits: 5}
	}).Once()
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("Put", mock.AnythingOfType("*models.UsageLedgerEntry"), mock.Anything).Return(tb).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.InstanceBudgetMonth"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once()
	expectConversationGSI4BuilderCapture(t, tb)
	tb.On("Execute").Return(nil).Once()

	regCtx := mintConversationRegistrationContext{
		reg:        &models.SoulAgentRegistration{ID: "reg-1"},
		inst:       &models.Instance{Slug: "inst1"},
		agentIDHex: agentHex,
	}
	turnSession := hostedGenesisTurnSession{
		conversationID:   conv.ConversationID,
		turnID:           "turn-1",
		modelSet:         conv.Model,
		existingMessages: []soulMintConversationMessage{{Role: "user", Content: "hi"}},
		sessionIsNew:     false,
		expectedStatus:   hostedgenesis.StatusInProgress,
		expectedVersion:  sessionModel.Version,
		conv:             conv,
		session:          &sessionModel,
	}
	appErr := s.persistHostedGenesisAcceptedTurn(context.Background(), regCtx, turnSession,
		[]soulMintConversationMessage{{Role: "user", Content: "hi"}},
		`[{"role":"user","content":"hi"}]`, nil, "req-1", "req-1", now)
	require.Nil(t, appErr)
}

// TestSoulMintConversationGSI4_RetryDispatchFailureUpdateWithBuilder covers
// persistHostedGenesisRetryDispatchFailure
// (handlers_soul_mint_conversation_recover.go): the update model must carry the
// gsi4 keys and the closure must write them.
func TestSoulMintConversationGSI4_RetryDispatchFailureUpdateWithBuilder(t *testing.T) {
	t.Parallel()

	tdb, s, tb, now, _, conv, sessionModel := newGSI4WriterFixture(t)
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once()
	expectConversationGSI4BuilderCapture(t, tb)
	tb.On("Execute").Return(nil).Once()

	pending := sessionModel
	pending.Status = string(hostedgenesis.StatusInProgress)
	failedSession, failedConv, appErr := s.persistHostedGenesisRetryDispatchFailure(context.Background(), &pending, conv, "req-recover", now, hostedGenesisFailureAssistantTurnFailed)
	require.Nil(t, appErr)
	require.NotNil(t, failedSession)
	require.NotNil(t, failedConv)
}
