package models

import (
	"testing"
	"time"
)

func TestSoulAgentPromotionLifecycleEventBeforeCreateSetsKeys(t *testing.T) {
	t.Parallel()

	event := &SoulAgentPromotionLifecycleEvent{
		AgentID:     " 0xABC ",
		EventType:   SoulAgentPromotionEventTypeFinalizeReady,
		RequestedBy: " alice ",
		OccurredAt:  time.Date(2026, 3, 28, 19, 0, 0, 0, time.UTC),
	}
	if err := event.BeforeCreate(); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
	if event.PK != "SOUL#AGENT#0xabc" {
		t.Fatalf("unexpected pk: %q", event.PK)
	}
	if event.GSI1PK != "SOUL_PROMOTION_EVENT_REQUESTER#alice" {
		t.Fatalf("unexpected gsi1 pk: %q", event.GSI1PK)
	}
	if event.EventID == "" || event.SK == "" || event.GSI1SK == "" {
		t.Fatalf("expected generated event keys, got %#v", event)
	}
}

func TestDefaultSoulAgentPromotionLifecycleEventIDPrefersConversation(t *testing.T) {
	t.Parallel()

	occurredAt := time.Date(2026, 3, 28, 19, 5, 0, 0, time.UTC)
	got := DefaultSoulAgentPromotionLifecycleEventID(SoulAgentPromotionEventTypeReviewStarted, occurredAt, "agent", "conv-7", "op-1")
	want := "review_started#20260328T190500.000000000Z#conv-7"
	if got != want {
		t.Fatalf("unexpected event id: %q", got)
	}
}
