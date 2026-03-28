package models

import (
	"testing"
	"time"
)

func TestSoulAgentPromotionBeforeCreateSetsDefaults(t *testing.T) {
	promotion := &SoulAgentPromotion{
		AgentID: "0XABCDEF",
		Domain:  "Example.COM",
		LocalID: "Agent Bot",
		Wallet:  "0X00000000000000000000000000000000000000AA",
	}

	if err := promotion.BeforeCreate(); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}

	if promotion.Stage != SoulAgentPromotionStageRequested ||
		promotion.RequestStatus != SoulAgentPromotionRequestStatusRequested ||
		promotion.ReviewStatus != SoulAgentPromotionReviewStatusNotStarted ||
		promotion.ApprovalStatus != SoulAgentPromotionApprovalStatusPending ||
		promotion.ReadinessStatus != SoulAgentPromotionReadinessAwaitingVerification {
		t.Fatalf("unexpected defaults: %#v", promotion)
	}
	if promotion.RequestedAt.IsZero() || promotion.CreatedAt.IsZero() || promotion.UpdatedAt.IsZero() {
		t.Fatalf("expected timestamps to be set: %#v", promotion)
	}
	if promotion.PK != "SOUL#AGENT#0xabcdef" || promotion.SK != "PROMOTION" {
		t.Fatalf("unexpected keys: %q %q", promotion.PK, promotion.SK)
	}
	if promotion.GSI1PK != "SOUL_PROMOTION_STAGE#requested" {
		t.Fatalf("unexpected gsi1 pk: %q", promotion.GSI1PK)
	}
}

func TestSoulAgentPromotionUpdateKeysNormalizesIdentityFields(t *testing.T) {
	promotion := promotionForNormalization()
	if err := promotion.UpdateKeys(); err != nil {
		t.Fatalf("UpdateKeys: %v", err)
	}

	if promotion.AgentID != "0xabcdef" ||
		promotion.Domain != "example.com" ||
		promotion.LocalID != "agent bot" ||
		promotion.Wallet != "0x00000000000000000000000000000000000000aa" {
		t.Fatalf("unexpected identity normalization: %#v", promotion)
	}
}

func TestSoulAgentPromotionUpdateKeysNormalizesStatusFields(t *testing.T) {
	promotion := promotionForNormalization()
	if err := promotion.UpdateKeys(); err != nil {
		t.Fatalf("UpdateKeys: %v", err)
	}

	if promotion.Stage != "ready_to_finalize" ||
		promotion.RequestStatus != "minted" ||
		promotion.ReviewStatus != "draft_ready" ||
		promotion.ApprovalStatus != "approved" ||
		promotion.ReadinessStatus != "ready_for_finalize" {
		t.Fatalf("unexpected status normalization: %#v", promotion)
	}
	if promotion.MintOperationStatus != "executed" ||
		promotion.PrincipalAddress != "0x00000000000000000000000000000000000000bb" ||
		promotion.LatestConversationID != "conv-1" ||
		promotion.LatestConversationStatus != "completed" ||
		promotion.LatestReviewSHA256 != "abc123" {
		t.Fatalf("unexpected detail normalization: %#v", promotion)
	}
	if promotion.GSI1PK != "SOUL_PROMOTION_STAGE#ready_to_finalize" {
		t.Fatalf("unexpected gsi1 pk after update: %q", promotion.GSI1PK)
	}
}

func TestSoulAgentPromotionBeforeUpdateRefreshesUpdatedAt(t *testing.T) {
	promotion := promotionForNormalization()
	promotion.UpdatedAt = time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)

	if err := promotion.BeforeUpdate(); err != nil {
		t.Fatalf("BeforeUpdate: %v", err)
	}
	if !promotion.UpdatedAt.After(time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected updated_at to advance, got %s", promotion.UpdatedAt)
	}
}

func TestSoulAgentPromotionUpdateGSI1ClearsWhenStageBlank(t *testing.T) {
	promotion := promotionForNormalization()
	if err := promotion.UpdateKeys(); err != nil {
		t.Fatalf("UpdateKeys: %v", err)
	}

	promotion.Stage = ""
	promotion.updateGSI1()
	if promotion.GSI1PK != "" || promotion.GSI1SK != "" {
		t.Fatalf("expected empty gsi1 when stage is blank, got %q %q", promotion.GSI1PK, promotion.GSI1SK)
	}
}

func promotionForNormalization() *SoulAgentPromotion {
	return &SoulAgentPromotion{
		AgentID:                  "0XABCDEF",
		RegistrationID:           " reg-1 ",
		RequestedBy:              " alice ",
		Domain:                   "Example.COM",
		LocalID:                  "Agent Bot",
		Wallet:                   "0X00000000000000000000000000000000000000AA",
		Stage:                    " Ready_To_Finalize ",
		RequestStatus:            " Minted ",
		ReviewStatus:             " Draft_Ready ",
		ApprovalStatus:           " Approved ",
		ReadinessStatus:          " Ready_For_Finalize ",
		MintOperationID:          " op-1 ",
		MintOperationStatus:      " Executed ",
		PrincipalAddress:         "0X00000000000000000000000000000000000000BB ",
		LatestConversationID:     " conv-1 ",
		LatestConversationStatus: " Completed ",
		LatestReviewSHA256:       " ABC123 ",
		RequestedAt:              time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC),
	}
}
