package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestMintConversationStatusProjection_LegacyCompletedMapsOnlyWithValidDeclarations(t *testing.T) {
	valid := string(mustMarshalJSON(t, testMintConversationDecl()))
	conv := &models.SoulAgentMintConversation{
		AgentID:              "0x" + strings.Repeat("22", 32),
		ConversationID:       "conv-legacy",
		Messages:             encodeMintConversationBlob(`[{"role":"user","content":"secret prompt"},{"role":"assistant","content":"secret reply"}]`),
		ProducedDeclarations: encodeMintConversationBlob(valid),
		Status:               models.SoulMintConversationStatusCompleted,
		RequestID:            "req-legacy",
		CreatedAt:            time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		CompletedAt:          time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC),
	}
	proj := mintConversationStatusProjectionFromModel(conv, true)
	if proj.ConversationID != "conv-legacy" || proj.Status != models.SoulMintConversationStatusDeclarationReady || proj.Reason != "" || !proj.ProducedDeclarationsPresent || !proj.ProducedDeclarationsValid || proj.RequestID != "req-legacy" || proj.MessageCount != 2 {
		t.Fatalf("unexpected valid legacy projection: %#v", proj)
	}
	resp := buildHostedGenesisConversationResponse(conv, hostedGenesisProjectionOptions{RegistrationID: "reg-1", RequestID: "req-legacy", CollapseCreated: true})
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "secret prompt") || strings.Contains(string(body), "secret reply") {
		t.Fatalf("status response leaked raw transcript: %s", string(body))
	}

	conv.ProducedDeclarations = encodeMintConversationBlob(`{"private":true}`)
	proj = mintConversationStatusProjectionFromModel(conv, true)
	if proj.Status != models.SoulMintConversationStatusFailed || proj.Reason != hostedGenesisFailureInvalidProducedDeclarations || !proj.ProducedDeclarationsPresent || proj.ProducedDeclarationsValid {
		t.Fatalf("unexpected invalid legacy projection: %#v", proj)
	}
}

func TestMintConversationStatusProjection_CollapsesCreatedForLesserPath(t *testing.T) {
	conv := &models.SoulAgentMintConversation{ConversationID: "conv-created", Status: models.SoulMintConversationStatusCreated, RequestID: "req-created"}
	if got := mintConversationStatusProjectionFromModel(conv, true); got.Status != models.SoulMintConversationStatusInProgress {
		t.Fatalf("expected created to collapse to in_progress for Lesser projection, got %#v", got)
	}
	if got := mintConversationStatusProjectionFromModel(conv, false); got.Status != models.SoulMintConversationStatusCreated {
		t.Fatalf("expected created to remain available without collapse, got %#v", got)
	}
}
