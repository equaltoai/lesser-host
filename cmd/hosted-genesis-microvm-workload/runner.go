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
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// turnStore is the minimal HostedGenesisSession + transcript + registration
// surface the workload needs to load prompt-ready turn input. It is satisfied by
// *store.Store; the workload never receives a raw AWS SDK client.
type turnStore interface {
	GetHostedGenesisSession(ctx context.Context, instanceSlug string, conversationID string) (*models.HostedGenesisSession, error)
	GetSoulAgentMintConversation(ctx context.Context, agentID string, conversationID string) (*models.SoulAgentMintConversation, error)
	GetSoulAgentRegistration(ctx context.Context, id string) (*models.SoulAgentRegistration, error)
}

// turnRunner is the in-VM hosted-genesis workload's assistant-turn +
// declaration-extraction executor. It loads the authoritative conversation
// transcript + registration through the existing store layer, runs the assistant
// turn and declaration extraction through the existing internal/ai/llm clients
// (which carry an explicit HTTP timeout configured at process startup), and
// durably records the outcome to HostedGenesisSession truth through the
// completion writer. It never receives a raw AWS SDK client, a bearer token, or
// a raw lifecycle hook payload.
//
// The runner is fail-closed: a missing session/conversation/registration, a
// missing provider key, an empty assistant response, or a declaration-extraction
// failure surfaces as a typed completion failure written to session truth —
// never as a silent HTTP 200 or a swallowed error.
type turnRunner struct {
	store   turnStore
	writer  *completion.CompletionWriter
	nowFunc func() time.Time
}

// turnInput is the resolved, prompt-ready input for one assistant turn. It is
// loaded from HostedGenesisSession + SoulAgentMintConversation +
// SoulAgentRegistration truth.
type turnInput struct {
	modelSet     string
	systemPrompt string
	messages     []llm.MintConversationMessage
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

	return turnInput{
		modelSet:     modelSet,
		systemPrompt: mintprompt.MintConversationSystemPrompt(reg),
		messages:     messages,
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
func (r *turnRunner) runAssistantTurn(ctx context.Context, in turnInput) (string, models.AIUsage, error) {
	apiKey, err := providerAPIKey(ctx, in.modelSet)
	if err != nil {
		return "", models.AIUsage{}, err
	}
	modelSet := strings.ToLower(strings.TrimSpace(in.modelSet))
	switch {
	case strings.HasPrefix(modelSet, "openai:"):
		return llm.StreamMintConversationOpenAI(ctx, apiKey, in.modelSet, in.systemPrompt, in.messages, func(string) {})
	case strings.HasPrefix(modelSet, "anthropic:"):
		return llm.StreamMintConversationAnthropic(ctx, apiKey, in.modelSet, in.systemPrompt, in.messages, func(string) {})
	default:
		return "", models.AIUsage{}, fmt.Errorf("unsupported model set %q", in.modelSet)
	}
}

// runDeclarationExtraction extracts structured declarations from the post-turn
// transcript (accepted user messages + produced assistant message) through the
// existing internal/ai/llm declaration clients. Returns a draft + a
// publish-ready DeclarationCheckpoint.
func (r *turnRunner) runDeclarationExtraction(ctx context.Context, in turnInput, assistantContent string) (llm.MintConversationDeclarationsDraft, models.AIUsage, error) {
	apiKey, err := providerAPIKey(ctx, in.modelSet)
	if err != nil {
		return llm.MintConversationDeclarationsDraft{}, models.AIUsage{}, err
	}
	extractionMessages := append(append([]llm.MintConversationMessage(nil), in.messages...), llm.MintConversationMessage{
		Role:    "assistant",
		Content: strings.TrimSpace(assistantContent),
	})
	declInput := llm.MintConversationDeclarationsInput{
		Registration: llm.MintConversationRegistrationContext{
			Domain:               in.registration.DomainNormalized,
			LocalID:              in.registration.LocalID,
			AgentID:              in.registration.AgentID,
			DeclaredCapabilities: in.registration.Capabilities,
		},
		Messages: extractionMessages,
	}
	modelSet := strings.ToLower(strings.TrimSpace(in.modelSet))
	switch {
	case strings.HasPrefix(modelSet, "openai:"):
		return llm.MintConversationDeclarationsOpenAI(ctx, apiKey, in.modelSet, declInput)
	case strings.HasPrefix(modelSet, "anthropic:"):
		return llm.MintConversationDeclarationsAnthropic(ctx, apiKey, in.modelSet, declInput)
	default:
		return llm.MintConversationDeclarationsDraft{}, models.AIUsage{}, fmt.Errorf("unsupported model set %q", in.modelSet)
	}
}

// runTurnAndPersist is the run-hook's durable execution path. It runs the
// assistant turn, persists assistant_turn_ready, runs declaration extraction,
// and persists declaration_ready — or persists a typed failure at any point the
// path cannot continue. Every completion write is idempotent per turn ID and
// conditional on the session's status + version; a replay against an already-
// advanced session is recorded as a conflict (not a silent re-apply).
func (r *turnRunner) runTurnAndPersist(ctx context.Context, turn completion.CompletionTurn) error {
	in, err := r.loadTurnInput(ctx, turn)
	if err != nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidCompletionState, err.Error())
	}

	assistantContent, _, err := r.runAssistantTurn(ctx, in)
	if err != nil || strings.TrimSpace(assistantContent) == "" {
		msg := "assistant turn failed"
		if err != nil {
			msg = err.Error()
		}
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeAssistantTurnFailed, msg)
	}

	postTurnMessageCount := len(in.messages) + 1
	if _, werr := r.writer.RecordAssistantTurnReady(ctx, turn, completion.AssistantTurnCompletion{
		AssistantContent: assistantContent,
		MessageCount:     postTurnMessageCount,
	}); werr != nil {
		// A conflict here means the session already advanced (e.g. a replay or
		// a concurrent recovery). Do not overwrite; surface the conflict.
		return fmt.Errorf("record assistant turn ready: %w", werr)
	}

	draft, _, err := r.runDeclarationExtraction(ctx, in, assistantContent)
	if err != nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeDeclarationExtractionFailed, err.Error())
	}
	checkpoint, err := r.buildDeclarationCheckpoint(turn, in, draft)
	if err != nil {
		return r.recordFailure(ctx, turn, hostedgenesis.FailureCodeInvalidProducedDeclarations, err.Error())
	}
	if _, werr := r.writer.RecordDeclarationReady(ctx, turn, checkpoint); werr != nil {
		return fmt.Errorf("record declaration ready: %w", werr)
	}
	return nil
}

// buildDeclarationCheckpoint assembles a publish-ready DeclarationCheckpoint
// from the extracted draft. The declaration id + hash are derived from the
// conversation + turn identity so a replay produces the same checkpoint
// (idempotent declaration_ready).
func (r *turnRunner) buildDeclarationCheckpoint(turn completion.CompletionTurn, in turnInput, draft llm.MintConversationDeclarationsDraft) (hostedgenesis.DeclarationCheckpoint, error) {
	now := r.now()
	declarationID := fmt.Sprintf("decl:%s:%s:%s", in.session.InstanceSlug, in.session.ConversationID, turn.TurnID)
	declarationHash, err := hashDeclarationDraft(draft)
	if err != nil {
		return hostedgenesis.DeclarationCheckpoint{}, err
	}
	return hostedgenesis.DeclarationCheckpoint{
		DeclarationID:   declarationID,
		DeclarationHash: declarationHash,
		CheckpointRef:   fmt.Sprintf("checkpoint://hosted-genesis/%s/declaration/%s", in.session.ConversationID, turn.TurnID),
		ProducedAt:      now,
		RegistrationID:  in.session.RegistrationID,
		ConversationID:  in.session.ConversationID,
		AgentID:         in.session.AgentID,
		MessageCount:    len(in.messages) + 1,
		Model:           in.modelSet,
		RequestID:       turn.RequestID,
	}, nil
}

func (r *turnRunner) recordFailure(ctx context.Context, turn completion.CompletionTurn, code hostedgenesis.FailureCode, message string) error {
	_, err := r.writer.RecordFailure(ctx, turn, completion.CompletionFailure{
		Code:      code,
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
		return nil
	}
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
// host-owned MicroVM execution role (AppTheory v1.15.2 executionRole
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
// P52 H1.5 corrective (AppTheory v1.15.2): the prior comment held that SSM
// fallback was "a control-plane concern and not available in the MicroVM image
// env" — that was true when the in-VM workload had no IAM identity. With v1.15.2
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
