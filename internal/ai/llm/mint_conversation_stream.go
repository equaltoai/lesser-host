package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"

	"github.com/equaltoai/lesser-host/internal/ai/modelselection"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// MintConversationMessage is the provider-neutral bounded transcript element
// consumed by the phase-local Hosted Genesis tool loop.
type MintConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

const mintConversationOpenAIMaxCompletionTokens int64 = 4096

// StreamMintConversationOpenAI streams a Responses API response from OpenAI,
// calling onDelta for each incremental content delta. Returns the full assistant
// response.
func StreamMintConversationOpenAI(
	ctx context.Context,
	apiKey string,
	modelSet string,
	systemPrompt string,
	messages []MintConversationMessage,
	onDelta func(string),
) (string, models.AIUsage, error) {
	return StreamMintConversationOpenAIWithTelemetry(ctx, apiKey, modelSet, systemPrompt, messages, onDelta, nil)
}

// StreamMintConversationOpenAIWithTelemetry is StreamMintConversationOpenAI
// plus content-free, per-SDK-event telemetry.
func StreamMintConversationOpenAIWithTelemetry(
	ctx context.Context,
	apiKey string,
	modelSet string,
	systemPrompt string,
	messages []MintConversationMessage,
	onDelta func(string),
	telemetry ProviderTelemetrySink,
) (string, models.AIUsage, error) {
	definition, err := modelselection.ResolveModelSetForProvider(modelSet, modelselection.ProviderOpenAI)
	if err != nil {
		return "", models.AIUsage{}, err
	}
	model := definition.ConcreteModel
	recorder := newProviderTelemetryRecorder("openai", model, "assistant_stream", telemetry)
	recorder.emit(ProviderTelemetryEvent{EventType: "request_start"})

	client := openAIClientForKey(apiKey)
	start := time.Now()
	stream := client.Responses.NewStreaming(ctx, newOpenAIMintConversationStreamParamsWithEffort(model, definition.ReasoningEffort, systemPrompt, messages))
	defer stream.Close()

	state := openAIStreamTelemetryState{}
	for stream.Next() {
		event, delta := state.observeSDKEvent(stream.Current())
		recorder.emitSDK(event)
		if delta != "" && onDelta != nil {
			onDelta(delta)
		}
	}
	if err := stream.Err(); err != nil {
		failure := ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err)}
		if state.full.Len() > 0 {
			failure.OutputBytes, failure.OutputRunes, failure.OutputSHA256 = providerOutputMetadata(state.full.String())
			failure.OutputCount = 1
		}
		recorder.emit(failure)
		return "", models.AIUsage{}, err
	}

	if state.full.Len() == 0 {
		err := fmt.Errorf("openai: empty response")
		recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err)})
		return "", models.AIUsage{}, err
	}
	content := strings.TrimSpace(state.full.String())
	if content == "" {
		err := fmt.Errorf("openai: empty response")
		recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err)})
		return "", models.AIUsage{}, err
	}

	usage := models.AIUsage{Provider: modelselection.ProviderOpenAI, Model: model, DurationMs: time.Since(start).Milliseconds(), ToolCalls: maxInt64(1, state.toolCalls)}
	if state.completedResponse != nil {
		usage = openAIUsageFromResponse(state.completedResponse, start)
	}
	bytes, runes, digest := providerOutputMetadata(content)
	recorder.emit(ProviderTelemetryEvent{
		EventType:    "provider_call_completed",
		LastEvent:    true,
		OutputBytes:  bytes,
		OutputRunes:  runes,
		OutputSHA256: digest,
		OutputCount:  1,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
		ToolCalls:    usage.ToolCalls,
		StopReason:   state.stopReason,
	})
	return content, usage, nil
}

func newOpenAIMintConversationStreamParamsWithEffort(model string, reasoningEffort string, systemPrompt string, messages []MintConversationMessage) responses.ResponseNewParams {
	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(model),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: buildOpenAIResponseInput(systemPrompt, messages)},
		MaxOutputTokens: openai.Int(mintConversationOpenAIMaxCompletionTokens),
	}
	if reasoningEffort == modelselection.ReasoningEffortMedium {
		params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffortMedium}
	}
	return params
}

type openAIStreamTelemetryState struct {
	full              strings.Builder
	completedResponse *responses.Response
	toolCalls         int64
	stopReason        string
}

func (s *openAIStreamTelemetryState) observeSDKEvent(event responses.ResponseStreamEventUnion) (ProviderTelemetryEvent, string) {
	observation := ProviderTelemetryEvent{EventType: strings.TrimSpace(event.Type)}
	delta := ""
	switch value := event.AsAny().(type) {
	case responses.ResponseTextDeltaEvent:
		delta = value.Delta
	case responses.ResponseTextDoneEvent:
		if s.full.Len() == 0 {
			delta = value.Text
		}
	case responses.ResponseOutputItemDoneEvent:
		if value.Item.Type == openAIResponseFunctionCallType {
			s.toolCalls++
		}
	case responses.ResponseCompletedEvent:
		s.observeTerminalResponse(value.Response, &delta)
	case responses.ResponseIncompleteEvent:
		s.observeTerminalResponse(value.Response, &delta)
	}
	if delta != "" {
		s.full.WriteString(delta)
	}
	observation.StopReason = s.stopReason
	observation.ToolCalls = s.toolCalls
	observation.DeltaBytes, observation.DeltaRunes, _ = providerOutputMetadata(delta)
	observation.OutputBytes, observation.OutputRunes, observation.OutputSHA256 = providerOutputMetadata(s.full.String())
	if observation.OutputBytes > 0 {
		observation.OutputCount = 1
	}
	if s.completedResponse != nil {
		observation.InputTokens = s.completedResponse.Usage.InputTokens
		observation.OutputTokens = s.completedResponse.Usage.OutputTokens
		observation.TotalTokens = s.completedResponse.Usage.TotalTokens
	}
	return observation, delta
}

func (s *openAIStreamTelemetryState) observeTerminalResponse(response responses.Response, delta *string) {
	s.completedResponse = &response
	s.stopReason = openAIResponseStopReason(&response)
	if s.full.Len() == 0 {
		*delta = response.OutputText()
	}
}

// StreamMintConversationAnthropic streams a chat completion from Anthropic, calling onDelta
// for each incremental content delta. Returns the full assistant response.
func StreamMintConversationAnthropic(
	ctx context.Context,
	apiKey string,
	modelSet string,
	systemPrompt string,
	messages []MintConversationMessage,
	onDelta func(string),
) (string, models.AIUsage, error) {
	return StreamMintConversationAnthropicWithTelemetry(ctx, apiKey, modelSet, systemPrompt, messages, onDelta, nil)
}

// StreamMintConversationAnthropicWithTelemetry is
// StreamMintConversationAnthropic plus content-free, per-SDK-event telemetry.
func StreamMintConversationAnthropicWithTelemetry(
	ctx context.Context,
	apiKey string,
	modelSet string,
	systemPrompt string,
	messages []MintConversationMessage,
	onDelta func(string),
	telemetry ProviderTelemetrySink,
) (string, models.AIUsage, error) {
	definition, err := modelselection.ResolveModelSetForProvider(modelSet, modelselection.ProviderAnthropic)
	if err != nil {
		return "", models.AIUsage{}, err
	}
	model := anthropic.Model(definition.ConcreteModel)
	recorder := newProviderTelemetryRecorder("anthropic", string(model), "assistant_stream", telemetry)
	recorder.emit(ProviderTelemetryEvent{EventType: "request_start"})

	client := anthropicClientForKey(apiKey)
	start := time.Now()
	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages:  buildAnthropicConversationMessages(messages),
	}
	if definition.ReasoningEffort == modelselection.ReasoningEffortMedium {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortMedium}
	}
	stream := client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	state := anthropicStreamTelemetryState{}

	for stream.Next() {
		recorder.emitSDK(state.observeSDKEvent(stream.Current(), onDelta))
	}
	if stream.Err() != nil {
		err := stream.Err()
		bytes, runes, digest := providerOutputMetadata(state.full.String())
		failure := ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err), OutputBytes: bytes, OutputRunes: runes, OutputSHA256: digest}
		if bytes > 0 {
			failure.OutputCount = 1
		}
		recorder.emit(failure)
		return "", models.AIUsage{}, err
	}

	out := strings.TrimSpace(state.full.String())
	if out == "" {
		err := fmt.Errorf("anthropic: empty response")
		recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err)})
		return "", models.AIUsage{}, err
	}

	if state.modelName == "" {
		state.modelName = strings.TrimSpace(string(model))
	}
	usage := models.AIUsage{
		Provider:     "anthropic",
		Model:        state.modelName,
		DurationMs:   time.Since(start).Milliseconds(),
		ToolCalls:    maxInt64(1, state.toolCalls),
		InputTokens:  state.inputTokens,
		OutputTokens: state.outputTokens,
		TotalTokens:  state.inputTokens + state.outputTokens,
	}
	bytes, runes, digest := providerOutputMetadata(out)
	recorder.emit(ProviderTelemetryEvent{
		EventType:    "provider_call_completed",
		LastEvent:    true,
		OutputBytes:  bytes,
		OutputRunes:  runes,
		OutputSHA256: digest,
		OutputCount:  1,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
		ToolCalls:    usage.ToolCalls,
		StopReason:   state.stopReason,
	})
	return out, usage, nil
}

type anthropicStreamTelemetryState struct {
	full         strings.Builder
	modelName    string
	inputTokens  int64
	outputTokens int64
	stopReason   string
	toolCalls    int64
}

func (s *anthropicStreamTelemetryState) observeSDKEvent(event anthropic.MessageStreamEventUnion, onDelta func(string)) ProviderTelemetryEvent {
	observation := ProviderTelemetryEvent{EventType: strings.TrimSpace(event.Type)}
	switch delta := event.AsAny().(type) {
	case anthropic.MessageStartEvent:
		s.modelName = strings.TrimSpace(string(delta.Message.Model))
		s.inputTokens = delta.Message.Usage.InputTokens + delta.Message.Usage.CacheCreationInputTokens + delta.Message.Usage.CacheReadInputTokens
		s.outputTokens = delta.Message.Usage.OutputTokens
	case anthropic.MessageDeltaEvent:
		// message_delta usage is cumulative for the stream; keep the latest snapshot.
		s.inputTokens = delta.Usage.InputTokens + delta.Usage.CacheCreationInputTokens + delta.Usage.CacheReadInputTokens
		s.outputTokens = delta.Usage.OutputTokens
		s.stopReason = strings.TrimSpace(string(delta.Delta.StopReason))
		observation.StopReason = s.stopReason
	case anthropic.ContentBlockStartEvent:
		if delta.ContentBlock.Type == "tool_use" || delta.ContentBlock.Type == "server_tool_use" {
			s.toolCalls++
		}
	case anthropic.ContentBlockDeltaEvent:
		textDelta := delta.Delta.AsTextDelta()
		observation.DeltaBytes, observation.DeltaRunes, _ = providerOutputMetadata(textDelta.Text)
		if textDelta.Text != "" {
			s.full.WriteString(textDelta.Text)
		}
		if textDelta.Text != "" && onDelta != nil {
			onDelta(textDelta.Text)
		}
	}
	observation.InputTokens = s.inputTokens
	observation.OutputTokens = s.outputTokens
	observation.TotalTokens = s.inputTokens + s.outputTokens
	observation.ToolCalls = s.toolCalls
	observation.OutputBytes, observation.OutputRunes, observation.OutputSHA256 = providerOutputMetadata(s.full.String())
	if observation.OutputBytes > 0 {
		observation.OutputCount = 1
	}
	return observation
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func buildAnthropicConversationMessages(messages []MintConversationMessage) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(messages))
	for _, m := range messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		switch role {
		case mintConversationUserRole:
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(content)))
		case "assistant":
			out = append(out, anthropic.NewAssistantMessage(anthropic.NewTextBlock(content)))
		}
	}
	return out
}
