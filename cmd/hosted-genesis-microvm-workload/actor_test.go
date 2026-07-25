package main

import (
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestConversationActorUsesOnlyTypedCandidatePhase(t *testing.T) {
	candidate, err := hostedgenesis.NewDeclarationCandidate(hostedgenesis.DeclarationCandidateBinding{
		InstanceSlug: "acme", RegistrationID: "reg", AgentID: "0xabc", ConversationID: "conv", SourceTurnID: "turn-1", Model: "openai:gpt-5",
	}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	input := turnInput{modelSet: "openai:gpt-5", session: &models.HostedGenesisSession{Status: string(hostedgenesis.StatusInProgress), DeclarationCandidate: candidate}}
	decision := newConversationActor().decideBeforeProvider(input)
	if decision.action != actorActionConstructSection || decision.step != actorStepDeclarationSection {
		t.Fatalf("expected typed section action, got %#v", decision)
	}

	review := completeRunnerCandidate(t, candidate)
	input.session.DeclarationCandidate = review
	decision = newConversationActor().decideBeforeProvider(input)
	if decision.action != actorActionRenderReview || decision.step != actorStepOwnerReview {
		t.Fatalf("review recovery must render the exact stored review without a provider, got %#v", decision)
	}

	affirmed, err := hostedgenesis.ApplyDeclarationCandidateAction(review, hostedgenesis.DeclarationCandidateAction{
		Action: "affirm", CandidateRevision: review.Revision, CandidateHash: review.CandidateHash, ReviewHash: review.Review.ReviewHash,
	}, review.SourceTurnID, time.Unix(20, 0))
	if err != nil {
		t.Fatal(err)
	}
	input.session.DeclarationCandidate = affirmed
	decision = newConversationActor().decideBeforeProvider(input)
	if decision.action != actorActionFinalize || decision.step != actorStepDeterministicFinal {
		t.Fatalf("affirmed candidate must finalize provider-free, got %#v", decision)
	}
}

func TestConversationActorHardCutoverRejectsMissingCandidate(t *testing.T) {
	decision := newConversationActor().decideBeforeProvider(turnInput{session: &models.HostedGenesisSession{Status: string(hostedgenesis.StatusInProgress)}})
	if decision.action != actorActionFailRecoverably || decision.reason != "typed_candidate_required" {
		t.Fatalf("legacy lane did not fail closed: %#v", decision)
	}
}

func TestConversationActorCheckpointBindsTypedAction(t *testing.T) {
	in := turnInput{modelSet: "anthropic:claude", session: &models.HostedGenesisSession{ConversationID: "conv", Version: 7}}
	decision := actorDecision{action: actorActionConstructSection, step: actorStepDeclarationSection, statusFrom: hostedgenesis.StatusInProgress, statusTo: hostedgenesis.StatusAssistantTurnReady}
	cp, err := newConversationActor().checkpoint(in, completionTurnView{turnID: "turn", requestID: "req"}, decision, "sha256:abc")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Sequence != 8 || cp.Action != string(actorActionConstructSection) || cp.Runtime != hostedGenesisMicroVMActorRuntime || cp.ProviderFamily != "anthropic" {
		t.Fatalf("unexpected checkpoint: %#v", cp)
	}
}
