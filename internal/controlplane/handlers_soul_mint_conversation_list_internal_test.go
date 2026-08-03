package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// constTestListRegPrefix avoids goconst collisions with "reg-1" used in other
// test files in this package.
const constTestListRegPrefix = "list-reg"

// mockMethodAll is the testify mock method name for DynamoDB All(). Centralized
// to avoid goconst duplicate-string findings across the filter helpers below.
const mockMethodAll = "All"

func filterMockAllCalls(q *ttmocks.MockQuery) {
	var filtered []*mock.Call
	for _, call := range q.ExpectedCalls {
		if call.Method != mockMethodAll {
			filtered = append(filtered, call)
		}
	}
	q.ExpectedCalls = filtered
}

func TestSoulInstanceListMintConversationSummaries_ReturnsConversationsForAgent(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

	baseTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	stubHostedGenesisSessionList(t, tdb, []models.HostedGenesisSession{
		{
			InstanceSlug:   "inst1",
			RegistrationID: constTestListRegPrefix + "-a",
			AgentID:        identity.AgentID,
			ConversationID: "conv-a",
			Status:         string(hostedgenesis.StatusInProgress),
			LatestTurnID:   "turn-1",
			MessageCount:   3,
			CreatedAt:      baseTime,
			UpdatedAt:      baseTime.Add(time.Hour),
		},
		{
			InstanceSlug:   "inst1",
			RegistrationID: constTestListRegPrefix + "-b",
			AgentID:        identity.AgentID,
			ConversationID: "conv-b",
			Status:         string(hostedgenesis.StatusDeclarationReady),
			LatestTurnID:   "turn-2",
			MessageCount:   5,
			CreatedAt:      baseTime.Add(2 * time.Hour),
			UpdatedAt:      baseTime.Add(3 * time.Hour),
		},
	})

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", map[string][]string{"limit": {"50"}})

	resp, err := s.handleSoulInstanceListMintConversationSummaries(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out soulInstanceMintConversationListResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(out.Conversations) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(out.Conversations))
	}
	assertListSummaryValid(t, out.Conversations[0], "conv-b", constTestListRegPrefix+"-b", "turn-2", 5)
	assertListSummaryValid(t, out.Conversations[1], "conv-a", constTestListRegPrefix+"-a", "turn-1", 3)
}

func assertListSummaryValid(t *testing.T, c soulInstanceMintConversationListSummary, wantConv, wantReg, wantTurn string, wantMsgCount int) {
	t.Helper()
	if c.ConversationID != wantConv {
		t.Fatalf("expected conversation_id %q, got %q", wantConv, c.ConversationID)
	}
	if c.RegistrationID != wantReg {
		t.Fatalf("expected registration_id %q, got %q", wantReg, c.RegistrationID)
	}
	if c.Status == "" {
		t.Fatalf("status must not be empty: %#v", c)
	}
	if c.MessageCount != wantMsgCount {
		t.Fatalf("expected message_count %d, got %d", wantMsgCount, c.MessageCount)
	}
	if c.LatestTurnID != wantTurn {
		t.Fatalf("expected latest_turn_id %q, got %q", wantTurn, c.LatestTurnID)
	}
}

func TestSoulInstanceListMintConversationSummaries_BoundedToFifty(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

	baseTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	sessions := make([]models.HostedGenesisSession, 55)
	for i := range sessions {
		sessions[i] = models.HostedGenesisSession{
			InstanceSlug:   "inst1",
			RegistrationID: fmt.Sprintf("%s-%03d", constTestListRegPrefix, i),
			AgentID:        identity.AgentID,
			ConversationID: fmt.Sprintf("conv-%03d", i),
			Status:         string(hostedgenesis.StatusInProgress),
			LatestTurnID:   fmt.Sprintf("turn-%03d", i),
			MessageCount:   i + 1,
			CreatedAt:      baseTime.Add(time.Duration(i) * time.Minute),
			UpdatedAt:      baseTime.Add(time.Duration(i) * time.Minute),
		}
	}
	stubHostedGenesisSessionList(t, tdb, sessions)

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", map[string][]string{"limit": {"50"}})

	resp, err := s.handleSoulInstanceListMintConversationSummaries(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out soulInstanceMintConversationListResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Conversations) != 50 {
		t.Fatalf("expected exactly 50 conversations (bounded), got %d", len(out.Conversations))
	}
}

func TestSoulInstanceListMintConversationSummaries_SortedByUpdatedAtDescending(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

	// Sessions intentionally not in updated_at order to verify sort.
	baseTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	stubHostedGenesisSessionList(t, tdb, []models.HostedGenesisSession{
		{
			InstanceSlug:   "inst1",
			RegistrationID: constTestListRegPrefix + "-old",
			AgentID:        identity.AgentID,
			ConversationID: "conv-old",
			Status:         string(hostedgenesis.StatusInProgress),
			MessageCount:   1,
			CreatedAt:      baseTime,
			UpdatedAt:      baseTime, // oldest updated_at
		},
		{
			InstanceSlug:   "inst1",
			RegistrationID: constTestListRegPrefix + "-newest",
			AgentID:        identity.AgentID,
			ConversationID: "conv-newest",
			Status:         string(hostedgenesis.StatusDeclarationReady),
			MessageCount:   2,
			CreatedAt:      baseTime.Add(2 * time.Hour),
			UpdatedAt:      baseTime.Add(5 * time.Hour), // newest updated_at
		},
		{
			InstanceSlug:   "inst1",
			RegistrationID: constTestListRegPrefix + "-mid",
			AgentID:        identity.AgentID,
			ConversationID: "conv-mid",
			Status:         string(hostedgenesis.StatusFailed),
			MessageCount:   3,
			CreatedAt:      baseTime.Add(time.Hour),
			UpdatedAt:      baseTime.Add(3 * time.Hour), // middle updated_at
		},
	})

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", map[string][]string{"limit": {"50"}})

	resp, err := s.handleSoulInstanceListMintConversationSummaries(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out soulInstanceMintConversationListResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Conversations) != 3 {
		t.Fatalf("expected 3 conversations, got %d", len(out.Conversations))
	}
	expected := []string{"conv-newest", "conv-mid", "conv-old"}
	for i, want := range expected {
		if out.Conversations[i].ConversationID != want {
			t.Fatalf("position %d: expected %q, got %q — full order: %#v", i, want, out.Conversations[i].ConversationID, out.Conversations)
		}
	}
}

func TestSoulInstanceListMintConversationSummaries_NoMessagesOrDeclarationsInSummary(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

	baseTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	stubHostedGenesisSessionList(t, tdb, []models.HostedGenesisSession{
		{
			InstanceSlug:           "inst1",
			RegistrationID:         constTestListRegPrefix + "-secret",
			AgentID:                identity.AgentID,
			ConversationID:         "conv-a",
			Status:                 string(hostedgenesis.StatusInProgress),
			MessageCount:           2,
			LatestTurnID:           "turn-1",
			InputCheckpointRef:     "checkpoint://secret/input",
			AssistantCheckpointRef: "checkpoint://secret/assistant",
			TurnLedger: []hostedgenesis.TurnLedgerEntry{{
				TurnID:         "turn-1",
				ChargedCredits: 5,
				MessageCount:   2,
			}},
			CreatedAt: baseTime,
			UpdatedAt: baseTime.Add(time.Hour),
		},
	})

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", nil)

	resp, err := s.handleSoulInstanceListMintConversationSummaries(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := string(resp.Body)
	for _, forbidden := range []string{
		"messages",
		"produced_declarations",
		"input_checkpoint_ref",
		"assistant_checkpoint_ref",
		"turn_ledger",
		"checkpoint://secret",
		"charged_credits",
		mintConversationInstanceReadTestRawKey,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("summary response leaked forbidden field %q: %s", forbidden, body)
		}
	}

	var out soulInstanceMintConversationListResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Conversations) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(out.Conversations))
	}
	c := out.Conversations[0]
	if c.ConversationID != "conv-a" || c.RegistrationID != constTestListRegPrefix+"-secret" {
		t.Fatalf("unexpected summary: %#v", c)
	}
}

func TestSoulInstanceListMintConversationSummaries_UnauthenticatedRejected(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)

	ctx := &apptheory.Context{
		RequestID: "req-no-auth",
		Params:    map[string]string{"agentId": "0xabc"},
		Request: apptheory.Request{
			Headers: map[string][]string{},
		},
	}

	_, err := s.handleSoulInstanceListMintConversationSummaries(ctx)
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulMintInstanceReadCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized 401, got %#v", appErr)
	}
}

func TestSoulInstanceListMintConversationSummaries_EmptyAgentReturnsEmptyList(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", nil)

	resp, err := s.handleSoulInstanceListMintConversationSummaries(ctx)
	if err != nil {
		t.Fatalf("unexpected error for empty agent: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200 for empty agent, got %d", resp.Status)
	}

	var out soulInstanceMintConversationListResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversations == nil {
		t.Fatalf("conversations must be an empty array, not nil")
	}
	if len(out.Conversations) != 0 {
		t.Fatalf("expected 0 conversations for empty agent, got %d", len(out.Conversations))
	}
}

func TestSoulInstanceListMintConversationSummaries_StoreErrorReturns500(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

	// Remove default Maybe All mock and replace with error return.
	filterMockAllCalls(tdb.qHosted)
	tdb.qHosted.On(mockMethodAll, mock.Anything).Return(fmt.Errorf("dynamodb unavailable")).Once()

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", nil)

	_, err := s.handleSoulInstanceListMintConversationSummaries(ctx)
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulMintInstanceReadCodeInternal || appErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected internal error 500, got %#v", appErr)
	}
}

func TestSoulInstanceListMintConversationSummaries_NotFoundReturnsEmptyList(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

	// NotFound from DynamoDB should be treated as empty list, not error.
	filterMockAllCalls(tdb.qHosted)
	tdb.qHosted.On(mockMethodAll, mock.Anything).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", nil)

	resp, err := s.handleSoulInstanceListMintConversationSummaries(ctx)
	if err != nil {
		t.Fatalf("unexpected error for not-found: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200 for not-found, got %d", resp.Status)
	}

	var out soulInstanceMintConversationListResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Conversations) != 0 {
		t.Fatalf("expected 0 conversations for not-found, got %d", len(out.Conversations))
	}
}

func TestSoulInstanceListMintConversationSummaries_InvalidLimitRejected(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, "inst1")

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", map[string][]string{"limit": {"999"}})

	_, err := s.handleSoulInstanceListMintConversationSummaries(ctx)
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulMintInstanceReadCodeInvalidRequest || appErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid request 400, got %#v", appErr)
	}
}

// stubHostedGenesisSessionList mocks the GSI2 query result for
// ListHostedGenesisSessionsByAgent, populating the destination slice with
// the provided sessions. Sessions are used as-is without BeforeCreate —
// the handler only reads domain fields, not computed PK/SK keys.
func stubHostedGenesisSessionList(t *testing.T, tdb *mintConversationTestDB, sessions []models.HostedGenesisSession) {
	t.Helper()

	items := make([]*models.HostedGenesisSession, len(sessions))
	for i := range sessions {
		cp := sessions[i]
		items[i] = &cp
	}
	// Append a nil entry to verify the handler skips nil sessions safely.
	items = append(items, nil)

	// Remove the default Maybe All mock from qHosted so the specific
	// Once mock below is the one testify matches.
	filterMockAllCalls(tdb.qHosted)

	tdb.qHosted.On(mockMethodAll, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.HostedGenesisSession](t, args, 0)
		*dest = items
	}).Once()
}
