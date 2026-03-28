package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestSoulAgentPromotionLifecycleTransitions(t *testing.T) {
	promotion, _ := readyForFinalizePromotion(t)
	if promotion.Stage != models.SoulAgentPromotionStageReadyToFinalize {
		t.Fatalf("expected ready-to-finalize stage, got %#v", promotion)
	}
	if promotion.RequestStatus != models.SoulAgentPromotionRequestStatusMinted {
		t.Fatalf("expected minted request status, got %#v", promotion)
	}
	if promotion.ReviewStatus != models.SoulAgentPromotionReviewStatusDraftReady {
		t.Fatalf("expected draft-ready review status, got %#v", promotion)
	}
	if promotion.ReadinessStatus != models.SoulAgentPromotionReadinessReadyForFinalize {
		t.Fatalf("expected finalize readiness, got %#v", promotion)
	}
}

func TestSoulAgentPromotionViewShowsFinalizePrereqs(t *testing.T) {
	promotion, _ := readyForFinalizePromotion(t)
	view := (&Server{}).buildSoulAgentPromotionView(promotion)

	if !view.Prerequisites.PrincipalDeclarationRecorded ||
		!view.Prerequisites.MintOperationCreated ||
		!view.Prerequisites.MintExecuted ||
		!view.Prerequisites.ConversationStarted ||
		!view.Prerequisites.ConversationCompleted ||
		!view.Prerequisites.ReviewDraftReady ||
		!view.Prerequisites.ReadyForFinalize {
		t.Fatalf("unexpected prerequisites before graduation: %#v", view.Prerequisites)
	}
	if len(view.NextActions) != 1 || view.NextActions[0] != "begin_finalize" {
		t.Fatalf("unexpected next actions: %#v", view.NextActions)
	}
	if view.LatestBoundaryCount != 1 || view.LatestCapabilityCount != 1 {
		t.Fatalf("expected declaration counts, got %#v", view)
	}
}

func TestSoulAgentPromotionViewShowsGraduation(t *testing.T) {
	promotion, now := readyForFinalizePromotion(t)
	promotion = updateSoulAgentPromotionForGraduation(promotion, 1, now.Add(time.Minute))

	view := (&Server{}).buildSoulAgentPromotionView(promotion)
	if !view.Prerequisites.Graduated || view.PublishedVersion != 1 {
		t.Fatalf("expected graduated promotion view, got %#v", view)
	}
	if view.NextActions != nil {
		t.Fatalf("expected no next actions after graduation, got %#v", view.NextActions)
	}
}

func TestSoulAgentPromotionHelperBranches(t *testing.T) {
	if view := (&Server{}).buildSoulAgentPromotionView(nil); view.AgentID != "" {
		t.Fatalf("expected empty view for nil promotion, got %#v", view)
	}
	if actions := soulAgentPromotionNextActions(nil); actions != nil {
		t.Fatalf("expected nil actions for nil promotion, got %#v", actions)
	}

	promotion := &models.SoulAgentPromotion{
		ReadinessStatus: models.SoulAgentPromotionReadinessReadyForConversation,
		ReviewStatus:    models.SoulAgentPromotionReviewStatusNotStarted,
	}
	if actions := soulAgentPromotionNextActions(promotion); len(actions) != 1 || actions[0] != "start_review_conversation" {
		t.Fatalf("unexpected start-review action: %#v", actions)
	}

	promotion = updateSoulAgentPromotionReviewDigest(promotion, "")
	if promotion.LatestReviewSHA256 != "" || promotion.LatestBoundaryCount != 0 || promotion.LatestCapabilityCount != 0 {
		t.Fatalf("expected digest reset, got %#v", promotion)
	}

	promotion = updateSoulAgentPromotionReviewDigest(promotion, `{"bad":"json"`)
	if promotion.LatestReviewSHA256 == "" {
		t.Fatalf("expected digest hash for invalid JSON")
	}
	if promotion.LatestBoundaryCount != 0 || promotion.LatestCapabilityCount != 0 {
		t.Fatalf("expected invalid JSON to leave zero counts, got %#v", promotion)
	}
}

func TestHandleSoulAgentGetPromotion_NotFound(t *testing.T) {
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

	tdb.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
	if _, err := s.handleSoulAgentGetPromotion(ctx); err == nil {
		t.Fatalf("expected not found")
	}
}

func TestHandleSoulAgentPromotionVerify_ConflictWithoutRegistration(t *testing.T) {
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
		*dest = models.SoulAgentPromotion{
			AgentID: soulLifecycleTestAgentIDHex,
			Domain:  "example.com",
			LocalID: "agent-bot",
			Wallet:  "0x00000000000000000000000000000000000000aa",
		}
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
	if _, err := s.handleSoulAgentPromotionVerify(ctx); err == nil {
		t.Fatalf("expected conflict")
	}
}

func TestHandleSoulListMyPromotions_ReturnsPagedViews(t *testing.T) {
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

	tdb.qPromotion.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentPromotion")).Return(&core.PaginatedResult{
		HasMore:    true,
		NextCursor: " next ",
	}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPromotion](t, args, 0)
		*dest = []*models.SoulAgentPromotion{
			{
				AgentID:         soulLifecycleTestAgentIDHex,
				RegistrationID:  "reg-1",
				RequestedBy:     "alice",
				Domain:          "example.com",
				LocalID:         "agent-bot",
				Wallet:          "0x00000000000000000000000000000000000000aa",
				Stage:           models.SoulAgentPromotionStageReviewing,
				RequestStatus:   models.SoulAgentPromotionRequestStatusMinted,
				ReviewStatus:    models.SoulAgentPromotionReviewStatusConversationInProgress,
				ApprovalStatus:  models.SoulAgentPromotionApprovalStatusApproved,
				ReadinessStatus: models.SoulAgentPromotionReadinessReadyForConversation,
				CreatedAt:       time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC),
				UpdatedAt:       time.Date(2026, 3, 5, 12, 5, 0, 0, time.UTC),
			},
		}
	}).Once()

	ctx := adminCtx()
	ctx.AuthIdentity = "alice"
	ctx.Request.Query = map[string][]string{"limit": {"10"}, "cursor": {" cursor "}}

	resp, err := s.handleSoulListMyPromotions(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("unexpected response: %#v err=%v", resp, err)
	}

	var out soulAgentPromotionListResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Promotions) != 1 || !out.HasMore || out.NextCursor != "next" {
		t.Fatalf("unexpected list response: %#v", out)
	}
	if out.Promotions[0].NextActions == nil || out.Promotions[0].NextActions[0] != "complete_review_conversation" {
		t.Fatalf("expected promotion next action, got %#v", out.Promotions[0])
	}
}

func readyForFinalizePromotion(t *testing.T) (*models.SoulAgentPromotion, time.Time) {
	t.Helper()

	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	reg := &models.SoulAgentRegistration{
		ID:               "reg-1",
		Username:         "alice",
		DomainNormalized: "example.com",
		LocalID:          "agent-bot",
		AgentID:          soulLifecycleTestAgentIDHex,
		Wallet:           "0x00000000000000000000000000000000000000aa",
	}
	promotion := buildSoulAgentPromotionFromRegistration(reg, "alice", now)
	op := &models.SoulOperation{
		OperationID: "op-1",
		Status:      models.SoulOperationStatusExecuted,
	}
	promotion = updateSoulAgentPromotionForVerification(promotion, reg, op, "0x00000000000000000000000000000000000000bb", now.Add(time.Minute))
	promotion = updateSoulAgentPromotionForMintExecution(promotion, op, now.Add(2*time.Minute))
	promotion = updateSoulAgentPromotionForConversation(promotion, "conv-1", models.SoulMintConversationStatusInProgress, now.Add(3*time.Minute))

	declarationsJSON, err := json.Marshal(testMintConversationDecl())
	if err != nil {
		t.Fatalf("marshal declarations: %v", err)
	}
	promotion = updateSoulAgentPromotionForConversation(promotion, "conv-1", models.SoulMintConversationStatusCompleted, now.Add(4*time.Minute))
	promotion = updateSoulAgentPromotionReviewDigest(promotion, string(declarationsJSON))
	return promotion, now.Add(4 * time.Minute)
}
