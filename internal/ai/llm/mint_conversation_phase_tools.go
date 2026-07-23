package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	maxMintConversationPhaseToolRounds        = 4
	mintConversationPhaseSummaryMaxRunes      = 2400
	mintConversationPhaseNotesMaxItems        = 8
	mintConversationPhaseNoteMaxRunes         = 480
	mintConversationPhaseRefusalsMinItems     = 3
	mintConversationPhaseRefusalsMaxItems     = 8
	mintConversationPhaseRefusalFieldMaxRunes = 480
)

// MintConversationPhaseInput exposes exactly one typed declaration tool for
// the candidate's current section. Revision/hash are durable preconditions,
// not instructions inferred from transcript text.
type MintConversationPhaseInput struct {
	ModelSet          string
	SystemPrompt      string
	Messages          []MintConversationMessage
	Section           hostedgenesis.DeclarationSection
	CandidateRevision int64
	CandidateHash     string
	SourceTurnID      string
}

type MintConversationPhaseToolCall struct {
	Name      string
	CallID    string
	Arguments json.RawMessage
}

type MintConversationPhaseToolHandler func(context.Context, MintConversationPhaseToolCall) (hostedgenesis.DeclarationToolResult, error)

type MintConversationPhaseOutput struct {
	AssistantContent string
	Usage            models.AIUsage
}

// RunMintConversationPhase executes the provider SDK/tool loop. Only the
// section-local tool is exposed; accepted and rejected results are returned to
// the model as machine-readable JSON. The callback is where the in-MicroVM
// runner performs the guarded TableTheory checkpoint.
func RunMintConversationPhase(ctx context.Context, apiKey string, in MintConversationPhaseInput, handler MintConversationPhaseToolHandler, telemetry ProviderTelemetrySink) (MintConversationPhaseOutput, error) {
	if handler == nil {
		return MintConversationPhaseOutput{}, errors.New("declaration phase tool handler is required")
	}
	if err := validateMintConversationPhaseInput(in); err != nil {
		return MintConversationPhaseOutput{}, err
	}
	switch {
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(in.ModelSet)), "openai:"):
		return runMintConversationPhaseOpenAI(ctx, apiKey, in, handler, telemetry)
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(in.ModelSet)), "anthropic:"):
		return runMintConversationPhaseAnthropic(ctx, apiKey, in, handler, telemetry)
	default:
		return MintConversationPhaseOutput{}, fmt.Errorf("unsupported model set %q", in.ModelSet)
	}
}

func validateMintConversationPhaseInput(in MintConversationPhaseInput) error {
	toolName, ok := hostedgenesis.DeclarationToolForSection(in.Section)
	if !ok || strings.TrimSpace(toolName) == "" || in.CandidateRevision < 0 || strings.TrimSpace(in.CandidateHash) == "" || strings.TrimSpace(in.SourceTurnID) == "" {
		return errors.New("declaration phase input is invalid")
	}
	return nil
}

func runMintConversationPhaseOpenAI(ctx context.Context, apiKey string, in MintConversationPhaseInput, handler MintConversationPhaseToolHandler, telemetry ProviderTelemetrySink) (MintConversationPhaseOutput, error) {
	model, err := openAIModelFromSet(in.ModelSet)
	if err != nil {
		return MintConversationPhaseOutput{}, err
	}
	tool := openAIMintConversationPhaseTool(in.Section)
	messages := buildOpenAIConversationMessages(mintConversationPhaseSystemPrompt(in), in.Messages)
	usage := models.AIUsage{Provider: "openai", Model: model}
	toolEnabled := true
	recorder := newProviderTelemetryRecorder("openai", model, "declaration_phase", telemetry)
	ctx = withProviderAttemptTelemetry(ctx, DefaultProviderSDKRetryBudget, recorder.emitSDK)
	client := openAIClientForKey(apiKey)
	for round := 0; round < maxMintConversationPhaseToolRounds; round++ {
		requestStarted := time.Now()
		recorder.emit(ProviderTelemetryEvent{EventType: "request_start"})
		params := openai.ChatCompletionNewParams{
			Model: openai.ChatModel(model), Messages: messages,
			MaxCompletionTokens: openai.Int(mintConversationOpenAIMaxCompletionTokens),
			ParallelToolCalls:   openai.Bool(false),
			Tools:               []openai.ChatCompletionToolParam{tool},
		}
		if toolEnabled {
			params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("auto")}
		} else {
			// Keep the prior assistant tool call and result valid in the
			// continuation request while forbidding another section mutation.
			params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("none")}
		}
		chat, callErr := client.Chat.Completions.New(ctx, params)
		if callErr != nil {
			recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(callErr)})
			return MintConversationPhaseOutput{}, callErr
		}
		mergeAIUsage(&usage, openAIUsageFromChat(chat, requestStarted))
		outcome, handleErr := handleOpenAIMintConversationPhaseResponse(ctx, chat, in.Section, toolEnabled, handler, recorder, usage, &messages)
		if handleErr != nil {
			return MintConversationPhaseOutput{}, handleErr
		}
		if outcome.done {
			return outcome.output, nil
		}
		if outcome.accepted {
			toolEnabled = false
		}
	}
	return MintConversationPhaseOutput{}, errors.New("openai declaration phase exceeded bounded tool rounds")
}

type mintConversationPhaseRoundOutcome struct {
	output   MintConversationPhaseOutput
	done     bool
	accepted bool
}

func handleOpenAIMintConversationPhaseResponse(ctx context.Context, chat *openai.ChatCompletion, section hostedgenesis.DeclarationSection, toolEnabled bool, handler MintConversationPhaseToolHandler, recorder *providerTelemetryRecorder, usage models.AIUsage, messages *[]openai.ChatCompletionMessageParamUnion) (mintConversationPhaseRoundOutcome, error) {
	if len(chat.Choices) != 1 {
		return mintConversationPhaseRoundOutcome{}, errors.New("openai declaration phase returned invalid choices")
	}
	message := chat.Choices[0].Message
	*messages = append(*messages, message.ToParam())
	if len(message.ToolCalls) == 0 {
		return finishOpenAIMintConversationPhaseText(chat, recorder, usage)
	}
	if !toolEnabled || len(message.ToolCalls) != 1 {
		return mintConversationPhaseRoundOutcome{}, errors.New("openai declaration phase returned unexpected tool calls")
	}
	call := message.ToolCalls[0]
	result, err := handler(ctx, MintConversationPhaseToolCall{Name: call.Function.Name, CallID: call.ID, Arguments: json.RawMessage(call.Function.Arguments)})
	if err != nil {
		return mintConversationPhaseRoundOutcome{}, err
	}
	emitMintConversationToolValidation(recorder, call.Function.Name, call.ID, result)
	body, err := json.Marshal(result)
	if err != nil {
		return mintConversationPhaseRoundOutcome{}, err
	}
	*messages = append(*messages, openai.ToolMessage(string(body), call.ID))
	return acceptedMintConversationPhaseOutcome(section, result.Accepted, recorder, usage), nil
}

func finishOpenAIMintConversationPhaseText(chat *openai.ChatCompletion, recorder *providerTelemetryRecorder, usage models.AIUsage) (mintConversationPhaseRoundOutcome, error) {
	content := strings.TrimSpace(chat.Choices[0].Message.Content)
	if content == "" {
		return mintConversationPhaseRoundOutcome{}, errors.New("openai declaration phase returned no content or tool")
	}
	emitMintConversationPhaseCompletion(recorder, content, usage, strings.TrimSpace(chat.Choices[0].FinishReason))
	return mintConversationPhaseRoundOutcome{output: MintConversationPhaseOutput{AssistantContent: content, Usage: usage}, done: true}, nil
}

func acceptedMintConversationPhaseOutcome(section hostedgenesis.DeclarationSection, accepted bool, recorder *providerTelemetryRecorder, usage models.AIUsage) mintConversationPhaseRoundOutcome {
	if accepted && section == hostedgenesis.DeclarationSectionSoul {
		emitMintConversationPhaseCompletion(recorder, "", usage, "tool_accepted")
		return mintConversationPhaseRoundOutcome{output: MintConversationPhaseOutput{AssistantContent: "typed declaration candidate accepted", Usage: usage}, done: true, accepted: true}
	}
	return mintConversationPhaseRoundOutcome{accepted: accepted}
}

func runMintConversationPhaseAnthropic(ctx context.Context, apiKey string, in MintConversationPhaseInput, handler MintConversationPhaseToolHandler, telemetry ProviderTelemetrySink) (MintConversationPhaseOutput, error) {
	model, err := anthropicModelFromSet(in.ModelSet)
	if err != nil {
		return MintConversationPhaseOutput{}, err
	}
	messages := buildAnthropicConversationMessages(in.Messages)
	usage := models.AIUsage{Provider: "anthropic", Model: string(model)}
	toolEnabled := true
	recorder := newProviderTelemetryRecorder("anthropic", string(model), "declaration_phase", telemetry)
	ctx = withProviderAttemptTelemetry(ctx, DefaultProviderSDKRetryBudget, recorder.emitSDK)
	client := anthropicClientForKey(apiKey)
	for round := 0; round < maxMintConversationPhaseToolRounds; round++ {
		requestStarted := time.Now()
		recorder.emit(ProviderTelemetryEvent{EventType: "request_start"})
		params := anthropic.MessageNewParams{
			Model: model, MaxTokens: 4096,
			System:   []anthropic.TextBlockParam{{Text: mintConversationPhaseSystemPrompt(in)}},
			Messages: messages,
			Tools:    []anthropic.ToolUnionParam{anthropicMintConversationPhaseTool(in.Section)},
		}
		if toolEnabled {
			params.ToolChoice = anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{DisableParallelToolUse: anthropic.Bool(true)}}
		} else {
			// Anthropic likewise requires the referenced tool declaration to
			// remain present even though the post-checkpoint turn is text-only.
			none := anthropic.NewToolChoiceNoneParam()
			params.ToolChoice = anthropic.ToolChoiceUnionParam{OfNone: &none}
		}
		message, callErr := client.Messages.New(ctx, params)
		if callErr != nil {
			recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(callErr)})
			return MintConversationPhaseOutput{}, callErr
		}
		mergeAIUsage(&usage, anthropicUsageFromMessage(message, requestStarted))
		outcome, handleErr := handleAnthropicMintConversationPhaseResponse(ctx, message, in.Section, toolEnabled, handler, recorder, usage, &messages)
		if handleErr != nil {
			return MintConversationPhaseOutput{}, handleErr
		}
		if outcome.done {
			return outcome.output, nil
		}
		if outcome.accepted {
			toolEnabled = false
		}
	}
	return MintConversationPhaseOutput{}, errors.New("anthropic declaration phase exceeded bounded tool rounds")
}

func handleAnthropicMintConversationPhaseResponse(ctx context.Context, message *anthropic.Message, section hostedgenesis.DeclarationSection, toolEnabled bool, handler MintConversationPhaseToolHandler, recorder *providerTelemetryRecorder, usage models.AIUsage, messages *[]anthropic.MessageParam) (mintConversationPhaseRoundOutcome, error) {
	assistantBlocks, text, toolUses := parseAnthropicMintConversationPhaseContent(message)
	*messages = append(*messages, anthropic.NewAssistantMessage(assistantBlocks...))
	if len(toolUses) == 0 {
		return finishAnthropicMintConversationPhaseText(message, text, recorder, usage)
	}
	if !toolEnabled || len(toolUses) != 1 {
		return mintConversationPhaseRoundOutcome{}, errors.New("anthropic declaration phase returned unexpected tool calls")
	}
	call := toolUses[0]
	result, err := handler(ctx, MintConversationPhaseToolCall{Name: call.Name, CallID: call.ID, Arguments: append(json.RawMessage(nil), call.Input...)})
	if err != nil {
		return mintConversationPhaseRoundOutcome{}, err
	}
	emitMintConversationToolValidation(recorder, call.Name, call.ID, result)
	body, err := json.Marshal(result)
	if err != nil {
		return mintConversationPhaseRoundOutcome{}, err
	}
	*messages = append(*messages, anthropic.NewUserMessage(anthropic.NewToolResultBlock(call.ID, string(body), !result.Accepted)))
	return acceptedMintConversationPhaseOutcome(section, result.Accepted, recorder, usage), nil
}

func parseAnthropicMintConversationPhaseContent(message *anthropic.Message) ([]anthropic.ContentBlockParamUnion, string, []anthropic.ToolUseBlock) {
	assistantBlocks := make([]anthropic.ContentBlockParamUnion, 0, len(message.Content))
	var text strings.Builder
	var toolUses []anthropic.ToolUseBlock
	for _, block := range message.Content {
		switch value := block.AsAny().(type) {
		case anthropic.TextBlock:
			assistantBlocks = append(assistantBlocks, anthropic.NewTextBlock(value.Text))
			text.WriteString(value.Text)
		case anthropic.ToolUseBlock:
			assistantBlocks = append(assistantBlocks, anthropic.NewToolUseBlock(value.ID, value.Input, value.Name))
			toolUses = append(toolUses, value)
		}
	}
	return assistantBlocks, text.String(), toolUses
}

func finishAnthropicMintConversationPhaseText(message *anthropic.Message, text string, recorder *providerTelemetryRecorder, usage models.AIUsage) (mintConversationPhaseRoundOutcome, error) {
	content := strings.TrimSpace(text)
	if content == "" {
		return mintConversationPhaseRoundOutcome{}, errors.New("anthropic declaration phase returned no content or tool")
	}
	emitMintConversationPhaseCompletion(recorder, content, usage, strings.TrimSpace(string(message.StopReason)))
	return mintConversationPhaseRoundOutcome{output: MintConversationPhaseOutput{AssistantContent: content, Usage: usage}, done: true}, nil
}

func mintConversationPhaseSystemPrompt(in MintConversationPhaseInput) string {
	toolName, _ := hostedgenesis.DeclarationToolForSection(in.Section)
	limits := fmt.Sprintf(
		"summary at most %d Unicode characters; notes at most %d items of at most %d Unicode characters each",
		mintConversationPhaseSummaryMaxRunes,
		mintConversationPhaseNotesMaxItems,
		mintConversationPhaseNoteMaxRunes,
	)
	if in.Section == hostedgenesis.DeclarationSectionSoul {
		limits += fmt.Sprintf(
			"; refusals %d-%d items with bypass, invariant, and closestSafePath each at most %d Unicode characters",
			mintConversationPhaseRefusalsMinItems,
			mintConversationPhaseRefusalsMaxItems,
			mintConversationPhaseRefusalFieldMaxRunes,
		)
	}
	return strings.TrimSpace(in.SystemPrompt) + fmt.Sprintf(`

Typed declaration construction protocol:
- Current section: %s.
- Current candidate revision: %d.
- Current candidate hash: %s.
- Use only %s when the owner's answers support a complete current section, and copy the exact current revision/hash into its candidateRevision/candidateHash fields.
- Current-section payload limits: %s.
- Preserve every owner-supplied item for the current section. Compress wording before the tool call when necessary; Host rejects over-limit fields instead of truncating them.
- The tool result is authoritative. On a machine-readable section/path/code error, revise only this section and call the same tool again.
- Do not reconstruct accepted sections from the transcript and do not claim finalization.`, in.Section, in.CandidateRevision, in.CandidateHash, toolName, limits)
}

func openAIMintConversationPhaseTool(section hostedgenesis.DeclarationSection) openai.ChatCompletionToolParam {
	name, _ := hostedgenesis.DeclarationToolForSection(section)
	return openai.ChatCompletionToolParam{Function: shared.FunctionDefinitionParam{
		Name: name, Description: openai.String("Submit the normalized current Hosted Genesis declaration section for immediate Host validation and checkpointing."),
		Parameters: shared.FunctionParameters(mintConversationPhaseToolSchema(section)), Strict: openai.Bool(true),
	}}
}

func anthropicMintConversationPhaseTool(section hostedgenesis.DeclarationSection) anthropic.ToolUnionParam {
	name, _ := hostedgenesis.DeclarationToolForSection(section)
	schema := mintConversationPhaseToolSchema(section)
	required, _ := schema["required"].([]string)
	return anthropic.ToolUnionParamOfTool(anthropic.ToolInputSchemaParam{
		Properties: schema["properties"], Required: required, ExtraFields: map[string]any{"additionalProperties": false},
	}, name)
}

func mintConversationPhaseToolSchema(section hostedgenesis.DeclarationSection) map[string]any {
	sectionSchema := fiveBodySectionSchema()
	bindingProperties := map[string]any{
		"candidateRevision": map[string]any{"type": "integer", "minimum": 0},
		"candidateHash":     map[string]any{"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"},
	}
	if section == hostedgenesis.DeclarationSectionSoul {
		bindingProperties["section"] = soulPhaseSectionSchema()
		bindingProperties["selfDescription"] = map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"purpose": map[string]any{"type": "string"}, "constraints": map[string]any{"type": "string"},
				"commitments": map[string]any{"type": "string"}, "limitations": map[string]any{"type": "string"},
				"authoredBy": map[string]any{"type": "string", "enum": []string{"agent"}}, "mintingModel": map[string]any{"type": "string"},
			}, "required": []string{"purpose", "constraints", "commitments", "limitations", "authoredBy", "mintingModel"},
		}
		bindingProperties["capabilities"] = producedCapabilitiesSchema()
		bindingProperties["transparency"] = declarationTransparencySchema()
		return map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": bindingProperties,
			"required":   []string{"candidateRevision", "candidateHash", "section", "selfDescription", "capabilities", "transparency"},
		}
	}
	bindingProperties["section"] = sectionSchema
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": bindingProperties, "required": []string{"candidateRevision", "candidateHash", "section"},
	}
}

func fiveBodySectionSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"summary": map[string]any{"type": "string", "maxLength": mintConversationPhaseSummaryMaxRunes},
			"notes":   fiveBodyNotesSchema(),
		},
		"required": []string{"summary", "notes"},
	}
}

func fiveBodyNotesSchema() map[string]any {
	return map[string]any{
		"type": "array", "minItems": 0, "maxItems": mintConversationPhaseNotesMaxItems,
		"items": map[string]any{"type": "string", "maxLength": mintConversationPhaseNoteMaxRunes},
	}
}

func soulPhaseSectionSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"summary": map[string]any{"type": "string", "maxLength": mintConversationPhaseSummaryMaxRunes}, "notes": fiveBodyNotesSchema(),
			"refusals": map[string]any{
				"type": "array", "minItems": mintConversationPhaseRefusalsMinItems, "maxItems": mintConversationPhaseRefusalsMaxItems,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"properties": map[string]any{
						"bypass": map[string]any{"type": "string", "maxLength": mintConversationPhaseRefusalFieldMaxRunes}, "invariant": map[string]any{"type": "string", "maxLength": mintConversationPhaseRefusalFieldMaxRunes},
						"closestSafePath": map[string]any{"type": "string", "maxLength": mintConversationPhaseRefusalFieldMaxRunes},
					}, "required": []string{"bypass", "invariant", "closestSafePath"},
				},
			},
		}, "required": []string{"summary", "notes", "refusals"},
	}
}

func producedCapabilitiesSchema() map[string]any {
	return map[string]any{
		"type": "array", "minItems": 0, "maxItems": hostedgenesis.MaxProducedCapabilities,
		"items": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"capability": map[string]any{"type": "string"}, "scope": map[string]any{"type": "string"},
				"claimLevel":    map[string]any{"type": "string", "enum": []string{"self-declared"}},
				"lastValidated": map[string]any{"type": "string"}, "validationRef": map[string]any{"type": "string"}, "degradesTo": map[string]any{"type": "string"},
			}, "required": []string{"capability", "scope", "claimLevel", "lastValidated", "validationRef", "degradesTo"},
		},
	}
}

func declarationTransparencySchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"modelProviderUncertainty": map[string]any{"type": "string"}, "operationalNotes": map[string]any{"type": "string"},
			"selfDeclaredNotice": map[string]any{"type": "string"},
		}, "required": []string{"modelProviderUncertainty", "operationalNotes", "selfDeclaredNotice"},
	}
}

func mergeAIUsage(total *models.AIUsage, delta models.AIUsage) {
	if total == nil {
		return
	}
	if total.Provider == "" {
		total.Provider = delta.Provider
	}
	if total.Model == "" {
		total.Model = delta.Model
	}
	total.InputTokens += delta.InputTokens
	total.OutputTokens += delta.OutputTokens
	total.TotalTokens += delta.TotalTokens
	total.DurationMs += delta.DurationMs
	total.ToolCalls += delta.ToolCalls
}

func emitMintConversationPhaseCompletion(recorder *providerTelemetryRecorder, content string, usage models.AIUsage, stopReason string) {
	bytes, runes, digest := providerOutputMetadata(content)
	recorder.emit(ProviderTelemetryEvent{
		EventType: "provider_call_completed", LastEvent: true, OutputBytes: bytes, OutputRunes: runes, OutputSHA256: digest, OutputCount: 1,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens, ToolCalls: usage.ToolCalls, StopReason: stopReason,
	})
}

func emitMintConversationToolValidation(recorder *providerTelemetryRecorder, toolName string, toolCallID string, result hostedgenesis.DeclarationToolResult) {
	_, callDigest := providerPayloadMetadata([]byte(strings.TrimSpace(toolCallID)))
	codes := make([]string, 0, len(result.Errors))
	paths := make([]string, 0, len(result.Errors))
	for _, validationIssue := range result.Errors {
		codes = append(codes, string(validationIssue.Code))
		paths = append(paths, validationIssue.Path)
	}
	recorder.emit(ProviderTelemetryEvent{
		EventType: "tool_validation_completed", ToolName: strings.TrimSpace(toolName), ToolCallHash: "sha256:" + callDigest,
		Accepted: result.Accepted, ValidationCodes: codes, ValidationPaths: paths,
	})
}
