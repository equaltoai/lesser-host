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

func TestHandleSoulAgentListMintConversations_SortsNewestFirst(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()

	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationDomainAccess(t, tdb, identity.Domain)

	tdb.qConv.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *[]*models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = []*models.SoulAgentMintConversation{
			{
				AgentID:        identity.AgentID,
				ConversationID: "conv-old",
				Status:         models.SoulMintConversationStatusInProgress,
				CreatedAt:      time.Date(2026, 3, 7, 11, 0, 0, 0, time.UTC),
			},
			{
				AgentID:        identity.AgentID,
				ConversationID: "conv-new",
				Status:         models.SoulMintConversationStatusCompleted,
				CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
			},
		}
	}).Once()

	ctx := adminCtx()
	ctx.AuthIdentity = testUsernameAlice
	ctx.Params = map[string]string{"agentId": identity.AgentID}
	ctx.Request.Query = map[string][]string{"limit": {"10"}}

	resp, err := s.handleSoulAgentListMintConversations(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out soulAgentMintConversationsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 2 || len(out.Conversations) != 2 {
		t.Fatalf("unexpected count: %#v", out)
	}
	if out.Conversations[0].ConversationID != "conv-new" {
		t.Fatalf("expected newest conversation first, got %#v", out.Conversations)
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
