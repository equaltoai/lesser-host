package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
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
