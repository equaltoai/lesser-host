package main

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

const hostedGenesisMicroVMActorRuntime = "hosted-genesis-microvm-workload/v1"

type actorAction string

const (
	actorActionAsk               actorAction = "ask"
	actorActionWait              actorAction = "wait"
	actorActionRevise            actorAction = "revise"
	actorActionExtractFinalize   actorAction = "extract_finalize"
	actorActionFailRecoverably   actorAction = "fail_recoverably"
	actorStepAssistantTurn       string      = "assistant_turn"
	actorStepDeclarationExtract  string      = "declaration_extraction"
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

type conversationActor struct {
	runtime string
}

func newConversationActor() conversationActor {
	return conversationActor{runtime: hostedGenesisMicroVMActorRuntime}
}

// decideBeforeProvider is the in-VM actor's durable next-action decision over
// the delivered Host turn. Host has already accepted the turn, debited the user
// turn, and persisted status/version/idempotency guards. The actor decides what
// work the VM should perform next; Host remains the guarded writer.
func (a conversationActor) decideBeforeProvider(in turnInput) actorDecision {
	status := hostedgenesis.NormalizeStatus(in.session.Status)
	switch status {
	case hostedgenesis.StatusInProgress:
		if actorTranscriptRequestsFinalAffirmation(in.messages) {
			if actorLatestUserAffirms(in.messages) {
				return actorDecision{action: actorActionExtractFinalize, step: actorStepDeclarationExtract, statusFrom: hostedgenesis.StatusInProgress, statusTo: hostedgenesis.StatusDeclarationReady, reason: "final_affirmation"}
			}
			return actorDecision{action: actorActionRevise, step: actorStepAssistantTurn, statusFrom: hostedgenesis.StatusInProgress, statusTo: hostedgenesis.StatusAssistantTurnReady, reason: "revise_after_non_affirmation"}
		}
		return actorDecision{action: actorActionAsk, step: actorStepAssistantTurn, statusFrom: hostedgenesis.StatusInProgress, statusTo: hostedgenesis.StatusAssistantTurnReady, reason: "assistant_turn"}
	case hostedgenesis.StatusDeclarationExtractionPending:
		return actorDecision{action: actorActionExtractFinalize, step: actorStepDeclarationExtract, statusFrom: hostedgenesis.StatusDeclarationExtractionPending, statusTo: hostedgenesis.StatusDeclarationReady, reason: "host_authorized_extraction"}
	case hostedgenesis.StatusAssistantTurnReady, hostedgenesis.StatusDeclarationReady:
		return actorDecision{action: actorActionWait, step: actorStepNoopWait, statusFrom: status, statusTo: status, reason: "host_wait_state"}
	default:
		return actorDecision{action: actorActionFailRecoverably, step: actorStepInvalidStateFailure, statusFrom: status, statusTo: hostedgenesis.StatusFailed, reason: "invalid_completion_state"}
	}
}

func (a conversationActor) checkpoint(in turnInput, turn completionTurnView, decision actorDecision, extraSalt string) (*hostedgenesis.VMCheckpointMetadata, error) {
	checkpoint, err := hostedgenesis.NewVMCheckpointMetadata(hostedgenesis.VMCheckpointInput{
		ConversationID:     in.session.ConversationID,
		LatestTurnID:       turn.turnID,
		RequestID:          turn.requestID,
		Sequence:           in.session.Version + 1,
		Step:               decision.step,
		Action:             string(decision.action),
		StatusFrom:         decision.statusFrom,
		StatusTo:           decision.statusTo,
		Runtime:            firstNonEmpty(strings.TrimSpace(a.runtime), hostedGenesisMicroVMActorRuntime),
		ProviderFamily:     providerFamily(in.modelSet),
		ModelID:            providerModelID(in.modelSet),
		AdditionalHashSalt: extraSalt,
		ProviderSessionID:  "",
		TraceID:            "",
	})
	if err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

type completionTurnView struct {
	turnID    string
	requestID string
}

func actorTranscriptRequestsFinalAffirmation(messages []llm.MintConversationMessage) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		content := strings.ToLower(strings.TrimSpace(msg.Content))
		if content == "" {
			return false
		}
		return strings.Contains(content, "do you affirm") &&
			strings.Contains(content, "foundation of your minted soul") &&
			strings.Contains(content, "inscribed")
	}
	return false
}

func actorLatestUserAffirms(messages []llm.MintConversationMessage) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		return actorMessageAffirmsFinalDeclaration(msg.Content)
	}
	return false
}

func actorMessageAffirmsFinalDeclaration(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if normalized == "" {
		return false
	}
	normalized = strings.Trim(normalized, " \t\r\n.!?")
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "not affirm") ||
		strings.Contains(normalized, "do not affirm") ||
		strings.Contains(normalized, "don't affirm") ||
		strings.Contains(normalized, "change ") ||
		strings.Contains(normalized, "correct ") ||
		strings.Contains(normalized, "qualify ") ||
		strings.Contains(normalized, "strike ") {
		return false
	}
	switch normalized {
	case "yes", "yes i affirm", "i affirm", "affirmed", "i do", "confirmed", "i confirm", "approved", "i approve", "proceed":
		return true
	}
	return strings.Contains(normalized, "i affirm") ||
		strings.Contains(normalized, "i confirm") ||
		strings.Contains(normalized, "i approve")
}

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
