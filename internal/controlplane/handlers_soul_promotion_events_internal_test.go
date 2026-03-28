package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/theory-cloud/tabletheory/pkg/core"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestSoulAgentPromotionLifecycleEventHelpers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 28, 18, 30, 0, 0, time.UTC)
	promotion := readyForFinalizePromotionSnapshot(now)
	event := buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
		EventType:      models.SoulAgentPromotionEventTypeFinalizeReady,
		RequestID:      "rid-1",
		ConversationID: "conv-1",
		OccurredAt:     now,
	})

	if event == nil || event.EventType != models.SoulAgentPromotionEventTypeFinalizeReady {
		t.Fatalf("unexpected event: %#v", event)
	}
	if event.ConversationID != "conv-1" || event.OperationID != promotion.MintOperationID {
		t.Fatalf("expected conversation and operation ids, got %#v", event)
	}
	if event.Summary != "review draft ready for finalize" {
		t.Fatalf("unexpected summary: %q", event.Summary)
	}
	if !shouldEmitSoulPromotionReviewStartedEvent(nil, &models.SoulAgentPromotion{ReviewStatus: models.SoulAgentPromotionReviewStatusConversationInProgress}, "conv-1") {
		t.Fatalf("expected fresh in-progress review event")
	}
	if shouldEmitSoulPromotionReviewStartedEvent(
		&models.SoulAgentPromotion{ReviewStatus: models.SoulAgentPromotionReviewStatusConversationInProgress, LatestConversationID: "conv-1"},
		&models.SoulAgentPromotion{ReviewStatus: models.SoulAgentPromotionReviewStatusConversationInProgress, LatestConversationID: "conv-1"},
		"conv-1",
	) {
		t.Fatalf("expected duplicate in-progress review event to be suppressed")
	}
}

func TestHandleSoulListMyPromotionLifecycleEvents_ReturnsPagedViews(t *testing.T) {
	t.Parallel()

	tdb := newSoulRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	tdb.qLifecycle.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentPromotionLifecycleEvent")).Return(&core.PaginatedResult{
		HasMore:    true,
		NextCursor: " next ",
	}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPromotionLifecycleEvent](t, args, 0)
		*dest = []*models.SoulAgentPromotionLifecycleEvent{
			{
				EventID:                  "finalize_ready#1",
				EventType:                models.SoulAgentPromotionEventTypeFinalizeReady,
				Summary:                  "review draft ready for finalize",
				OccurredAt:               time.Date(2026, 3, 28, 18, 0, 0, 0, time.UTC),
				AgentID:                  soulLifecycleTestAgentIDHex,
				RequestedBy:              testUsernameAlice,
				RegistrationID:           "reg-1",
				Domain:                   "example.com",
				LocalID:                  "agent-bot",
				Wallet:                   "0x00000000000000000000000000000000000000aa",
				Stage:                    models.SoulAgentPromotionStageReadyToFinalize,
				RequestStatus:            models.SoulAgentPromotionRequestStatusMinted,
				ReviewStatus:             models.SoulAgentPromotionReviewStatusDraftReady,
				ApprovalStatus:           models.SoulAgentPromotionApprovalStatusApproved,
				ReadinessStatus:          models.SoulAgentPromotionReadinessReadyForFinalize,
				MintOperationID:          "op-1",
				ConversationID:           "conv-1",
				PublishedVersion:         0,
				CreatedAt:                time.Date(2026, 3, 28, 17, 0, 0, 0, time.UTC),
				UpdatedAt:                time.Date(2026, 3, 28, 18, 0, 0, 0, time.UTC),
				ReviewReadyAt:            time.Date(2026, 3, 28, 18, 0, 0, 0, time.UTC),
				LatestConversationID:     "conv-1",
				LatestConversationStatus: models.SoulMintConversationStatusCompleted,
			},
		}
	}).Once()

	ctx := adminCtx()
	ctx.AuthIdentity = testUsernameAlice
	ctx.Request.Query = map[string][]string{"limit": {"10"}, "cursor": {" cursor "}}

	resp, err := s.handleSoulListMyPromotionLifecycleEvents(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("unexpected response: %#v err=%v", resp, err)
	}

	var out soulAgentPromotionLifecycleEventListResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Events) != 1 || !out.HasMore || out.NextCursor != testSoulPaginationNextCursor {
		t.Fatalf("unexpected event list response: %#v", out)
	}
	if out.Events[0].EventType != models.SoulAgentPromotionEventTypeFinalizeReady {
		t.Fatalf("unexpected event view: %#v", out.Events[0])
	}
	if out.Events[0].Promotion.NextActions[0] != testSoulPromotionActionBeginFinalize {
		t.Fatalf("expected finalize next action, got %#v", out.Events[0].Promotion)
	}
}

func TestHandleSoulAgentListPromotionLifecycleEvents_ReturnsAgentFeed(t *testing.T) {
	t.Parallel()

	tdb := newSoulRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	tdb.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentPromotion](t, args, 0)
		*dest = *readyForFinalizePromotionSnapshot(time.Date(2026, 3, 28, 18, 0, 0, 0, time.UTC))
	}).Once()
	tdb.qLifecycle.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentPromotionLifecycleEvent")).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPromotionLifecycleEvent](t, args, 0)
		*dest = []*models.SoulAgentPromotionLifecycleEvent{
			{
				EventID:                  "graduated#1",
				EventType:                models.SoulAgentPromotionEventTypeGraduated,
				Summary:                  "promotion graduated",
				OccurredAt:               time.Date(2026, 3, 28, 19, 0, 0, 0, time.UTC),
				AgentID:                  soulLifecycleTestAgentIDHex,
				RequestedBy:              testUsernameAlice,
				RegistrationID:           "reg-1",
				Domain:                   "example.com",
				LocalID:                  "agent-bot",
				Wallet:                   "0x00000000000000000000000000000000000000aa",
				Stage:                    models.SoulAgentPromotionStageGraduated,
				RequestStatus:            models.SoulAgentPromotionRequestStatusGraduated,
				ReviewStatus:             models.SoulAgentPromotionReviewStatusPublished,
				ApprovalStatus:           models.SoulAgentPromotionApprovalStatusApproved,
				ReadinessStatus:          models.SoulAgentPromotionReadinessGraduated,
				MintOperationID:          "op-1",
				ConversationID:           "conv-1",
				PublishedVersion:         2,
				LatestConversationID:     "conv-1",
				LatestConversationStatus: models.SoulMintConversationStatusCompleted,
				GraduatedAt:              time.Date(2026, 3, 28, 19, 0, 0, 0, time.UTC),
				CreatedAt:                time.Date(2026, 3, 28, 17, 0, 0, 0, time.UTC),
				UpdatedAt:                time.Date(2026, 3, 28, 19, 0, 0, 0, time.UTC),
			},
		}
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}

	resp, err := s.handleSoulAgentListPromotionLifecycleEvents(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("unexpected response: %#v err=%v", resp, err)
	}

	var out soulAgentPromotionLifecycleEventListResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Events) != 1 {
		t.Fatalf("unexpected agent event response: %#v", out)
	}
	if out.Events[0].Promotion.PublishedVersion != 2 || !out.Events[0].Promotion.Prerequisites.Graduated {
		t.Fatalf("expected graduated promotion snapshot, got %#v", out.Events[0].Promotion)
	}
}

func readyForFinalizePromotionSnapshot(now time.Time) *models.SoulAgentPromotion {
	promotion := buildSoulAgentPromotionFromRegistration(&models.SoulAgentRegistration{
		ID:               "reg-1",
		Username:         testUsernameAlice,
		AgentID:          soulLifecycleTestAgentIDHex,
		DomainNormalized: "example.com",
		LocalID:          "agent-bot",
		Wallet:           "0x00000000000000000000000000000000000000aa",
	}, now.Add(-4*time.Minute))
	promotion = updateSoulAgentPromotionForVerification(promotion, &models.SoulAgentRegistration{
		ID:               "reg-1",
		Username:         testUsernameAlice,
		AgentID:          soulLifecycleTestAgentIDHex,
		DomainNormalized: "example.com",
		LocalID:          "agent-bot",
		Wallet:           "0x00000000000000000000000000000000000000aa",
	}, &models.SoulOperation{OperationID: "op-1", Status: models.SoulOperationStatusPending}, "0x00000000000000000000000000000000000000bb", now.Add(-3*time.Minute))
	promotion = updateSoulAgentPromotionForMintExecution(promotion, &models.SoulOperation{OperationID: "op-1", Status: models.SoulOperationStatusExecuted}, now.Add(-2*time.Minute))
	promotion = updateSoulAgentPromotionForConversation(promotion, "conv-1", models.SoulMintConversationStatusCompleted, now.Add(-time.Minute))
	return updateSoulAgentPromotionReviewDigest(promotion, `{"declarations":[{"id":"b1"}],"boundaries":[{"id":"b1"}],"capabilities":["social"]}`)
}
