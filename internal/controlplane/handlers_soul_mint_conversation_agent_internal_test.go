package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	mintConversationInstanceReadTestAuthHeader   = "authorization"
	mintConversationInstanceReadTestRawKey       = "raw-key"
	mintConversationInstanceReadTestAgent        = "agent"
	mintConversationInstanceReadTestConv         = "conv"
	mintConversationInstanceReadMessageBad       = "bad"
	mintConversationInstanceReadMessageMissing   = "missing"
	mintConversationInstanceReadMessageState     = "state"
	mintConversationInstanceReadNameNotFound     = "not found"
	mintConversationInstanceReadNameUnauthorized = "unauthorized"
	mintConversationInstanceReadValueTrue        = "true"
)

// requireMintConversationListQueryShape asserts the list query ran against the
// gsi4 agent-scoped time-ordered index with a gsi4PK partition, gsi4SK DESC
// ordering, and a bounded Limit. Any of these regressing to the old
// SK-ordered base-table query fails the test (issue #1067 part C2).
func requireMintConversationListQueryShape(t *testing.T, captured mintConversationListQueryCapture, wantAgentID string, wantLimit int) {
	t.Helper()
	if !captured.hasIndex || captured.index != "gsi4" {
		t.Fatalf("expected query Index(gsi4), got index=%q hasIndex=%v", captured.index, captured.hasIndex)
	}
	if captured.wherePK != "SOUL#AGENT#"+wantAgentID {
		t.Fatalf("expected Where(gsi4PK = SOUL#AGENT#%s), got %q", wantAgentID, captured.wherePK)
	}
	if captured.orderBy != "gsi4SK" || captured.order != soulMintConversationGSI4DescOrder {
		t.Fatalf("expected OrderBy(gsi4SK, %s), got OrderBy(%q, %q)", soulMintConversationGSI4DescOrder, captured.orderBy, captured.order)
	}
	if captured.limit != wantLimit {
		t.Fatalf("expected Limit(%d), got Limit(%d)", wantLimit, captured.limit)
	}
}

type mintConversationListQueryCapture struct {
	index    string
	wherePK  string
	orderBy  string
	order    string
	limit    int
	hasIndex bool
}

// captureMintConversationListQueryShape wires capture stubs for the list query
// builder so the test can assert the exact query shape the operator list runs.
func captureMintConversationListQueryShape(t *testing.T, q *ttmocks.MockQuery) *mintConversationListQueryCapture {
	t.Helper()
	captured := &mintConversationListQueryCapture{limit: -1}
	filterMockQueryCalls(q, "Index")
	filterMockQueryCalls(q, "Where")
	filterMockQueryCalls(q, "OrderBy")
	filterMockQueryCalls(q, "Limit")
	q.On("Index", mock.Anything).Return(q).Run(func(args mock.Arguments) {
		captured.index = testutil.RequireMockArg[string](t, args, 0)
		captured.hasIndex = true
	}).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Run(func(args mock.Arguments) {
		field := testutil.RequireMockArg[string](t, args, 0)
		if field == "gsi4PK" {
			captured.wherePK = testutil.RequireMockArg[string](t, args, 2)
		}
	}).Maybe()
	q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Run(func(args mock.Arguments) {
		captured.orderBy = testutil.RequireMockArg[string](t, args, 0)
		captured.order = testutil.RequireMockArg[string](t, args, 1)
	}).Maybe()
	q.On("Limit", mock.Anything).Return(q).Run(func(args mock.Arguments) {
		captured.limit = testutil.RequireMockArg[int](t, args, 0)
	}).Maybe()
	return captured
}

// TestHandleSoulAgentListMintConversations_SortsNewestFirst pins the selection
// semantics of the operator mint-conversation list (issue #1067, part C2 of
// #1061). The base SK is a crypto/rand token with no recency meaning, so the
// list must answer from the gsi4 agent-scoped time-ordered index: a regression
// that selects a page by SK order (an arbitrary subset beyond the limit) must
// fail this test. The query shape is asserted (Index gsi4, gsi4PK partition,
// OrderBy gsi4SK DESC, Limit) AND the seeded page is ordered by SK token in
// the OPPOSITE direction of recency, so the response can only contain the
// genuinely newest conversations when the query actually went through the
// index.
func TestHandleSoulAgentListMintConversations_SortsNewestFirst(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationDomainAccess(t, tdb, identity.Domain)

	captured := captureMintConversationListQueryShape(t, tdb.qConv)

	// Five conversations. The SK token ("zz".."aa") is deliberately reversed
	// against recency, so an SK-ordered page would select the OLDEST
	// conversations first; only the gsi4 recency-ordered page yields the newest.
	base := time.Date(2026, 3, 7, 11, 0, 0, 0, time.UTC)
	seeded := []*models.SoulAgentMintConversation{
		{AgentID: identity.AgentID, ConversationID: "zz-oldest", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base},                   // t0
		{AgentID: identity.AgentID, ConversationID: "yy", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base.Add(time.Hour)},           // t1
		{AgentID: identity.AgentID, ConversationID: "xx", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base.Add(2 * time.Hour)},       // t2
		{AgentID: identity.AgentID, ConversationID: "bb", Status: models.SoulMintConversationStatusCompleted, CreatedAt: base.Add(3 * time.Hour)},        // t3
		{AgentID: identity.AgentID, ConversationID: "aa-newest", Status: models.SoulMintConversationStatusCompleted, CreatedAt: base.Add(4 * time.Hour)}, // t4
	}
	tdb.qConv.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *[]*models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		// The gsi4 index returns the page ordered by gsi4SK DESC (createdAt
		// DESC): the newest two conversations for limit=2.
		*dest = []*models.SoulAgentMintConversation{seeded[4], seeded[3]}
	}).Once()

	ctx := adminCtx()
	ctx.AuthIdentity = testUsernameAlice
	ctx.Params = map[string]string{"agentId": identity.AgentID}
	ctx.Request.Query = map[string][]string{"limit": {"2"}}

	resp, err := s.handleSoulAgentListMintConversations(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	requireMintConversationListQueryShape(t, *captured, identity.AgentID, 2)

	var out soulAgentMintConversationsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 2 || len(out.Conversations) != 2 {
		t.Fatalf("expected bounded page of 2 conversations, got %#v", out)
	}
	// Selection semantics: the response must contain the genuinely newest
	// conversations, not whatever the SK order happened to pick.
	if out.Conversations[0].ConversationID != "aa-newest" || out.Conversations[1].ConversationID != "bb" {
		t.Fatalf("expected newest conversations selected first, got %#v", out.Conversations)
	}
}

// TestHandleSoulAgentListMintConversations_FailsClosedWhenBackfillIncomplete
// pins the fail-closed gate (issue #1067, part C2 of #1061): until the operator
// has completed the gsi4 backfill for SoulAgentMintConversation, the list must
// fail explicitly instead of silently returning a partial conversation set.
func TestHandleSoulAgentListMintConversations_FailsClosedWhenBackfillIncomplete(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationDomainAccess(t, tdb, identity.Domain)

	// Simulate a pre-backfill table: the gsi4 completeness marker is absent.
	tdb.qMarker.ExpectedCalls = nil
	tdb.qMarker.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(tdb.qMarker).Maybe()
	tdb.qMarker.On("First", mock.AnythingOfType("*models.SoulAgentMintConversationGSI4BackfillMarker")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := adminCtx()
	ctx.AuthIdentity = testUsernameAlice
	ctx.Params = map[string]string{"agentId": identity.AgentID}
	ctx.Request.Query = map[string][]string{"limit": {"10"}}

	_, err := s.handleSoulAgentListMintConversations(ctx)
	appErr := requireAppTheoryError(t, err)
	if appErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 fail-closed, got %#v", appErr)
	}
	if !strings.Contains(appErr.Message, "backfill not complete") {
		t.Fatalf("expected explicit backfill error, got %q", appErr.Message)
	}
	// The list query must never run when the gate is closed.
	tdb.qConv.AssertNotCalled(t, "All")
}

// TestHandleSoulAgentListMintConversations_FailsClosedWhenGSIQueryFails pins
// the index-absent failure shape: a gsi4 query error (for example the index has
// not been created by the stack update yet) propagates as an explicit failure,
// never a silent empty/partial page.
func TestHandleSoulAgentListMintConversations_FailsClosedWhenGSIQueryFails(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationDomainAccess(t, tdb, identity.Domain)

	tdb.qConv.On("All", mock.Anything).Return(errors.New("gsi4 boom")).Once()

	ctx := adminCtx()
	ctx.AuthIdentity = testUsernameAlice
	ctx.Params = map[string]string{"agentId": identity.AgentID}
	ctx.Request.Query = map[string][]string{"limit": {"10"}}

	_, err := s.handleSoulAgentListMintConversations(ctx)
	appErr := requireAppTheoryError(t, err)
	if appErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 fail-closed, got %#v", appErr)
	}
	if !strings.Contains(appErr.Message, "failed to list mint conversations") {
		t.Fatalf("expected explicit list failure, got %q", appErr.Message)
	}
}

func TestHandleSoulAgentListMintConversations_AppliesLimitToQuery(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationDomainAccess(t, tdb, identity.Domain)

	// Capture the limit actually applied to the query builder. The default
	// Maybe Limit stub is filtered out so this capture stub is the one
	// testify matches.
	appliedLimit := 0
	filterMockQueryCalls(tdb.qConv, "Limit")
	tdb.qConv.On("Limit", mock.Anything).Return(tdb.qConv).Run(func(args mock.Arguments) {
		appliedLimit = testutil.RequireMockArg[int](t, args, 0)
	}).Maybe()

	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	seeded := []*models.SoulAgentMintConversation{
		{AgentID: identity.AgentID, ConversationID: "conv-0", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base},
		{AgentID: identity.AgentID, ConversationID: "conv-1", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base.Add(time.Minute)},
		{AgentID: identity.AgentID, ConversationID: "conv-2", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base.Add(2 * time.Minute)},
		{AgentID: identity.AgentID, ConversationID: "conv-3", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base.Add(3 * time.Minute)},
		{AgentID: identity.AgentID, ConversationID: "conv-4", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base.Add(4 * time.Minute)},
	}
	tdb.qConv.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentMintConversation](t, args, 0)
		*dest = seeded
	}).Once()

	ctx := adminCtx()
	ctx.AuthIdentity = testUsernameAlice
	ctx.Params = map[string]string{"agentId": identity.AgentID}
	ctx.Request.Query = map[string][]string{"limit": {"2"}}

	resp, err := s.handleSoulAgentListMintConversations(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	if appliedLimit != 2 {
		t.Fatalf("expected query Limit(2), got Limit(%d)", appliedLimit)
	}

	var out soulAgentMintConversationsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 2 || len(out.Conversations) != 2 {
		t.Fatalf("expected bounded page of 2 conversations, got %#v", out)
	}
}

func TestHandleSoulInstanceListMintConversations_AppliesLimitToQuery(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

	appliedLimit := 0
	filterMockQueryCalls(tdb.qConv, "Limit")
	tdb.qConv.On("Limit", mock.Anything).Return(tdb.qConv).Run(func(args mock.Arguments) {
		appliedLimit = testutil.RequireMockArg[int](t, args, 0)
	}).Maybe()

	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	seeded := []*models.SoulAgentMintConversation{
		{AgentID: identity.AgentID, ConversationID: "conv-0", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base},
		{AgentID: identity.AgentID, ConversationID: "conv-1", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base.Add(time.Minute)},
		{AgentID: identity.AgentID, ConversationID: "conv-2", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base.Add(2 * time.Minute)},
		{AgentID: identity.AgentID, ConversationID: "conv-3", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base.Add(3 * time.Minute)},
		{AgentID: identity.AgentID, ConversationID: "conv-4", Status: models.SoulMintConversationStatusInProgress, CreatedAt: base.Add(4 * time.Minute)},
	}
	tdb.qConv.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentMintConversation](t, args, 0)
		*dest = seeded
	}).Once()

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", map[string][]string{"limit": {"2"}})

	resp, err := s.handleSoulInstanceListMintConversations(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	if appliedLimit != 2 {
		t.Fatalf("expected query Limit(2), got Limit(%d)", appliedLimit)
	}

	var out soulInstanceMintConversationsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 2 || len(out.Conversations) != 2 {
		t.Fatalf("expected bounded page of 2 conversations, got %#v", out)
	}
}

func TestHandleSoulAgentGetMintConversation_AllowsPendingAgent(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationDomainAccess(t, tdb, identity.Domain)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        identity.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         models.SoulMintConversationStatusInProgress,
		Model:          "claude-sonnet-5",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})

	ctx := &apptheory.Context{
		AuthIdentity: testUsernameAlice,
		Params: map[string]string{
			"agentId":        identity.AgentID,
			"conversationId": mintConversationTestConversationID,
		},
	}

	resp, err := s.handleSoulAgentGetMintConversation(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestHandleSoulInstanceListMintConversations_UsesInstanceKeyAndCompactDTO(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

	tdb.qConv.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentMintConversation](t, args, 0)
		*dest = []*models.SoulAgentMintConversation{
			{
				AgentID:              identity.AgentID,
				ConversationID:       "conv-old",
				Model:                "claude-sonnet-5",
				Messages:             `secret-message-body`,
				ProducedDeclarations: `{"secret":true}`,
				Status:               models.SoulMintConversationStatusInProgress,
				Usage:                models.AIUsage{TotalTokens: 10},
				ChargedCredits:       3,
				CreatedAt:            time.Date(2026, 3, 7, 11, 0, 0, 0, time.UTC),
			},
			{
				AgentID:              identity.AgentID,
				ConversationID:       "conv-new",
				Model:                "claude-sonnet-5",
				Messages:             `new-secret-message-body`,
				ProducedDeclarations: `{"newSecret":true}`,
				Status:               models.SoulMintConversationStatusCompleted,
				CreatedAt:            time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
			},
		}
	}).Once()

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", map[string][]string{"limit": {"50"}})

	resp, err := s.handleSoulInstanceListMintConversations(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	body := string(resp.Body)
	if strings.Contains(body, "secret-message-body") || strings.Contains(body, "produced_declarations") || strings.Contains(body, "messages") {
		t.Fatalf("compact instance-key list leaked private fields: %s", body)
	}

	var out soulInstanceMintConversationsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 2 || out.Limit != 50 || len(out.Conversations) != 2 {
		t.Fatalf("unexpected compact response: %#v", out)
	}
	if out.Conversations[0].ConversationID != "conv-new" {
		t.Fatalf("expected newest conversation first, got %#v", out.Conversations)
	}
	tdb.qAudit.AssertCalled(t, "Create")
}

func TestHandleSoulInstanceGetMintConversation_ReturnsHostedGenesisMessages(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:              identity.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Model:                "claude-sonnet-5",
		Messages:             encodeMintConversationBlob(`[{"role":"user","content":"describe yourself"},{"role":"assistant","content":"I am ready."}]`),
		ProducedDeclarations: encodeMintConversationBlob(`{"private":true}`),
		Status:               models.SoulMintConversationStatusAssistantTurnReady,
		LatestTurnID:         "turn-ready",
		CreatedAt:            time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})

	ctx := newMintConversationInstanceReadContext(identity.AgentID, mintConversationTestConversationID, nil)

	resp, err := s.handleSoulInstanceGetMintConversation(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.ConversationID != mintConversationTestConversationID || out.Conversation.Status != models.SoulMintConversationStatusAssistantTurnReady || out.Conversation.Failure != nil {
		t.Fatalf("expected explicit single-conversation route to return hosted-genesis status, got %#v", out)
	}
	if len(out.Conversation.Messages) != 2 || out.Conversation.Messages[0].Role != hostedGenesisTranscriptRoleUser || out.Conversation.Messages[0].Content != "describe yourself" || out.Conversation.Messages[1].Role != hostedGenesisTranscriptRoleAssistant {
		t.Fatalf("expected bounded transcript messages, got %#v", out.Conversation.Messages)
	}
	if strings.Contains(string(resp.Body), `produced_declarations`) || strings.Contains(string(resp.Body), mintConversationInstanceReadTestRawKey) {
		t.Fatalf("hosted-genesis projection leaked declarations or credential: %s", string(resp.Body))
	}
}

func TestHandleSoulInstanceMintConversationReads_HostedOffchainDoesNotRequireRegistry(t *testing.T) {
	t.Parallel()

	t.Run("list succeeds with contractless hosted offchain config", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		s.cfg.SoulChainID = 0
		s.cfg.SoulRegistryContractAddress = ""
		s.cfg.SoulRPCURL = ""
		identity := testMintConversationIdentity()

		expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
		stubMintConversationIdentity(t, tdb, identity, nil)
		stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")
		tdb.qConv.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentMintConversation](t, args, 0)
			*dest = []*models.SoulAgentMintConversation{
				{
					AgentID:              identity.AgentID,
					ConversationID:       "conv-contractless",
					Model:                "claude-sonnet-5",
					Messages:             `private transcript`,
					ProducedDeclarations: `{"private":true}`,
					Status:               models.SoulMintConversationStatusInProgress,
					CreatedAt:            time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
				},
			}
		}).Once()

		resp, err := s.handleSoulInstanceListMintConversations(newMintConversationInstanceReadContext(identity.AgentID, "", nil))
		if err != nil {
			t.Fatalf("unexpected contractless hosted/off-chain list error: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%q", resp.Status, string(resp.Body))
		}
		body := string(resp.Body)
		if !strings.Contains(body, "conv-contractless") {
			t.Fatalf("expected listed conversation id, got %s", body)
		}
		if strings.Contains(body, "private transcript") || strings.Contains(body, "produced_declarations") || strings.Contains(body, mintConversationInstanceReadTestRawKey) {
			t.Fatalf("contractless list leaked private fields or credential: %s", body)
		}
	})

	t.Run("single get succeeds with contractless hosted offchain config", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		s.cfg.SoulChainID = 0
		s.cfg.SoulRegistryContractAddress = ""
		s.cfg.SoulRPCURL = ""
		identity := testMintConversationIdentity()

		expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
		stubMintConversationIdentity(t, tdb, identity, nil)
		stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")
		stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
			AgentID:        identity.AgentID,
			ConversationID: mintConversationTestConversationID,
			Model:          "claude-sonnet-5",
			Messages:       encodeMintConversationBlob(`[{"role":"user","content":"hello"},{"role":"assistant","content":"ready"}]`),
			Status:         models.SoulMintConversationStatusAssistantTurnReady,
			LatestTurnID:   "turn-contractless",
			CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		})

		resp, err := s.handleSoulInstanceGetMintConversation(newMintConversationInstanceReadContext(identity.AgentID, mintConversationTestConversationID, nil))
		if err != nil {
			t.Fatalf("unexpected contractless hosted/off-chain get error: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%q", resp.Status, string(resp.Body))
		}
		var out hostedGenesisConversationResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Conversation.ConversationID != mintConversationTestConversationID || out.Conversation.Status != models.SoulMintConversationStatusAssistantTurnReady {
			t.Fatalf("expected contractless hosted conversation, got %#v", out.Conversation)
		}
	})
}

func TestHandleSoulInstanceMintConversationReads_AuthenticateBeforeSoulEnabled(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.cfg.SoulEnabled = false
	identity := testMintConversationIdentity()
	tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()

	_, err := s.handleSoulInstanceListMintConversations(newMintConversationInstanceReadContext(identity.AgentID, "", nil))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulMintInstanceReadCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized before SoulEnabled disclosure, got %#v", appErr)
	}
	tdb.qKey.AssertNumberOfCalls(t, "First", 1)
}

func TestHandleSoulInstanceMintConversationReads_ValidKeyFailsClosedWhenSoulDisabled(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.cfg.SoulEnabled = false
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")

	_, err := s.handleSoulInstanceListMintConversations(newMintConversationInstanceReadContext(identity.AgentID, "", nil))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulMintInstanceReadCodeConflict || appErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected authenticated disabled-Soul conflict, got %#v", appErr)
	}
}

func TestHandleSoulInstanceMintConversationReads_RejectBoundaryAndInputFailures(t *testing.T) {
	t.Parallel()

	t.Run("tenant mismatch is forbidden", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		identity := testMintConversationIdentity()

		expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
		stubMintConversationIdentity(t, tdb, identity, nil)
		stubMintConversationInstanceDomain(t, tdb, identity.Domain, "other-inst")

		_, err := s.handleSoulInstanceListMintConversations(newMintConversationInstanceReadContext(identity.AgentID, "", nil))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulMintInstanceReadCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden {
			t.Fatalf("expected boundary violation 403, got %#v", appErr)
		}
		if appErr.Details["field"] != soulMintInstanceReadFieldAgentID || appErr.Details["reason"] != soulMintInstanceReadReasonTenantMismatch {
			t.Fatalf("expected tenant-boundary details, got %#v", appErr.Details)
		}
	})

	t.Run("invalid conversation id is rejected", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		identity := testMintConversationIdentity()

		expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
		stubMintConversationIdentity(t, tdb, identity, nil)
		stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

		_, err := s.handleSoulInstanceGetMintConversation(newMintConversationInstanceReadContext(identity.AgentID, strings.Repeat("a", soulMintInstanceReadConversationIDMaxLength+1), nil))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulMintInstanceReadCodeInvalidRequest || appErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected invalid request 400, got %#v", appErr)
		}
		if appErr.Details["field"] != soulMintInstanceReadFieldConversationID {
			t.Fatalf("expected conversationId details, got %#v", appErr.Details)
		}
	})

	t.Run("strict key lookup does not try plaintext fallback", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		identity := testMintConversationIdentity()

		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()

		_, err := s.handleSoulInstanceListMintConversations(newMintConversationInstanceReadContext(identity.AgentID, "", nil))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulMintInstanceReadCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized 401, got %#v", appErr)
		}
		tdb.qKey.AssertNumberOfCalls(t, "First", 1)
	})
}

func TestHandleSoulInstanceGetMintConversation_RejectsOversizeSingle(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        identity.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "claude-sonnet-5",
		Messages:       strings.Repeat("x", soulMintInstanceReadSingleMaxBytes+1),
		Status:         models.SoulMintConversationStatusCompleted,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})

	_, err := s.handleSoulInstanceGetMintConversation(newMintConversationInstanceReadContext(identity.AgentID, mintConversationTestConversationID, nil))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulMintInstanceReadCodeResponseTooLarge || appErr.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected response too large 413, got %#v", appErr)
	}
	if strings.Contains(appErr.Message, "xxx") {
		t.Fatalf("oversize error leaked private content: %q", appErr.Message)
	}
}

func TestSoulMintInstanceReadErrorMappingHelpers(t *testing.T) {
	t.Parallel()

	if got := soulMintInstanceReadErrorFromAppError(nil); got != nil {
		t.Fatalf("expected nil app error mapping, got %#v", got)
	}

	cases := []struct {
		name       string
		in         *apptheory.AppTheoryError
		wantCode   string
		wantStatus int
	}{
		{"bad request", newAppTheoryError(appErrCodeBadRequest, mintConversationInstanceReadMessageBad), soulMintInstanceReadCodeInvalidRequest, http.StatusBadRequest},
		{mintConversationInstanceReadNameNotFound, newAppTheoryError(soulMintAppErrCodeNotFound, mintConversationInstanceReadMessageMissing), soulMintInstanceReadCodeNotFound, http.StatusNotFound},
		{mintConversationInstanceReadNameUnauthorized, newAppTheoryError(appErrCodeUnauthorized, testNope), soulMintInstanceReadCodeUnauthorized, http.StatusUnauthorized},
		{"conflict", newAppTheoryError(soulMintAppErrCodeConflict, mintConversationInstanceReadMessageState), soulMintInstanceReadCodeConflict, http.StatusConflict},
		{"internal default", newAppTheoryError(soulMintAppErrCodeInternal, "boom"), soulMintInstanceReadCodeInternal, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			appErr := soulMintInstanceReadErrorFromAppError(tc.in)
			if appErr == nil || appErr.Code != tc.wantCode || appErr.StatusCode != tc.wantStatus {
				t.Fatalf("unexpected mapping: %#v", appErr)
			}
		})
	}

	if got := soulMintInstanceReadAccessError(nil); got != nil {
		t.Fatalf("expected nil access error mapping, got %#v", got)
	}
	if appErr := soulMintInstanceReadAccessError(newAppTheoryError(soulMintAppErrCodeInternal, "")); appErr.Code != soulMintInstanceReadCodeInternal || appErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected internal access mapping, got %#v", appErr)
	}
	if appErr := soulMintInstanceReadAccessError(newAppTheoryError(soulMintAppErrCodeConflict, "")); appErr.Code != soulMintInstanceReadCodeBoundaryViolation || appErr.Details["reason"] != soulMintInstanceReadReasonDomainNotVerified {
		t.Fatalf("expected domain boundary mapping, got %#v", appErr)
	}
}

func TestSoulMintInstanceReadConversationIDAndLimitHelpers(t *testing.T) {
	t.Parallel()

	if soulMintInstanceReadConversationIDSafe("") || soulMintInstanceReadConversationIDSafe("bad/id") {
		t.Fatalf("expected empty and slash conversation ids to be unsafe")
	}
	if !soulMintInstanceReadConversationIDSafe("conv:1_ok.test") {
		t.Fatalf("expected safe conversation id")
	}

	if limit, appErr := parseSoulMintInstanceReadLimit(&apptheory.Context{}); appErr != nil || limit != soulMintInstanceReadListDefaultLimit {
		t.Fatalf("expected default limit, got limit=%d err=%#v", limit, appErr)
	}
	if _, appErr := parseSoulMintInstanceReadLimit(&apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"limit": {"0"}}}}); appErr == nil || appErr.Code != soulMintInstanceReadCodeInvalidRequest {
		t.Fatalf("expected invalid limit error, got %#v", appErr)
	}
}

func TestSoulMintInstanceReadSummaryAndResponseHelpers(t *testing.T) {
	t.Parallel()

	if got := soulMintConversationSummaryFromModel(nil); got.ConversationID != "" {
		t.Fatalf("expected empty summary for nil model, got %#v", got)
	}
	summary := soulMintConversationSummaryFromModel(&models.SoulAgentMintConversation{
		AgentID:        " " + mintConversationInstanceReadTestAgent + " ",
		ConversationID: " " + mintConversationInstanceReadTestConv + " ",
		Status:         models.SoulMintConversationStatusInProgress,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	if summary.AgentID != mintConversationInstanceReadTestAgent || summary.ConversationID != mintConversationInstanceReadTestConv || summary.CompletedAt != nil {
		t.Fatalf("unexpected summary: %#v", summary)
	}

	if appErr := rejectOversizeSoulMintInstanceConversation(nil); appErr != nil {
		t.Fatalf("expected nil conversation oversize check to pass, got %#v", appErr)
	}
	if resp, err := soulMintInstanceReadJSON(http.StatusOK, map[string]string{"ok": mintConversationInstanceReadValueTrue}, 0); err != nil || resp.Status != http.StatusOK {
		t.Fatalf("expected JSON response, got resp=%#v err=%v", resp, err)
	}
	if _, err := soulMintInstanceReadJSON(http.StatusOK, map[string]string{"too": strings.Repeat("x", 8)}, 4); err == nil {
		t.Fatalf("expected JSON byte cap error")
	}
	if appErr := soulMintInstanceReadResponseError(soulMintInstanceReadError(soulMintInstanceReadCodeResponseTooLarge, "large", http.StatusRequestEntityTooLarge, nil)); appErr.Code != soulMintInstanceReadCodeResponseTooLarge {
		t.Fatalf("expected typed response error to pass through, got %#v", appErr)
	}
	if appErr := soulMintInstanceReadResponseError(errors.New("marshal failed")); appErr.Code != soulMintInstanceReadCodeInternal {
		t.Fatalf("expected generic response error to map internal, got %#v", appErr)
	}

	if ctxParam(nil, soulMintInstanceReadFieldAgentID) != "" ||
		ctxParam(&apptheory.Context{Params: map[string]string{soulMintInstanceReadFieldAgentID: " " + mintConversationInstanceReadTestAgent + " "}}, soulMintInstanceReadFieldAgentID) != mintConversationInstanceReadTestAgent {
		t.Fatalf("unexpected ctxParam behavior")
	}
}

func TestHandleSoulAgentMintConversation_ConflictsForPublishedAgent(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()
	identity.SelfDescriptionVersion = 1

	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationDomainAccess(t, tdb, identity.Domain)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{
		AuthIdentity: testUsernameAlice,
		Params:       map[string]string{"agentId": identity.AgentID},
	}

	if _, err := s.handleSoulAgentMintConversation(ctx); err == nil {
		t.Fatalf("expected published-agent conflict")
	}
}

func TestHandleSoulAgentCompleteMintConversation_ConflictsForPublishedAgent(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()
	identity.SelfDescriptionVersion = 1

	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationDomainAccess(t, tdb, identity.Domain)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{
		AuthIdentity: testUsernameAlice,
		Params: map[string]string{
			"agentId":        identity.AgentID,
			"conversationId": mintConversationTestConversationID,
		},
	}

	if _, err := s.handleSoulAgentCompleteMintConversation(ctx); err == nil {
		t.Fatalf("expected published-agent conflict")
	}
}

func TestHandleSoulAgentCompleteMintConversation_ReturnsCompletedConversationReplay(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()
	identity.SelfDescriptionVersion = 1
	declBytes := mustMarshalJSON(t, testMintConversationDecl())

	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationDomainAccess(t, tdb, identity.Domain)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:              identity.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Status:               models.SoulMintConversationStatusCompleted,
		ProducedDeclarations: encodeMintConversationBlob(string(declBytes)),
	})

	ctx := &apptheory.Context{
		AuthIdentity: testUsernameAlice,
		Params: map[string]string{
			"agentId":        identity.AgentID,
			"conversationId": mintConversationTestConversationID,
		},
	}

	resp, err := s.handleSoulAgentCompleteMintConversation(ctx)
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

func newMintConversationInstanceReadContext(agentID string, conversationID string, query map[string][]string) *apptheory.Context {
	params := map[string]string{"agentId": agentID}
	if conversationID != "" {
		params["conversationId"] = conversationID
	}
	return &apptheory.Context{
		RequestID: "req-instance-mint-read",
		Params:    params,
		Request: apptheory.Request{
			Headers: map[string][]string{mintConversationInstanceReadTestAuthHeader: {"Bearer " + mintConversationInstanceReadTestRawKey}},
			Query:   query,
		},
	}
}

func expectMintConversationInstanceKey(t *testing.T, tdb *mintConversationTestDB, rawKey string, instanceSlug string) {
	t.Helper()

	tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{
			ID:           sha256HexTrimmed(rawKey),
			InstanceSlug: instanceSlug,
			CreatedAt:    time.Now().Add(-time.Hour).UTC(),
		}
	}).Once()
}

func stubMintConversationInstanceDomain(t *testing.T, tdb *mintConversationTestDB, domain string, instanceSlug string) {
	t.Helper()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:       domain,
			InstanceSlug: instanceSlug,
			Status:       models.DomainStatusVerified,
		}
	}).Once()
}

func requireAppTheoryError(t *testing.T, err error) *apptheory.AppTheoryError {
	t.Helper()

	var appErr *apptheory.AppTheoryError
	if !errors.As(err, &appErr) || appErr == nil {
		t.Fatalf("expected AppTheoryError, got %T: %v", err, err)
	}
	return appErr
}

func assertMintConversationCompletionConflictDetails(t *testing.T, appErr *apptheory.AppTheoryError, wantCode string, wantStatusCode int, wantStatus string, wantPresent bool, wantValid bool, wantReason string) {
	t.Helper()

	if appErr.Code != wantCode || appErr.StatusCode != wantStatusCode || appErr.Message != soulMintConversationCompleteConflictMessage {
		t.Fatalf("expected completion-state conflict code=%s status=%d message=%q, got %#v", wantCode, wantStatusCode, soulMintConversationCompleteConflictMessage, appErr)
	}
	if got := appErr.Details[soulMintConversationCompleteDetailStatus]; got != wantStatus {
		t.Fatalf("expected conversation status detail %q, got %#v in %#v", wantStatus, got, appErr.Details)
	}
	if got := appErr.Details[soulMintConversationCompleteDetailDeclarationsPresent]; got != wantPresent {
		t.Fatalf("expected declarations-present detail %v, got %#v in %#v", wantPresent, got, appErr.Details)
	}
	if got := appErr.Details[soulMintConversationCompleteDetailDeclarationsValid]; got != wantValid {
		t.Fatalf("expected declarations-valid detail %v, got %#v in %#v", wantValid, got, appErr.Details)
	}
	if got := appErr.Details[soulMintConversationCompleteDetailReason]; got != wantReason {
		t.Fatalf("expected reason detail %q, got %#v in %#v", wantReason, got, appErr.Details)
	}
}

func TestHandleSoulAgentAliasRoutes_RequireConversationID(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	baseCtx := &apptheory.Context{
		AuthIdentity: testUsernameAlice,
		Params:       map[string]string{"agentId": identity.AgentID},
	}

	t.Run("complete", func(t *testing.T) {
		stubMintConversationIdentity(t, tdb, identity, nil)
		stubMintConversationDomainAccess(t, tdb, identity.Domain)
		if _, err := s.handleSoulAgentCompleteMintConversation(baseCtx); err == nil {
			t.Fatalf("expected missing conversationId error")
		}
	})

	t.Run("begin_finalize", func(t *testing.T) {
		stubMintConversationIdentity(t, tdb, identity, nil)
		stubMintConversationDomainAccess(t, tdb, identity.Domain)
		if _, err := s.handleSoulAgentBeginFinalizeMintConversation(baseCtx); err == nil {
			t.Fatalf("expected missing conversationId error")
		}
	})

	t.Run("preflight", func(t *testing.T) {
		stubMintConversationIdentity(t, tdb, identity, nil)
		stubMintConversationDomainAccess(t, tdb, identity.Domain)
		if _, err := s.handleSoulAgentFinalizeMintConversationPreflight(baseCtx); err == nil {
			t.Fatalf("expected missing conversationId error")
		}
	})

	t.Run("finalize", func(t *testing.T) {
		stubMintConversationIdentity(t, tdb, identity, nil)
		stubMintConversationDomainAccess(t, tdb, identity.Domain)
		if _, err := s.handleSoulAgentFinalizeMintConversation(baseCtx); err == nil {
			t.Fatalf("expected missing conversationId error")
		}
	})
}
