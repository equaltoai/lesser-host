package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/mintprompt"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// turnStore is the minimal HostedGenesisSession + transcript + registration
// surface the workload needs to load prompt-ready turn input. It is satisfied by
// *store.Store; the workload never receives a raw AWS SDK client.
type turnStore interface {
	GetHostedGenesisSession(ctx context.Context, instanceSlug string, conversationID string) (*models.HostedGenesisSession, error)
	GetSoulAgentMintConversation(ctx context.Context, agentID string, conversationID string) (*models.SoulAgentMintConversation, error)
	PutSoulAgentMintConversation(ctx context.Context, item *models.SoulAgentMintConversation) error
	GetSoulAgentRegistration(ctx context.Context, id string) (*models.SoulAgentRegistration, error)
}

// turnRunner is the in-VM hosted-genesis workload's conversation actor runtime.
// It loads the authoritative Host transcript + registration through the
// existing store layer, lets the in-VM actor decide the next bounded action
// (ask, wait, revise, extract/finalize, or fail recoverably), executes provider
// work through the existing internal/ai/llm clients (which carry an explicit
// HTTP timeout configured at process startup), and durably records the outcome
// through the completion writer. Host remains the guarded writer for status,
// version, debit, idempotency, and finalization; the workload never receives a
// raw AWS SDK client, a bearer token, or a raw lifecycle hook payload.
//
// The runner is fail-closed: a missing session/conversation/registration, a
// missing provider key, an empty assistant response, or a declaration-extraction
// failure surfaces as a typed completion failure written to session truth —
// never as a silent HTTP 200 or a swallowed error.
type turnStoreFactory func(context.Context) (turnStore, *completion.CompletionWriter, error)

type turnRunner struct {
	store                     turnStore
	writer                    *completion.CompletionWriter
	storeFactory              turnStoreFactory
	nowFunc                   func() time.Time
	providerCallTimeout       time.Duration
	workloadExecutionTimeout  time.Duration
	providerHeartbeatInterval time.Duration
}

// turnInput is the resolved, prompt-ready input for one assistant turn. It is
// loaded from HostedGenesisSession + SoulAgentMintConversation +
// SoulAgentRegistration truth.
type turnInput struct {
	modelSet     string
	systemPrompt string
	messages     []llm.MintConversationMessage
	contract     hostedgenesis.DeclarationContract
	registration *models.SoulAgentRegistration
	conv         *models.SoulAgentMintConversation
	session      *models.HostedGenesisSession
}

// loadTurnInput loads the authoritative transcript and registration context for
// a completion turn. Failures are loud — there is no degraded mode.
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
	contract, contractErr := hostedgenesis.RequireFiveBodyDeclarationContractFromEnv()
	if contractErr != nil {
		return turnInput{}, fmt.Errorf("resolve hosted genesis declaration contract: %w", contractErr)
	}
	systemPrompt, promptErr := mintprompt.MintConversationSystemPromptForContract(reg, contract)
	if promptErr != nil {
		return turnInput{}, fmt.Errorf("build hosted genesis system prompt: %w", promptErr)
	}

	return turnInput{
		modelSet:     modelSet,
		systemPrompt: systemPrompt,
		messages:     messages,
		contract:     contract,
		registration: reg,
		conv:         conv,
		session:      session,
	}, nil
}

// runAssistantTurn executes the assistant turn for the loaded transcript and
// returns the full trimmed assistant response + usage. The provider is selected
// by model-set prefix, mirroring the control-plane runner. Provider keys come
// from the process environment (the image is provisioned with them at deploy
// time); a missing key fails closed.
func (r *turnRunner) runAssistantTurn(ctx context.Context, turn completion.CompletionTurn, in turnInput) (string, models.AIUsage, error) {
	apiKey, err := providerAPIKey(ctx, in.modelSet)
	if err != nil {
		return "", models.AIUsage{}, err
	}
	providerCtx, cancel := context.WithTimeout(ctx, r.providerTimeout())
	defer cancel()
	telemetry := newProviderCallTelemetry(turn, in, "assistant_stream")
	stopHeartbeat := telemetry.startHeartbeat(providerCtx, r.providerHeartbeat())
	defer stopHeartbeat()
	modelSet := strings.ToLower(strings.TrimSpace(in.modelSet))
	switch {
	case strings.HasPrefix(modelSet, "openai:"):
		return llm.StreamMintConversationOpenAIWithTelemetry(providerCtx, apiKey, in.modelSet, in.systemPrompt, in.messages, nil, telemetry.observe)
	case strings.HasPrefix(modelSet, "anthropic:"):
		return llm.StreamMintConversationAnthropicWithTelemetry(providerCtx, apiKey, in.modelSet, in.systemPrompt, in.messages, nil, telemetry.observe)
	default:
		return "", models.AIUsage{}, fmt.Errorf("unsupported model set %q", in.modelSet)
	}
}

// runDeclarationExtraction extracts structured declarations from the supplied
// post-turn transcript (accepted user messages + produced assistant message)
// through the existing internal/ai/llm declaration clients.
func (r *turnRunner) runDeclarationExtraction(ctx context.Context, turn completion.CompletionTurn, in turnInput, messages []llm.MintConversationMessage) (llm.MintConversationDeclarationsDraft, models.AIUsage, error) {
	apiKey, err := providerAPIKey(ctx, in.modelSet)
	if err != nil {
		return llm.MintConversationDeclarationsDraft{}, models.AIUsage{}, err
	}
	providerCtx, cancel := context.WithTimeout(ctx, r.providerTimeout())
	defer cancel()
	telemetry := newProviderCallTelemetry(turn, in, "declaration_extraction")
	stopHeartbeat := telemetry.startHeartbeat(providerCtx, r.providerHeartbeat())
	defer stopHeartbeat()
	declInput := llm.MintConversationDeclarationsInput{
		SchemaVersion:   in.contract.SchemaVersion,
		GuidanceVersion: in.contract.GuidanceVersion,
		Registration: llm.MintConversationRegistrationContext{
			Domain:               in.registration.DomainNormalized,
			LocalID:              in.registration.LocalID,
			AgentID:              in.registration.AgentID,
			DeclaredCapabilities: hostedgenesis.FilterDeclaredCapabilitiesForPrompt(in.registration.Capabilities),
		},
		Messages: append([]llm.MintConversationMessage(nil), messages...),
	}
	modelSet := strings.ToLower(strings.TrimSpace(in.modelSet))
	switch {
	case strings.HasPrefix(modelSet, "openai:"):
		return llm.MintConversationDeclarationsOpenAIWithTelemetry(providerCtx, apiKey, in.modelSet, declInput, telemetry.observe)
	case strings.HasPrefix(modelSet, "anthropic:"):
		return llm.MintConversationDeclarationsAnthropicWithTelemetry(providerCtx, apiKey, in.modelSet, declInput, telemetry.observe)
	default:
		return llm.MintConversationDeclarationsDraft{}, models.AIUsage{}, fmt.Errorf("unsupported model set %q", in.modelSet)
	}
}

// runTurnAndPersist is the run-hook's durable execution path. It loads Host
// truth, asks the in-VM actor for the next action, and applies the resulting
// completion write under Host-owned status/version/turn guards. A replay
// against an already-advanced session is recorded as a conflict (not a silent
// re-apply).
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
		return runner.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, err.Error(), telemetry)
	}
	telemetry.bind(in)
	telemetry.emit("store_preflight", "store_preflight_completed", "", "")
	return runner.runPreparedTurnAndPersist(ctx, turn, in, telemetry)
}

// prepareTurn initializes a fresh turn-scoped store and performs the first
// authoritative DynamoDB reads before the controller acknowledges the workload
// invocation. AWS credential providers retrieve lazily, so constructing the
// TableTheory client alone is not a sufficient readiness check. Returning the
// prepared runner + input lets the endpoint detach only provider work, while a
// credential/store failure remains visible to the controller and AI worker.
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
	case actorActionAsk, actorActionRevise:
		return r.runAssistantTurnAndPersist(ctx, turn, in, actor, decision, telemetry)
	case actorActionExtractFinalize:
		// The VM actor owns the extract/finalize decision. Host has already
		// accepted the user turn, applied billing/idempotency policy, and left the
		// latest turn in guarded Host truth; RecordDeclarationReady enforces the
		// current status/version/turn checkpoint instead of requiring a separate
		// Host-orchestrated declaration_extraction_pending job.
		return r.runDeclarationExtractionAndPersist(ctx, turn, in, actor, decision, telemetry)
	case actorActionWait:
		return nil
	case actorActionFailRecoverably:
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, decision.reason, telemetry)
	default:
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "unknown hosted genesis actor decision", telemetry)
	}
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
	copy.store = st
	copy.writer = writer
	return &copy, nil
}

func (r *turnRunner) runAssistantTurnAndPersist(ctx context.Context, turn completion.CompletionTurn, in turnInput, actor conversationActor, decision actorDecision, telemetry *turnLifecycleTelemetry) error {
	assistantContent, assistantUsage, err := r.runAssistantTurn(ctx, turn, in)
	if err != nil || strings.TrimSpace(assistantContent) == "" {
		msg := "assistant turn failed"
		failureClass := hostedgenesis.FailureClassInvalidProviderOutput
		if err != nil {
			msg = providerFailureMessage("assistant_stream", err)
			failureClass = hostedgenesis.NormalizeFailureClass(llm.ProviderFailureClass(err))
		}
		telemetry.emit("assistant_stream", "provider_call_failed", string(decision.action), string(failureClass))
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeAssistantTurnFailed, msg, telemetry, failureClass)
	}

	postTurnMessageCount := len(in.messages) + 1
	postTurnMessages := append(append([]llm.MintConversationMessage(nil), in.messages...), llm.MintConversationMessage{
		Role:    "assistant",
		Content: strings.TrimSpace(assistantContent),
	})
	telemetry.emit("persist", "persist_started", string(decision.action), "")
	if persistErr := r.persistConversationAssistantTurn(ctx, &in, turn, postTurnMessages, assistantUsage); persistErr != nil {
		telemetry.emit("persist", "persist_failed", string(decision.action), "store_error")
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeAssistantTurnFailed, "persist assistant turn: "+persistErr.Error(), telemetry)
	}
	checkpoint, checkpointErr := actor.checkpoint(in, completionTurnView{turnID: turn.TurnID, requestID: turn.RequestID}, decision, hostedgenesis.CheckpointRef("assistant", in.session.ConversationID, turn.TurnID))
	if checkpointErr != nil {
		telemetry.emit("persist", "persist_failed", string(decision.action), "checkpoint_error")
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "build assistant checkpoint: "+checkpointErr.Error(), telemetry)
	}
	if _, werr := r.writer.RecordAssistantTurnReadyWithCheckpoint(ctx, turn, completion.AssistantTurnCompletion{
		AssistantContent: assistantContent,
		MessageCount:     postTurnMessageCount,
	}, checkpoint); werr != nil {
		telemetry.emit("persist", "persist_failed", string(decision.action), "completion_conflict")
		// A conflict here means the session already advanced (e.g. a replay or
		// a concurrent recovery). Do not overwrite; surface the conflict.
		return fmt.Errorf("record assistant turn ready: %w", werr)
	}
	telemetry.emit("persist", "persist_completed", string(decision.action), "")
	return nil
}

func (r *turnRunner) runDeclarationExtractionAndPersist(ctx context.Context, turn completion.CompletionTurn, in turnInput, actor conversationActor, decision actorDecision, telemetry *turnLifecycleTelemetry) error {
	if !hasAssistantMessage(in.messages) {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "declaration extraction requires an assistant transcript", telemetry)
	}
	draft, declarationUsage, err := r.runDeclarationExtraction(ctx, turn, in, in.messages)
	if err != nil {
		failureClass := hostedgenesis.NormalizeFailureClass(llm.ProviderFailureClass(err))
		telemetry.emit("declaration_extraction", "provider_call_failed", string(decision.action), string(failureClass))
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeDeclarationExtractionFailed, providerFailureMessage("declaration_extraction", err), telemetry, failureClass)
	}
	telemetry.emit("declaration_validation", "validation_started", string(decision.action), "")
	declarationsJSON, err := r.buildProducedDeclarationsJSON(draft, in.modelSet, in.contract)
	if err != nil {
		telemetry.emit("declaration_validation", "validation_failed", string(decision.action), "validation_error")
		if errors.Is(err, hostedgenesis.ErrDeclarationContractUnconfigured) {
			return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeOperatorActionRequired, err.Error(), telemetry)
		}
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidProducedDeclarations, string(hostedgenesis.DeclarationValidationCodeFromError(err)), telemetry, hostedgenesis.FailureClassParseValidation)
	}
	telemetry.emit("declaration_validation", "validation_completed", string(decision.action), "")
	telemetry.emit("persist", "persist_started", string(decision.action), "")
	if persistErr := r.persistConversationDeclarationReady(ctx, &in, turn, declarationsJSON, declarationUsage); persistErr != nil {
		telemetry.emit("persist", "persist_failed", string(decision.action), "store_error")
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidProducedDeclarations, "persist declarations: "+persistErr.Error(), telemetry)
	}
	checkpoint, err := r.buildDeclarationCheckpoint(turn, in, declarationsJSON)
	if err != nil {
		telemetry.emit("persist", "persist_failed", string(decision.action), "checkpoint_error")
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidProducedDeclarations, err.Error(), telemetry)
	}
	vmCheckpoint, checkpointErr := actor.checkpoint(in, completionTurnView{turnID: turn.TurnID, requestID: turn.RequestID}, decision, checkpoint.DeclarationHash)
	if checkpointErr != nil {
		telemetry.emit("persist", "persist_failed", string(decision.action), "checkpoint_error")
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, "build declaration checkpoint: "+checkpointErr.Error(), telemetry)
	}
	if _, werr := r.writer.RecordDeclarationReadyWithCheckpoint(ctx, turn, checkpoint, vmCheckpoint); werr != nil {
		telemetry.emit("persist", "persist_failed", string(decision.action), "completion_conflict")
		return fmt.Errorf("record declaration ready: %w", werr)
	}
	telemetry.emit("persist", "persist_completed", string(decision.action), "")
	return nil
}

func hasAssistantMessage(messages []llm.MintConversationMessage) bool {
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") && strings.TrimSpace(msg.Content) != "" {
			return true
		}
	}
	return false
}

type producedDeclarations struct {
	SchemaVersion     string                             `json:"schemaVersion,omitempty"`
	GuidanceVersion   string                             `json:"guidanceVersion,omitempty"`
	FiveBodies        *hostedgenesis.FiveBodyDeclaration `json:"fiveBodies,omitempty"`
	SelfDescription   soul.SelfDescriptionV2             `json:"selfDescription"`
	Capabilities      []soul.CapabilityV2                `json:"capabilities"`
	Boundaries        []soul.BoundaryV2                  `json:"boundaries"`
	Transparency      map[string]any                     `json:"transparency"`
	AdversarialReview *hostedgenesis.AdversarialReview   `json:"adversarialReview,omitempty"`
}

func (r *turnRunner) persistConversationAssistantTurn(ctx context.Context, in *turnInput, turn completion.CompletionTurn, messages []llm.MintConversationMessage, usage models.AIUsage) error {
	if r == nil || r.store == nil || in.conv == nil {
		return errors.New("conversation store is not initialized")
	}
	body, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("marshal assistant transcript: %w", err)
	}
	now := r.now()
	conv := *in.conv
	conv.Messages = models.EncodeSoulMintConversationBlob(string(body))
	conv.Usage = addAIUsage(conv.Usage, usage)
	conv.Status = models.SoulMintConversationStatusAssistantTurnReady
	conv.StatusReason = ""
	conv.LatestTurnID = strings.TrimSpace(turn.TurnID)
	conv.RequestID = firstNonEmpty(strings.TrimSpace(turn.RequestID), conv.RequestID)
	conv.UpdatedAt = now
	if err := conv.UpdateKeys(); err != nil {
		return err
	}
	if err := r.store.PutSoulAgentMintConversation(ctx, &conv); err != nil {
		return err
	}
	in.conv = &conv
	return nil
}

func (r *turnRunner) persistConversationDeclarationReady(ctx context.Context, in *turnInput, turn completion.CompletionTurn, declarationsJSON string, usage models.AIUsage) error {
	if r == nil || r.store == nil || in.conv == nil {
		return errors.New("conversation store is not initialized")
	}
	now := r.now()
	conv := *in.conv
	conv.ProducedDeclarations = models.EncodeSoulMintConversationBlob(strings.TrimSpace(declarationsJSON))
	conv.Usage = addAIUsage(conv.Usage, usage)
	conv.Status = models.SoulMintConversationStatusDeclarationReady
	conv.StatusReason = ""
	conv.LatestTurnID = strings.TrimSpace(turn.TurnID)
	conv.RequestID = firstNonEmpty(strings.TrimSpace(turn.RequestID), conv.RequestID)
	conv.CompletedAt = now
	conv.UpdatedAt = now
	if err := conv.UpdateKeys(); err != nil {
		return err
	}
	if err := r.store.PutSoulAgentMintConversation(ctx, &conv); err != nil {
		return err
	}
	in.conv = &conv
	return nil
}

// buildProducedDeclarationsJSON builds the instance-trust hosted/off-chain
// declaration payload. Registration-declared capabilities are model context,
// not produced evidence; an empty extracted capabilities array is valid.
// Fresh hosted-genesis production is five-body-only: a contract that does not
// affirmatively select the five-body lane fails closed here instead of
// routing to a legacy declaration builder.
func (r *turnRunner) buildProducedDeclarationsJSON(draft llm.MintConversationDeclarationsDraft, modelSet string, contract hostedgenesis.DeclarationContract) (string, error) {
	if !contract.IsFiveBody() {
		return "", hostedgenesis.ErrDeclarationContractUnconfigured
	}
	return r.buildFiveBodyProducedDeclarationsJSON(draft, modelSet, hostedgenesis.FiveBodyDeclarationContract())
}

func (r *turnRunner) buildFiveBodyProducedDeclarationsJSON(draft llm.MintConversationDeclarationsDraft, modelSet string, contract hostedgenesis.DeclarationContract) (string, error) {
	now := r.now()
	if err := hostedgenesis.ValidateDeclarationContractVersions(draft.SchemaVersion, draft.GuidanceVersion, contract); err != nil {
		return "", err
	}
	fiveBodies := hostedgenesis.NormalizeFiveBodyDeclaration(draft.FiveBodies)
	if err := hostedgenesis.ValidateFiveBodyDeclaration(fiveBodies); err != nil {
		return "", err
	}
	review, reviewErr := hostedgenesis.BuildAdversarialReviewV2(fiveBodies)
	if reviewErr != nil {
		return "", reviewErr
	}
	decl := producedDeclarations{
		SchemaVersion:     contract.SchemaVersion,
		GuidanceVersion:   contract.GuidanceVersion,
		FiveBodies:        &fiveBodies,
		SelfDescription:   fiveBodySelfDescription(draft.SelfDescription, fiveBodies, modelSet),
		Capabilities:      []soul.CapabilityV2{},
		Boundaries:        hostedgenesis.FiveBodyBoundaries(fiveBodies.Soul.Refusals, now),
		Transparency:      draft.Transparency,
		AdversarialReview: &review,
	}
	if selfDescriptionErr := decl.SelfDescription.Validate(); selfDescriptionErr != nil {
		return "", hostedgenesis.NewDeclarationValidationError(hostedgenesis.DeclarationCodeSelfDescription)
	}
	var capErr error
	decl.Capabilities, capErr = hostedgenesis.ValidateAndNormalizeProducedCapabilities(draft.Capabilities)
	if capErr != nil {
		return "", capErr
	}
	if len(decl.Boundaries) < 3 {
		return "", hostedgenesis.NewDeclarationValidationError(hostedgenesis.DeclarationCodeSoulRefusals)
	}
	for i := range decl.Boundaries {
		if boundaryErr := decl.Boundaries[i].Validate(); boundaryErr != nil {
			return "", hostedgenesis.NewDeclarationValidationError(hostedgenesis.DeclarationCodeBoundariesBad)
		}
	}
	if decl.Transparency == nil {
		decl.Transparency = map[string]any{}
	}
	if reviewValidationErr := hostedgenesis.ValidateAdversarialReview(review); reviewValidationErr != nil {
		return "", reviewValidationErr
	}
	body, err := json.Marshal(decl)
	if err != nil {
		return "", hostedgenesis.NewDeclarationValidationError(hostedgenesis.DeclarationCodeInvalid)
	}
	return string(body), nil
}

func fiveBodySelfDescription(source soul.SelfDescriptionV2, fiveBodies hostedgenesis.FiveBodyDeclaration, modelSet string) soul.SelfDescriptionV2 {
	source.Purpose = firstNonEmpty(source.Purpose, fiveBodies.Identity.Summary)
	source.Constraints = firstNonEmpty(source.Constraints, fiveBodies.Boundaries.Summary)
	source.Commitments = firstNonEmpty(source.Commitments, fiveBodies.Philosophy.Summary)
	source.Limitations = firstNonEmpty(source.Limitations, fiveBodies.Soul.Summary)
	source.AuthoredBy = "agent"
	source.MintingModel = strings.TrimSpace(modelSet)
	return source
}

// buildDeclarationCheckpoint assembles a publish-ready DeclarationCheckpoint
// over the exact ProducedDeclarations JSON persisted to the conversation. The
// checkpoint hash must match that stored blob byte-for-byte; otherwise the
// instance API correctly refuses to project declarations.
func (r *turnRunner) buildDeclarationCheckpoint(turn completion.CompletionTurn, in turnInput, declarationsJSON string) (hostedgenesis.DeclarationCheckpoint, error) {
	now := r.now()
	declarationHash, hashHex, err := hashDeclarationJSON(declarationsJSON)
	if err != nil {
		return hostedgenesis.DeclarationCheckpoint{}, err
	}
	return hostedgenesis.DeclarationCheckpoint{
		DeclarationID:   "decl_" + hashHex[:16],
		DeclarationHash: declarationHash,
		CheckpointRef:   hostedgenesis.CheckpointRef("declaration", in.session.ConversationID, hashHex[:16]),
		ProducedAt:      now,
		RegistrationID:  in.session.RegistrationID,
		ConversationID:  in.session.ConversationID,
		AgentID:         in.session.AgentID,
		MessageCount:    len(in.messages) + 1,
		Model:           in.modelSet,
		SchemaVersion:   in.contract.SchemaVersion,
		GuidanceVersion: in.contract.GuidanceVersion,
		RequestID:       turn.RequestID,
	}, nil
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
	telemetry.emit("failure_persist", "failure_persist_started", "", string(code))
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
		telemetry.emit("failure_persist", "failure_persist_completed", "", string(code))
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
		hostedgenesis.FailureCodeAssistantTurnFailed,
		hostedgenesis.FailureCodeDeclarationExtractionFailed:
		return true
	default:
		return false
	}
}

func recoveryActionFor(code hostedgenesis.FailureCode) hostedgenesis.RecoveryAction {
	switch code {
	case hostedgenesis.FailureCodeAssistantTurnFailed,
		hostedgenesis.FailureCodeDeclarationExtractionFailed,
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
	modelSetNorm := strings.ToLower(strings.TrimSpace(modelSet))
	switch {
	case strings.HasPrefix(modelSetNorm, "openai:"):
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
	case strings.HasPrefix(modelSetNorm, "anthropic:"):
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
