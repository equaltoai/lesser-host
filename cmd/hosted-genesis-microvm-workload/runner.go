package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/ai/modelselection"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/mintprompt"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// turnStore is the TableTheory-backed authority available inside the AppTheory
// MicroVM workload. Candidate checkpoints and finalization are guarded
// transactions; no raw AWS SDK client or custom persistence layer is exposed.
type turnStore interface {
	GetHostedGenesisSession(context.Context, string, string) (*models.HostedGenesisSession, error)
	GetSoulAgentMintConversation(context.Context, string, string) (*models.SoulAgentMintConversation, error)
	GetSoulAgentRegistration(context.Context, string) (*models.SoulAgentRegistration, error)
	CheckpointHostedGenesisCandidate(context.Context, *models.HostedGenesisSession, int64, hostedgenesis.Status, string, int64, string) error
	RecordHostedGenesisAssistantTurnAndConversation(context.Context, *models.HostedGenesisSession, int64, hostedgenesis.Status, string, int64, string, *models.SoulAgentMintConversation) error
	FinalizeHostedGenesisCandidateAndConversation(context.Context, *models.HostedGenesisSession, int64, hostedgenesis.Status, string, int64, string, *models.SoulAgentMintConversation) error
}

type turnStoreFactory func(context.Context) (turnStore, *completion.CompletionWriter, error)
type declarationPhaseRunner func(context.Context, string, llm.MintConversationPhaseInput, llm.MintConversationPhaseToolHandler, llm.ProviderTelemetrySink) (llm.MintConversationPhaseOutput, error)

type turnRunner struct {
	store                     turnStore
	writer                    *completion.CompletionWriter
	storeFactory              turnStoreFactory
	phaseRunner               declarationPhaseRunner
	nowFunc                   func() time.Time
	providerCallTimeout       time.Duration
	workloadExecutionTimeout  time.Duration
	providerHeartbeatInterval time.Duration
}

type turnInput struct {
	modelSet     string
	systemPrompt string
	messages     []llm.MintConversationMessage
	contract     hostedgenesis.DeclarationContract
	registration *models.SoulAgentRegistration
	conv         *models.SoulAgentMintConversation
	session      *models.HostedGenesisSession
}

func (r *turnRunner) loadTurnInput(ctx context.Context, turn completion.CompletionTurn) (turnInput, error) {
	session, err := r.store.GetHostedGenesisSession(ctx, turn.InstanceSlug, turn.ConversationID)
	if err != nil || session == nil {
		return turnInput{}, fmt.Errorf("load hosted genesis session: %w", err)
	}
	conv, err := r.store.GetSoulAgentMintConversation(ctx, session.AgentID, session.ConversationID)
	if err != nil || conv == nil {
		return turnInput{}, fmt.Errorf("load mint conversation transcript: %w", err)
	}
	reg, err := r.store.GetSoulAgentRegistration(ctx, session.RegistrationID)
	if err != nil || reg == nil {
		return turnInput{}, fmt.Errorf("load agent registration: %w", err)
	}
	messages, err := decodeMintConversationMessages(conv.Messages)
	if err != nil {
		return turnInput{}, fmt.Errorf("decode mint conversation messages: %w", err)
	}
	modelSet := strings.TrimSpace(conv.Model)
	if modelSet == "" {
		return turnInput{}, errors.New("mint conversation has no model set")
	}
	contract, err := hostedgenesis.RequireFiveBodyDeclarationContractFromEnv()
	if err != nil {
		return turnInput{}, fmt.Errorf("resolve hosted genesis declaration contract: %w", err)
	}
	if !contract.IsFiveBody() {
		return turnInput{}, hostedgenesis.ErrDeclarationContractUnconfigured
	}
	systemPrompt, err := mintprompt.MintConversationSystemPromptForContract(reg, contract)
	if err != nil {
		return turnInput{}, fmt.Errorf("build hosted genesis system prompt: %w", err)
	}
	if err := validateTurnInputCandidate(session, modelSet, contract); err != nil {
		return turnInput{}, err
	}
	return turnInput{modelSet: modelSet, systemPrompt: systemPrompt, messages: messages, contract: contract, registration: reg, conv: conv, session: session}, nil
}

func validateTurnInputCandidate(session *models.HostedGenesisSession, modelSet string, contract hostedgenesis.DeclarationContract) error {
	if session.DeclarationCandidate == nil {
		return nil
	}
	candidate := session.DeclarationCandidate
	if err := candidate.Validate(); err != nil {
		return fmt.Errorf("validate typed declaration candidate: %w", err)
	}
	if err := hostedgenesis.ValidateDeclarationContractVersions(candidate.SchemaVersion, candidate.GuidanceVersion, contract); err != nil {
		return fmt.Errorf("validate typed declaration candidate contract: %w", err)
	}
	if !declarationCandidateMatchesTurnSession(candidate, session, modelSet) {
		return errors.New("typed declaration candidate binding mismatch")
	}
	return nil
}

func declarationCandidateMatchesTurnSession(candidate *hostedgenesis.DeclarationCandidate, session *models.HostedGenesisSession, modelSet string) bool {
	return candidate.InstanceSlug == strings.ToLower(strings.TrimSpace(session.InstanceSlug)) &&
		candidate.RegistrationID == strings.TrimSpace(session.RegistrationID) && strings.EqualFold(candidate.AgentID, session.AgentID) &&
		candidate.ConversationID == session.ConversationID &&
		strings.EqualFold(modelselection.CanonicalModelSet(candidate.Model), modelselection.CanonicalModelSet(modelSet))
}

func (r *turnRunner) runTurnAndPersist(ctx context.Context, turn completion.CompletionTurn) error {
	telemetry := newTurnLifecycleTelemetry(turn)
	telemetry.emit("accepted", "turn_accepted", "", "")
	telemetry.emit("store_preflight", "store_preflight_started", "", "")
	runner, in, err := r.prepareTurn(ctx, turn)
	if err != nil {
		telemetry.emit("store_preflight", "store_preflight_failed", "", "store_error")
		if runner == nil {
			return fmt.Errorf("initialize hosted genesis microvm turn store: %w", err)
		}
		if errors.Is(err, hostedgenesis.ErrDeclarationContractUnconfigured) {
			return runner.recordFailure(ctx, turn, hostedgenesis.FailureCodeOperatorActionRequired, err.Error(), telemetry)
		}
		return runner.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "typed candidate preflight failed", telemetry)
	}
	telemetry.bind(in)
	telemetry.emit("store_preflight", "store_preflight_completed", "", "")
	return runner.runPreparedTurnAndPersist(ctx, turn, in, telemetry)
}

func (r *turnRunner) prepareTurn(ctx context.Context, turn completion.CompletionTurn) (*turnRunner, turnInput, error) {
	runner, err := r.withTurnStore(ctx)
	if err != nil {
		return nil, turnInput{}, err
	}
	in, err := runner.loadTurnInput(ctx, turn)
	if err != nil {
		return runner, turnInput{}, err
	}
	return runner, in, nil
}

func (r *turnRunner) runPreparedTurnAndPersist(ctx context.Context, turn completion.CompletionTurn, in turnInput, telemetry *turnLifecycleTelemetry) error {
	telemetry.bind(in)
	actor := newConversationActor()
	decision := actor.decideBeforeProvider(in)
	telemetry.emit("actor_decision", "actor_decision_completed", string(decision.action), "")
	switch decision.action {
	case actorActionConstructSection:
		return r.runDeclarationPhaseAndPersist(ctx, turn, &in, actor, decision, telemetry)
	case actorActionRenderReview:
		return r.runDeterministicReviewAndPersist(ctx, turn, &in, actor, decision, telemetry)
	case actorActionFinalize:
		return r.runDeterministicFinalizationAndPersist(ctx, turn, &in, actor, decision, telemetry)
	case actorActionWait:
		return nil
	case actorActionFailRecoverably:
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, decision.reason, telemetry)
	default:
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "unknown hosted genesis actor decision", telemetry)
	}
}

func (r *turnRunner) runDeterministicReviewAndPersist(ctx context.Context, turn completion.CompletionTurn, in *turnInput, actor conversationActor, decision actorDecision, telemetry *turnLifecycleTelemetry) error {
	if in == nil || in.session == nil || in.session.DeclarationCandidate == nil || in.conv == nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "typed review candidate required", telemetry)
	}
	candidate := in.session.DeclarationCandidate
	if candidate.Phase != hostedgenesis.DeclarationCandidatePhaseReview || candidate.Review == nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "deterministic owner review required", telemetry)
	}
	postTurnMessages := append(append([]llm.MintConversationMessage(nil), in.messages...), llm.MintConversationMessage{Role: "assistant", Content: candidate.Review.ReviewText})
	checkpoint, err := actor.checkpoint(*in, completionTurnView{turnID: turn.TurnID, requestID: turn.RequestID}, decision, candidate.CandidateHash)
	if err != nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "build owner review checkpoint", telemetry)
	}
	telemetry.emit("persist", "persist_started", string(decision.action), "")
	if err := r.persistAssistantTurnAtomically(ctx, in, turn, postTurnMessages, models.AIUsage{}, checkpoint); err != nil {
		telemetry.emit("persist", "persist_failed", string(decision.action), "store_error")
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "persist owner review", telemetry)
	}
	telemetry.emit("persist", "persist_completed", string(decision.action), "")
	return nil
}

func (r *turnRunner) withTurnStore(ctx context.Context) (*turnRunner, error) {
	if r == nil {
		return nil, errors.New("turn runner is not configured")
	}
	if r.store != nil && r.writer != nil {
		return r, nil
	}
	if r.storeFactory == nil {
		return nil, errors.New("turn store factory is not configured")
	}
	st, writer, err := r.storeFactory(ctx)
	if err != nil {
		return nil, err
	}
	if st == nil || writer == nil {
		return nil, errors.New("turn store factory returned incomplete store")
	}
	copy := *r
	copy.store, copy.writer = st, writer
	return &copy, nil
}

func (r *turnRunner) runDeclarationPhaseAndPersist(ctx context.Context, turn completion.CompletionTurn, in *turnInput, actor conversationActor, decision actorDecision, telemetry *turnLifecycleTelemetry) error {
	if in == nil || in.session == nil || in.session.DeclarationCandidate == nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "typed candidate required", telemetry)
	}
	apiKey, err := providerAPIKey(ctx, in.modelSet)
	if err != nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeAssistantTurnFailed, providerFailureMessage("declaration_phase", err), telemetry, hostedgenesis.FailureClassProviderAPIFailure)
	}
	providerCtx, cancel := context.WithTimeout(ctx, r.providerTimeout())
	defer cancel()
	providerTelemetry := newProviderCallTelemetry(turn, *in, "declaration_phase")
	stopHeartbeat := providerTelemetry.startHeartbeat(providerCtx, r.providerHeartbeat())
	defer stopHeartbeat()
	phaseRunner := r.phaseRunner
	if phaseRunner == nil {
		phaseRunner = llm.RunMintConversationPhase
	}
	candidate := in.session.DeclarationCandidate
	providerAttemptOrdinalBase := declarationProviderAttemptOrdinalBase(
		candidate.ProviderAttempts,
		turn.TurnID,
		candidate.CurrentSection,
		candidate.Revision,
		candidate.CandidateHash,
	)
	evidence := &providerEvidenceTracker{}
	output, err := phaseRunner(providerCtx, apiKey, llm.MintConversationPhaseInput{
		ModelSet: in.modelSet, SystemPrompt: in.systemPrompt, Messages: append([]llm.MintConversationMessage(nil), in.messages...),
		Section: candidate.CurrentSection, CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash, SourceTurnID: turn.TurnID,
	}, r.declarationPhaseToolHandler(in, turn, evidence), r.declarationProviderObserver(providerCtx, in, turn, candidate, providerAttemptOrdinalBase, providerTelemetry, evidence))
	if evidence.errValue() != nil {
		telemetry.emit("declaration_phase", "provider_evidence_persist_failed", string(decision.action), string(hostedgenesis.FailureClassProviderEvidenceStore))
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeAssistantTurnFailed, "persist provider attempt evidence", telemetry, hostedgenesis.FailureClassProviderEvidenceStore)
	}
	if err != nil || strings.TrimSpace(output.AssistantContent) == "" {
		failureClass := hostedgenesis.NormalizeFailureClass(llm.ProviderFailureClass(err))
		telemetry.emit("declaration_phase", "provider_call_failed", string(decision.action), string(failureClass))
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeAssistantTurnFailed, providerFailureMessage("declaration_phase", err), telemetry, failureClass)
	}
	return r.persistDeclarationPhaseOutput(ctx, turn, in, actor, decision, telemetry, output)
}

type providerEvidenceTracker struct {
	mu  sync.Mutex
	err error
}

func (tracker *providerEvidenceTracker) record(err error) {
	if err == nil {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.err == nil {
		tracker.err = err
	}
}

func (tracker *providerEvidenceTracker) errValue() error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.err
}

func (r *turnRunner) declarationProviderObserver(ctx context.Context, in *turnInput, turn completion.CompletionTurn, candidate *hostedgenesis.DeclarationCandidate, ordinalBase int64, telemetry *providerCallTelemetry, evidence *providerEvidenceTracker) llm.ProviderTelemetrySink {
	return func(event llm.ProviderTelemetryEvent) {
		telemetry.observe(event)
		if !providerAttemptEvidenceEvent(event.EventType) {
			return
		}
		if event.SDKAttemptOrdinal > 0 {
			event.SDKAttemptOrdinal += ordinalBase
		}
		evidence.record(r.checkpointProviderAttemptEvidence(ctx, in, turn, candidate.CurrentSection, candidate.Revision, candidate.CandidateHash, event))
	}
}

func (r *turnRunner) declarationPhaseToolHandler(in *turnInput, turn completion.CompletionTurn, evidence *providerEvidenceTracker) llm.MintConversationPhaseToolHandler {
	return func(ctx context.Context, call llm.MintConversationPhaseToolCall) (hostedgenesis.DeclarationToolResult, error) {
		if err := evidence.errValue(); err != nil {
			return hostedgenesis.DeclarationToolResult{}, err
		}
		current := in.session
		if current == nil || current.DeclarationCandidate == nil {
			return hostedgenesis.DeclarationToolResult{}, errors.New("typed declaration candidate disappeared")
		}
		next, result, err := hostedgenesis.ApplyDeclarationTool(current.DeclarationCandidate, hostedgenesis.DeclarationToolRequest{
			ToolName: call.Name, ToolCallID: call.CallID, ExpectedRevision: current.DeclarationCandidate.Revision,
			ExpectedHash: current.DeclarationCandidate.CandidateHash, SourceTurnID: turn.TurnID, Payload: call.Arguments,
		}, r.now())
		if err != nil || !result.Accepted || result.Idempotent {
			return result, err
		}
		if err := r.checkpointDeclarationPhaseTool(ctx, in, turn, current, next); err != nil {
			return hostedgenesis.DeclarationToolResult{}, err
		}
		return result, nil
	}
}

func (r *turnRunner) checkpointDeclarationPhaseTool(ctx context.Context, in *turnInput, turn completion.CompletionTurn, current *models.HostedGenesisSession, next *hostedgenesis.DeclarationCandidate) error {
	progressed := cloneSessionForRunner(current)
	progressed.DeclarationCandidate = next
	progressed.RequestID = strings.TrimSpace(turn.RequestID)
	progressed.UpdatedAt = r.now()
	if err := r.store.CheckpointHostedGenesisCandidate(ctx, progressed, current.Version, hostedgenesis.StatusInProgress, turn.TurnID, current.DeclarationCandidate.Revision, current.DeclarationCandidate.CandidateHash); err != nil {
		return fmt.Errorf("checkpoint typed declaration candidate: %w", err)
	}
	progressed.Version = current.Version + 1
	in.session = progressed
	return nil
}

func (r *turnRunner) persistDeclarationPhaseOutput(ctx context.Context, turn completion.CompletionTurn, in *turnInput, actor conversationActor, decision actorDecision, telemetry *turnLifecycleTelemetry, output llm.MintConversationPhaseOutput) error {
	assistantContent := strings.TrimSpace(output.AssistantContent)
	candidate := in.session.DeclarationCandidate
	if candidate != nil && candidate.Phase == hostedgenesis.DeclarationCandidatePhaseReview && candidate.Review != nil {
		assistantContent = candidate.Review.ReviewText
	}
	postTurnMessages := append(append([]llm.MintConversationMessage(nil), in.messages...), llm.MintConversationMessage{Role: "assistant", Content: assistantContent})
	telemetry.emit("persist", "persist_started", string(decision.action), "")
	checkpoint, err := actor.checkpoint(*in, completionTurnView{turnID: turn.TurnID, requestID: turn.RequestID}, decision, candidate.CandidateHash)
	if err != nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "build phase checkpoint", telemetry)
	}
	if err := r.persistAssistantTurnAtomically(ctx, in, turn, postTurnMessages, output.Usage, checkpoint); err != nil {
		telemetry.emit("persist", "persist_failed", string(decision.action), "store_error")
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeAssistantTurnFailed, "persist assistant turn", telemetry, hostedgenesis.FailureClassAssistantTurnStore)
	}
	telemetry.emit("persist", "persist_completed", string(decision.action), "")
	return nil
}

func providerAttemptEvidenceEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "sdk_http_attempt", "tool_validation_completed", "provider_call_completed", "provider_call_failed":
		return true
	default:
		return false
	}
}

func (r *turnRunner) checkpointProviderAttemptEvidence(ctx context.Context, in *turnInput, turn completion.CompletionTurn, section hostedgenesis.DeclarationSection, revision int64, candidateHash string, event llm.ProviderTelemetryEvent) error {
	if r == nil || r.store == nil || in == nil || in.session == nil || in.session.DeclarationCandidate == nil {
		return errors.New("typed candidate provider evidence store is unavailable")
	}
	if event.SDKAttemptOrdinal == 0 {
		// Tests and explicitly injected clients may not use Host's production
		// attempt-observing transport. In production, every non-SDK observation
		// follows the sdk_http_attempt record emitted by that transport.
		if !hasProviderAttemptBinding(in.session.DeclarationCandidate.ProviderAttempts, turn.TurnID, section, revision, candidateHash) {
			return nil
		}
	}
	codes := make([]hostedgenesis.DeclarationValidationCode, 0, len(event.ValidationCodes))
	for _, code := range event.ValidationCodes {
		codes = append(codes, hostedgenesis.DeclarationValidationCode(code))
	}
	nextCandidate, err := hostedgenesis.ApplyDeclarationProviderAttempt(in.session.DeclarationCandidate, hostedgenesis.DeclarationProviderAttemptUpdate{
		Provider: event.Provider, Model: event.Model, Phase: event.Phase, Section: section,
		SourceTurnID: turn.TurnID, CandidateRevision: revision, CandidateHash: candidateHash,
		SDKAttemptOrdinal: event.SDKAttemptOrdinal, SDKRetryBudget: event.SDKRetryBudget,
		HTTPStatus: event.HTTPStatus, ProviderRequestID: event.ProviderRequestID,
		ToolName: event.ToolName, ToolCallHash: event.ToolCallHash, ValidationCodes: codes,
		ValidationPaths: append([]string(nil), event.ValidationPaths...), Accepted: event.Accepted,
		OutputBytes: event.OutputBytes, OutputSHA256: event.OutputSHA256,
		InputTokens: event.InputTokens, OutputTokens: event.OutputTokens, TotalTokens: event.TotalTokens,
		ToolCalls: event.ToolCalls, StopReason: event.StopReason,
		FailureClass: hostedgenesis.FailureClass(event.FailureClass), DurationMS: maxInt64Runner(event.DurationMS, event.ElapsedMS),
	}, r.now())
	if err != nil {
		return err
	}
	current := in.session
	progressed := cloneSessionForRunner(current)
	progressed.DeclarationCandidate = nextCandidate
	progressed.RequestID = strings.TrimSpace(turn.RequestID)
	progressed.UpdatedAt = r.now()
	if err := r.store.CheckpointHostedGenesisCandidate(ctx, progressed, current.Version, hostedgenesis.StatusInProgress, turn.TurnID, current.DeclarationCandidate.Revision, current.DeclarationCandidate.CandidateHash); err != nil {
		return fmt.Errorf("checkpoint provider attempt evidence: %w", err)
	}
	progressed.Version = current.Version + 1
	in.session = progressed
	return nil
}

func hasProviderAttemptBinding(attempts []hostedgenesis.DeclarationProviderAttempt, turnID string, section hostedgenesis.DeclarationSection, revision int64, candidateHash string) bool {
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := attempts[index]
		if attempt.SourceTurnID == strings.TrimSpace(turnID) && attempt.Section == section &&
			attempt.CandidateRevision == revision && attempt.CandidateHash == strings.TrimSpace(candidateHash) {
			return true
		}
	}
	return false
}

func declarationProviderAttemptOrdinalBase(attempts []hostedgenesis.DeclarationProviderAttempt, turnID string, section hostedgenesis.DeclarationSection, revision int64, candidateHash string) int64 {
	turnID = strings.TrimSpace(turnID)
	candidateHash = strings.TrimSpace(candidateHash)
	var base int64
	for _, attempt := range attempts {
		if attempt.SourceTurnID == turnID && attempt.Section == section &&
			attempt.CandidateRevision == revision && attempt.CandidateHash == candidateHash &&
			attempt.SDKAttemptOrdinal > base {
			base = attempt.SDKAttemptOrdinal
		}
	}
	return base
}

func maxInt64Runner(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (r *turnRunner) runDeterministicFinalizationAndPersist(ctx context.Context, turn completion.CompletionTurn, in *turnInput, actor conversationActor, decision actorDecision, telemetry *turnLifecycleTelemetry) error {
	if in == nil || in.session == nil || in.session.DeclarationCandidate == nil || in.conv == nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "typed affirmed candidate required", telemetry)
	}
	current := in.session
	candidate := current.DeclarationCandidate
	finalizedAt := candidate.Affirmation.AffirmedAt
	finalized, err := hostedgenesis.FinalizeDeclarationCandidate(candidate, turn.TurnID, finalizedAt)
	if err != nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidProducedDeclarations, "candidate finalization rejected", telemetry)
	}
	checkpoint, err := r.buildCandidateDeclarationCheckpoint(turn, *in, finalized)
	if err != nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidProducedDeclarations, "candidate checkpoint rejected", telemetry)
	}
	progressed := cloneSessionForRunner(current)
	progressed.Status = string(hostedgenesis.StatusDeclarationReady)
	progressed.DeclarationCandidate = finalized
	progressed.DeclarationCheckpoint = &checkpoint
	progressed.Failure = nil
	progressed.RequestID = strings.TrimSpace(turn.RequestID)
	progressed.UpdatedAt = finalizedAt
	progressed.CompletedAt = finalizedAt
	vmCheckpoint, err := actor.checkpoint(*in, completionTurnView{turnID: turn.TurnID, requestID: turn.RequestID}, decision, finalized.CandidateHash)
	if err != nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "build finalization checkpoint", telemetry)
	}
	progressed.VMCheckpoint = vmCheckpoint
	conv := *in.conv
	conv.ProducedDeclarations = models.EncodeSoulMintConversationBlob(finalized.CanonicalJSON)
	conv.Status = models.SoulMintConversationStatusDeclarationReady
	conv.StatusReason = ""
	conv.LatestTurnID = strings.TrimSpace(turn.TurnID)
	conv.RequestID = strings.TrimSpace(turn.RequestID)
	conv.UpdatedAt = finalizedAt
	conv.CompletedAt = finalizedAt
	if err := r.store.FinalizeHostedGenesisCandidateAndConversation(ctx, progressed, current.Version, hostedgenesis.StatusInProgress, turn.TurnID, candidate.Revision, candidate.CandidateHash, &conv); err != nil {
		return fmt.Errorf("finalize typed declaration candidate: %w", err)
	}
	in.session, in.conv = progressed, &conv
	telemetry.emit("persist", "persist_completed", string(decision.action), "")
	return nil
}

func (r *turnRunner) persistAssistantTurnAtomically(ctx context.Context, in *turnInput, turn completion.CompletionTurn, messages []llm.MintConversationMessage, usage models.AIUsage, checkpoint *hostedgenesis.VMCheckpointMetadata) error {
	if r == nil || r.store == nil || in == nil || in.conv == nil {
		return errors.New("conversation store is not initialized")
	}
	body, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("marshal assistant transcript: %w", err)
	}
	conv := *in.conv
	conv.Messages = models.EncodeSoulMintConversationBlob(string(body))
	conv.Usage = addAIUsage(conv.Usage, usage)
	conv.Status = models.SoulMintConversationStatusAssistantTurnReady
	conv.StatusReason = ""
	conv.LatestTurnID = strings.TrimSpace(turn.TurnID)
	conv.RequestID = firstNonEmpty(strings.TrimSpace(turn.RequestID), conv.RequestID)
	conv.UpdatedAt = r.now()
	current := in.session
	progressed := cloneSessionForRunner(current)
	progressed.Status = string(hostedgenesis.StatusAssistantTurnReady)
	progressed.MessageCount = maxIntRunner(progressed.MessageCount, len(messages))
	progressed.AssistantCheckpointRef = hostedgenesis.CheckpointRef("assistant", progressed.ConversationID, turn.TurnID)
	progressed.VMCheckpoint = checkpoint
	progressed.Failure = nil
	progressed.RequestID = strings.TrimSpace(turn.RequestID)
	progressed.UpdatedAt = conv.UpdatedAt
	progressed.CompletedAt = time.Time{}
	if err := r.store.RecordHostedGenesisAssistantTurnAndConversation(ctx, progressed, current.Version, hostedgenesis.StatusInProgress, turn.TurnID, current.DeclarationCandidate.Revision, current.DeclarationCandidate.CandidateHash, &conv); err != nil {
		return err
	}
	progressed.Version = current.Version + 1
	in.session, in.conv = progressed, &conv
	return nil
}

func maxIntRunner(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func (r *turnRunner) buildCandidateDeclarationCheckpoint(turn completion.CompletionTurn, in turnInput, candidate *hostedgenesis.DeclarationCandidate) (hostedgenesis.DeclarationCheckpoint, error) {
	if candidate == nil || candidate.Affirmation == nil || candidate.Phase != hostedgenesis.DeclarationCandidatePhaseFinalized {
		return hostedgenesis.DeclarationCheckpoint{}, errors.New("finalized declaration candidate required")
	}
	declarationHash, hashHex, err := hashDeclarationJSON(candidate.CanonicalJSON)
	if err != nil || declarationHash != candidate.CandidateHash {
		return hostedgenesis.DeclarationCheckpoint{}, errors.New("candidate canonical hash mismatch")
	}
	return hostedgenesis.DeclarationCheckpoint{
		DeclarationID: "decl_" + hashHex[:16], DeclarationHash: declarationHash,
		CheckpointRef: hostedgenesis.CheckpointRef("declaration", in.session.ConversationID, hashHex[:16]),
		ProducedAt:    candidate.Affirmation.AffirmedAt, RegistrationID: in.session.RegistrationID,
		ConversationID: in.session.ConversationID, AgentID: in.session.AgentID, MessageCount: len(in.messages),
		Model: in.modelSet, SchemaVersion: candidate.SchemaVersion, GuidanceVersion: candidate.GuidanceVersion, RequestID: turn.RequestID,
	}, nil
}

func cloneSessionForRunner(session *models.HostedGenesisSession) *models.HostedGenesisSession {
	if session == nil {
		return nil
	}
	copy := *session
	copy.TurnLedger = append([]hostedgenesis.TurnLedgerEntry(nil), session.TurnLedger...)
	copy.DeclarationCandidate = session.DeclarationCandidate.Clone()
	if session.MicroVMLifecycleRef != nil {
		v := *session.MicroVMLifecycleRef
		copy.MicroVMLifecycleRef = &v
	}
	if session.DeclarationCheckpoint != nil {
		v := *session.DeclarationCheckpoint
		copy.DeclarationCheckpoint = &v
	}
	if session.Publication != nil {
		v := *session.Publication
		copy.Publication = &v
	}
	if session.Failure != nil {
		v := *session.Failure
		copy.Failure = &v
	}
	if session.TraceIDs != nil {
		v := *session.TraceIDs
		copy.TraceIDs = &v
	}
	if session.VMCheckpoint != nil {
		v := *session.VMCheckpoint
		copy.VMCheckpoint = &v
	}
	return &copy
}

func addAIUsage(existing models.AIUsage, delta models.AIUsage) models.AIUsage {
	out := existing
	if strings.TrimSpace(out.Provider) == "" {
		out.Provider = strings.TrimSpace(delta.Provider)
	}
	if strings.TrimSpace(out.Model) == "" {
		out.Model = strings.TrimSpace(delta.Model)
	}
	out.InputTokens += delta.InputTokens
	out.OutputTokens += delta.OutputTokens
	total := delta.TotalTokens
	if total == 0 && (delta.InputTokens != 0 || delta.OutputTokens != 0) {
		total = delta.InputTokens + delta.OutputTokens
	}
	out.TotalTokens += total
	out.DurationMs += delta.DurationMs
	out.ToolCalls += delta.ToolCalls
	return out
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (r *turnRunner) recordFailure(ctx context.Context, turn completion.CompletionTurn, code hostedgenesis.FailureCode, message string, telemetry *turnLifecycleTelemetry, classes ...hostedgenesis.FailureClass) error {
	var failureClass hostedgenesis.FailureClass
	if len(classes) > 0 {
		failureClass = classes[0]
	}
	telemetryClass := string(failureClass)
	if telemetryClass == "" {
		telemetryClass = string(code)
	}
	telemetry.emit("failure_persist", "failure_persist_started", "", telemetryClass)
	_, err := r.writer.RecordFailure(ctx, turn, completion.CompletionFailure{
		Code:      code,
		Class:     failureClass,
		Message:   message,
		Retryable: isRetryableFailureCode(code),
		Recovery: hostedgenesis.Recovery{
			Action:            recoveryActionFor(code),
			MaxAttempts:       3,
			RetryAfterSeconds: 5,
			Reason:            message,
		},
	})
	if err == nil {
		telemetry.emit("failure_persist", "failure_persist_completed", "", telemetryClass)
		return nil
	}
	telemetry.emit("failure_persist", "failure_persist_failed", "", "completion_conflict")
	// A conflict recording failure means the session already reached a terminal
	// state; report the original failure as the run error but do not mask the
	// conflict.
	if errors.Is(err, completion.ErrCompletionConflict) {
		return fmt.Errorf("record failure (%s): session already terminal: %w", code, err)
	}
	return fmt.Errorf("record failure (%s): %w", code, err)
}

func (r *turnRunner) now() time.Time {
	if r.nowFunc != nil {
		return r.nowFunc().UTC()
	}
	return time.Now().UTC()
}

// providerTimeout is a whole-call deadline. The SDK-configured HTTP client
// timeout is per request attempt; both provider SDKs retry by default, so that
// transport timeout alone can outlive the MicroVM's maximum duration. The
// call-scoped context bounds the complete retry lifecycle and leaves the outer
// workload envelope time to persist a guarded typed failure.
func (r *turnRunner) providerTimeout() time.Duration {
	if r != nil && r.providerCallTimeout > 0 {
		return r.providerCallTimeout
	}
	return llm.DefaultProviderHTTPTimeout
}

func (r *turnRunner) workloadTimeout() time.Duration {
	if r != nil && r.workloadExecutionTimeout > 0 {
		return r.workloadExecutionTimeout
	}
	return hostedgenesis.DefaultWorkloadExecutionTimeout
}

func (r *turnRunner) providerHeartbeat() time.Duration {
	if r != nil && r.providerHeartbeatInterval > 0 {
		return r.providerHeartbeatInterval
	}
	return defaultProviderHeartbeatInterval
}

// providerFailureMessage deliberately excludes SDK error text. Provider errors
// may echo request/response bodies or headers, so this detail remains the
// established content-free phase/kind message while Failure.Class carries the
// canonical durable provider taxonomy.
func providerFailureMessage(phase string, err error) string {
	failureClass := llm.ProviderFailureClass(err)
	failureKind := "provider_error"
	switch failureClass {
	case string(hostedgenesis.FailureClassProviderTimeout):
		failureKind = "timeout"
	case string(hostedgenesis.FailureClassProviderCanceled):
		failureKind = "canceled"
	case string(hostedgenesis.FailureClassInvalidProviderOutput), string(hostedgenesis.FailureClassParseValidation):
		failureKind = failureClass
	}
	return strings.TrimSpace(phase) + " " + failureKind
}

func isRetryableFailureCode(code hostedgenesis.FailureCode) bool {
	switch code {
	case hostedgenesis.FailureCodeLLMUnavailable,
		hostedgenesis.FailureCodeAssistantTurnFailed:
		return true
	default:
		return false
	}
}

func recoveryActionFor(code hostedgenesis.FailureCode) hostedgenesis.RecoveryAction {
	switch code {
	case hostedgenesis.FailureCodeAssistantTurnFailed,
		hostedgenesis.FailureCodeLLMUnavailable:
		return hostedgenesis.RecoveryActionRetrySameStep
	case hostedgenesis.FailureCodeInvalidCompletionState,
		hostedgenesis.FailureCodeMissingProducedDeclarations,
		hostedgenesis.FailureCodeInvalidProducedDeclarations:
		return hostedgenesis.RecoveryActionRestartSoulBootstrap
	default:
		return hostedgenesis.RecoveryActionOperatorAction
	}
}

// ssmKeyLoader loads a provider API key from SSM. It is a package-level
// indirection so unit tests can substitute a fake loader without standing up a
// real AWS SSM client. The production default delegates to internal/secrets
// (OpenAIServiceKey / ClaudeAPIKey), which read the same unscoped SecureString
// params the control plane reads (/lesser-host/api/openai/service and
// /lesser-host/api/claude). The in-VM workload can reach SSM because the
// host-owned MicroVM execution role (AppTheory executionRole
// propagation) grants ssm:GetParameter + kms:Decrypt on exactly those params.
type ssmKeyLoader func(ctx context.Context, client secrets.SSMAPI) (string, error)

// providerSSMLoaders resolves the SSM fallback loader for each provider family.
// Defaults delegate to internal/secrets; tests override via setProviderSSMLoaders.
var providerSSMLoaders = map[string]ssmKeyLoader{
	"openai":    secrets.OpenAIServiceKey,
	"anthropic": secrets.ClaudeAPIKey,
}

// providerAPIKey resolves the provider API key for a model set, env-first with
// an SSM fallback. It mirrors the control-plane apiKeyForMintConversationModel
// resolution: OPENAI_API_KEY / ANTHROPIC_API_KEY (or CLAUDE_API_KEY) win when
// set; otherwise the loader reads the provider-key SecureString from SSM.
//
// P52 H1.5 corrective: the prior comment held that SSM
// fallback was "a control-plane concern and not available in the MicroVM image
// env" — that was true when the in-VM workload had no IAM identity. With
// execution-role propagation, the MicroVM assumes the host-owned execution role
// (DynamoDB + SSM provider-key grants), so SSM fallback is now available and is
// the production path: the image env never carries raw provider keys (the
// execution role + SSM keeps them out of the image/CloudFormation), while the
// env-first path is preserved for local tests that set OPENAI_API_KEY directly.
// A missing key in both env and SSM fails closed.
func providerAPIKey(ctx context.Context, modelSet string) (string, error) {
	definition, resolveErr := modelselection.ResolveModelSet(strings.TrimSpace(modelSet))
	switch {
	case resolveErr == nil && definition.Provider == modelselection.ProviderOpenAI:
		if k := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); k != "" {
			return k, nil
		}
		loader := providerSSMLoaders["openai"]
		if loader == nil {
			return "", errors.New("openai provider key not configured")
		}
		k, err := loader(ctx, nil)
		if err != nil {
			return "", errors.New("openai provider key not configured")
		}
		return k, nil
	case resolveErr == nil && definition.Provider == modelselection.ProviderAnthropic:
		if k := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); k != "" {
			return k, nil
		}
		if k := strings.TrimSpace(os.Getenv("CLAUDE_API_KEY")); k != "" {
			return k, nil
		}
		loader := providerSSMLoaders["anthropic"]
		if loader == nil {
			return "", errors.New("anthropic provider key not configured")
		}
		k, err := loader(ctx, nil)
		if err != nil {
			return "", errors.New("anthropic provider key not configured")
		}
		return k, nil
	default:
		return "", fmt.Errorf("unsupported model set %q", modelSet)
	}
}

// decodeMintConversationMessages decodes the stored conversation blob into
// provider-ready MintConversationMessage values.
func decodeMintConversationMessages(stored string) ([]llm.MintConversationMessage, error) {
	raw := strings.TrimSpace(models.DecodeSoulMintConversationBlob(stored))
	if raw == "" {
		return nil, errors.New("conversation has no messages")
	}
	var msgs []llm.MintConversationMessage
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		return nil, fmt.Errorf("unmarshal messages: %w", err)
	}
	if len(msgs) == 0 {
		return nil, errors.New("conversation has no messages")
	}
	out := make([]llm.MintConversationMessage, 0, len(msgs))
	for _, m := range msgs {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		content := strings.TrimSpace(m.Content)
		if role == "" || content == "" {
			continue
		}
		out = append(out, llm.MintConversationMessage{Role: role, Content: content})
	}
	if len(out) == 0 {
		return nil, errors.New("conversation has no non-empty messages")
	}
	return out, nil
}
