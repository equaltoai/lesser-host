package main

import (
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/mintprompt"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestConversationActorDecidesNextActions(t *testing.T) {
	actor := newConversationActor()
	cases := []struct {
		name     string
		status   hostedgenesis.Status
		messages []llm.MintConversationMessage
		want     actorAction
		wantStep string
	}{
		{
			name:     "new user turn asks assistant",
			status:   hostedgenesis.StatusInProgress,
			messages: []llm.MintConversationMessage{{Role: "user", Content: "describe yourself"}},
			want:     actorActionAsk,
			wantStep: actorStepAssistantTurn,
		},
		{
			name:   "non-affirmation revises after final question",
			status: hostedgenesis.StatusInProgress,
			messages: []llm.MintConversationMessage{
				{Role: "user", Content: "describe yourself"},
				{Role: "assistant", Content: mintprompt.CanonicalFinalAffirmationQuestion},
				{Role: "user", Content: "please change the boundaries"},
			},
			want:     actorActionRevise,
			wantStep: actorStepAssistantTurn,
		},
		{
			name:   "affirmation selects extraction",
			status: hostedgenesis.StatusInProgress,
			messages: []llm.MintConversationMessage{
				{Role: "user", Content: "describe yourself"},
				{Role: "assistant", Content: mintprompt.CanonicalFinalAffirmationQuestion},
				{Role: "user", Content: "I affirm"},
			},
			want:     actorActionExtractFinalize,
			wantStep: actorStepDeclarationExtract,
		},
		{
			name:     "host authorized extraction",
			status:   hostedgenesis.StatusDeclarationExtractionPending,
			messages: []llm.MintConversationMessage{{Role: "user", Content: "describe"}, {Role: "assistant", Content: "ready"}},
			want:     actorActionExtractFinalize,
			wantStep: actorStepDeclarationExtract,
		},
		{
			name:     "ready states wait",
			status:   hostedgenesis.StatusAssistantTurnReady,
			messages: []llm.MintConversationMessage{{Role: "user", Content: "describe"}, {Role: "assistant", Content: "ready"}},
			want:     actorActionWait,
			wantStep: actorStepNoopWait,
		},
		{
			name:     "invalid state fails recoverably",
			status:   hostedgenesis.StatusFailed,
			messages: []llm.MintConversationMessage{{Role: "user", Content: "describe"}},
			want:     actorActionFailRecoverably,
			wantStep: actorStepInvalidStateFailure,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := turnInput{modelSet: "openai:gpt-test", messages: tc.messages, session: &models.HostedGenesisSession{Status: string(tc.status)}}
			decision := actor.decideBeforeProvider(in)
			if decision.action != tc.want || decision.step != tc.wantStep {
				t.Fatalf("unexpected decision: got action=%q step=%q want action=%q step=%q", decision.action, decision.step, tc.want, tc.wantStep)
			}
		})
	}
}

func TestConversationActorCheckpointStoresOnlySafeMetadata(t *testing.T) {
	actor := newConversationActor()
	in := turnInput{
		modelSet: "openai:gpt-test",
		messages: []llm.MintConversationMessage{{Role: "user", Content: "describe yourself"}},
		session: &models.HostedGenesisSession{
			ConversationID: "conv-1",
			Status:         string(hostedgenesis.StatusInProgress),
			Version:        7,
		},
	}
	decision := actor.decideBeforeProvider(in)
	checkpoint, err := actor.checkpoint(in, completionTurnView{turnID: "turn-1", requestID: "req-1"}, decision, "salt-1")
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := checkpoint.Validate(); err != nil {
		t.Fatalf("checkpoint should validate: %#v err=%v", checkpoint, err)
	}
	if checkpoint.Sequence != 8 || checkpoint.Action != string(actorActionAsk) || checkpoint.Step != actorStepAssistantTurn || checkpoint.LatestTurnID != "turn-1" {
		t.Fatalf("unexpected checkpoint metadata: %#v", checkpoint)
	}
	if !strings.HasPrefix(checkpoint.Ref, "checkpoint://hosted-genesis/vm-actor/") || !strings.HasPrefix(checkpoint.Hash, "sha256:") {
		t.Fatalf("expected compact VM actor ref/hash, got ref=%q hash=%q", checkpoint.Ref, checkpoint.Hash)
	}
	rendered := strings.ToLower(checkpoint.Ref + checkpoint.Hash + checkpoint.ProviderFamily + checkpoint.ModelID + checkpoint.RequestID)
	for _, forbidden := range []string{"bearer ", "authorization", "api_key", "secret", "raw transcript", "raw prompt", "describe yourself"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("checkpoint leaked forbidden material %q in %#v", forbidden, checkpoint)
		}
	}
}

func TestVMCheckpointRejectsUnsafeMetadata(t *testing.T) {
	_, err := hostedgenesis.NewVMCheckpointMetadata(hostedgenesis.VMCheckpointInput{
		ConversationID:     "conv-1",
		LatestTurnID:       "turn-1",
		Sequence:           1,
		Step:               actorStepAssistantTurn,
		Action:             string(actorActionAsk),
		StatusFrom:         hostedgenesis.StatusInProgress,
		StatusTo:           hostedgenesis.StatusAssistantTurnReady,
		Runtime:            hostedGenesisMicroVMActorRuntime,
		ProviderSessionID:  "bearer raw-provider-token",
		AdditionalHashSalt: "salt",
	})
	if err == nil {
		t.Fatal("expected unsafe checkpoint metadata to be rejected")
	}
}
