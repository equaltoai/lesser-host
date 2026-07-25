package main

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

const hostedGenesisMicroVMActorRuntime = "hosted-genesis-microvm-workload/v2"

type actorAction string

const (
	actorActionConstructSection  actorAction = "construct_section"
	actorActionRenderReview      actorAction = "render_review"
	actorActionFinalize          actorAction = "finalize"
	actorActionWait              actorAction = "wait"
	actorActionFailRecoverably   actorAction = "fail_recoverably"
	actorStepDeclarationSection  string      = "declaration_section"
	actorStepOwnerReview         string      = "owner_review"
	actorStepDeterministicFinal  string      = "deterministic_finalization"
	actorStepNoopWait            string      = "wait"
	actorStepInvalidStateFailure string      = "invalid_state_failure"
)

type actorDecision struct {
	action     actorAction
	step       string
	statusFrom hostedgenesis.Status
	statusTo   hostedgenesis.Status
	reason     string
}

type conversationActor struct{ runtime string }

func newConversationActor() conversationActor {
	return conversationActor{runtime: hostedGenesisMicroVMActorRuntime}
}

// decideBeforeProvider derives the next action only from Host's typed durable
// candidate. Transcript phrases are never authority for review, affirmation,
// or finalization.
func (a conversationActor) decideBeforeProvider(in turnInput) actorDecision {
	if in.session == nil || hostedgenesis.NormalizeStatus(in.session.Status) != hostedgenesis.StatusInProgress {
		status := hostedgenesis.Status("")
		if in.session != nil {
			status = hostedgenesis.NormalizeStatus(in.session.Status)
		}
		if status == hostedgenesis.StatusAssistantTurnReady || status == hostedgenesis.StatusDeclarationReady || status == hostedgenesis.StatusPublished {
			return actorDecision{action: actorActionWait, step: actorStepNoopWait, statusFrom: status, statusTo: status, reason: "host_wait_state"}
		}
		return actorDecision{action: actorActionFailRecoverably, step: actorStepInvalidStateFailure, statusFrom: status, statusTo: hostedgenesis.StatusFailed, reason: "invalid_completion_state"}
	}
	candidate := in.session.DeclarationCandidate
	if candidate == nil {
		return actorDecision{action: actorActionFailRecoverably, step: actorStepInvalidStateFailure, statusFrom: hostedgenesis.StatusInProgress, statusTo: hostedgenesis.StatusFailed, reason: "typed_candidate_required"}
	}
	switch candidate.Phase {
	case hostedgenesis.DeclarationCandidatePhaseSection:
		return actorDecision{action: actorActionConstructSection, step: actorStepDeclarationSection, statusFrom: hostedgenesis.StatusInProgress, statusTo: hostedgenesis.StatusAssistantTurnReady, reason: string(candidate.CurrentSection)}
	case hostedgenesis.DeclarationCandidatePhaseAffirmed:
		return actorDecision{action: actorActionFinalize, step: actorStepDeterministicFinal, statusFrom: hostedgenesis.StatusInProgress, statusTo: hostedgenesis.StatusDeclarationReady, reason: "structurally_affirmed"}
	case hostedgenesis.DeclarationCandidatePhaseReview:
		return actorDecision{action: actorActionRenderReview, step: actorStepOwnerReview, statusFrom: hostedgenesis.StatusInProgress, statusTo: hostedgenesis.StatusAssistantTurnReady, reason: "deterministic_owner_review"}
	default:
		return actorDecision{action: actorActionFailRecoverably, step: actorStepInvalidStateFailure, statusFrom: hostedgenesis.StatusInProgress, statusTo: hostedgenesis.StatusFailed, reason: "invalid_candidate_phase"}
	}
}

func (a conversationActor) checkpoint(in turnInput, turn completionTurnView, decision actorDecision, extraSalt string) (*hostedgenesis.VMCheckpointMetadata, error) {
	checkpoint, err := hostedgenesis.NewVMCheckpointMetadata(hostedgenesis.VMCheckpointInput{
		ConversationID: in.session.ConversationID, LatestTurnID: turn.turnID, RequestID: turn.requestID,
		Sequence: in.session.Version + 1, Step: decision.step, Action: string(decision.action),
		StatusFrom: decision.statusFrom, StatusTo: decision.statusTo,
		Runtime:        firstNonEmpty(strings.TrimSpace(a.runtime), hostedGenesisMicroVMActorRuntime),
		ProviderFamily: providerFamily(in.modelSet), ModelID: providerModelID(in.modelSet),
		AdditionalHashSalt: extraSalt,
	})
	if err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

type completionTurnView struct{ turnID, requestID string }

func providerFamily(modelSet string) string {
	modelSet = strings.ToLower(strings.TrimSpace(modelSet))
	switch {
	case strings.HasPrefix(modelSet, "openai:"):
		return "openai"
	case strings.HasPrefix(modelSet, "anthropic:"):
		return "anthropic"
	default:
		return "unknown"
	}
}

func providerModelID(modelSet string) string {
	modelSet = strings.TrimSpace(modelSet)
	if _, model, ok := strings.Cut(modelSet, ":"); ok {
		return strings.TrimSpace(model)
	}
	return modelSet
}
