package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

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
		Model:          "anthropic:claude-sonnet-4-6",
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
				Model:                "anthropic:claude-sonnet-4-6",
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
				Model:                "anthropic:claude-sonnet-4-6",
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

func TestHandleSoulInstanceGetMintConversation_ReturnsFullBoundedSingle(t *testing.T) {
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
		Model:                "anthropic:claude-sonnet-4-6",
		Messages:             encodeMintConversationBlob(`[{"role":"user","content":"private"}]`),
		ProducedDeclarations: encodeMintConversationBlob(`{"private":true}`),
		Status:               models.SoulMintConversationStatusCompleted,
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

	var out soulInstanceMintConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation == nil || !strings.Contains(out.Conversation.Messages, `"private"`) || !strings.Contains(out.Conversation.ProducedDeclarations, `"private":true`) {
		t.Fatalf("expected explicit single-conversation route to include full bounded record, got %#v", out)
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
		Model:          "anthropic:claude-sonnet-4-6",
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
		in         *apptheory.AppError
		wantCode   string
		wantStatus int
	}{
		{"bad request", &apptheory.AppError{Code: appErrCodeBadRequest, Message: mintConversationInstanceReadMessageBad}, soulMintInstanceReadCodeInvalidRequest, http.StatusBadRequest},
		{mintConversationInstanceReadNameNotFound, &apptheory.AppError{Code: soulMintAppErrCodeNotFound, Message: mintConversationInstanceReadMessageMissing}, soulMintInstanceReadCodeNotFound, http.StatusNotFound},
		{mintConversationInstanceReadNameUnauthorized, &apptheory.AppError{Code: appErrCodeUnauthorized, Message: testNope}, soulMintInstanceReadCodeUnauthorized, http.StatusUnauthorized},
		{"conflict", &apptheory.AppError{Code: soulMintAppErrCodeConflict, Message: mintConversationInstanceReadMessageState}, soulMintInstanceReadCodeConflict, http.StatusConflict},
		{"internal default", &apptheory.AppError{Code: soulMintAppErrCodeInternal, Message: "boom"}, soulMintInstanceReadCodeInternal, http.StatusInternalServerError},
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
	if appErr := soulMintInstanceReadAccessError(&apptheory.AppError{Code: soulMintAppErrCodeInternal}); appErr.Code != soulMintInstanceReadCodeInternal || appErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected internal access mapping, got %#v", appErr)
	}
	if appErr := soulMintInstanceReadAccessError(&apptheory.AppError{Code: soulMintAppErrCodeConflict}); appErr.Code != soulMintInstanceReadCodeBoundaryViolation || appErr.Details["reason"] != soulMintInstanceReadReasonDomainNotVerified {
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
