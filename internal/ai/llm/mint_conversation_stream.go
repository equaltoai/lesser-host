package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const mintConversationOpenAIMaxCompletionTokens int64 = 4096

// StreamMintConversationOpenAI streams a chat completion from OpenAI, calling onDelta
// for each incremental content delta. Returns the full assistant response.
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
	model, err := openAIModelFromSet(modelSet)
	if err != nil {
		return "", models.AIUsage{}, err
	}
	recorder := newProviderTelemetryRecorder("openai", model, "assistant_stream", telemetry)
	recorder.emit(ProviderTelemetryEvent{EventType: "request_start"})

	client := openAIClientForKey(apiKey)
	start := time.Now()
	stream := client.Chat.Completions.NewStreaming(ctx, newOpenAIMintConversationStreamParams(model, systemPrompt, messages))

	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		chunk := stream.Current()
		_ = acc.AddChunk(chunk)
		event := ProviderTelemetryEvent{
			EventType:    "chat.completion.chunk",
			InputTokens:  chunk.Usage.PromptTokens,
			OutputTokens: chunk.Usage.CompletionTokens,
			TotalTokens:  chunk.Usage.TotalTokens,
		}
		var delta string
		if len(chunk.Choices) > 0 {
			delta = chunk.Choices[0].Delta.Content
			event.StopReason = strings.TrimSpace(chunk.Choices[0].FinishReason)
			event.ToolCalls = int64(len(chunk.Choices[0].Delta.ToolCalls))
		}
		event.DeltaBytes, event.DeltaRunes, _ = providerOutputMetadata(delta)
		if len(acc.Choices) > 0 {
			event.OutputBytes, event.OutputRunes, event.OutputSHA256 = providerOutputMetadata(acc.Choices[0].Message.Content)
			event.OutputCount = int64(len(acc.Choices))
		}
		recorder.emitSDK(event)
		if delta != "" && onDelta != nil {
			onDelta(delta)
		}
	}
	if err := stream.Err(); err != nil {
		failure := ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err)}
		if len(acc.Choices) > 0 {
			failure.OutputBytes, failure.OutputRunes, failure.OutputSHA256 = providerOutputMetadata(acc.Choices[0].Message.Content)
			failure.OutputCount = int64(len(acc.Choices))
		}
		recorder.emit(failure)
		return "", models.AIUsage{}, err
	}

	if len(acc.Choices) == 0 {
		err := fmt.Errorf("openai: empty choices")
		recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err)})
		return "", models.AIUsage{}, err
	}
	full := strings.TrimSpace(acc.Choices[0].Message.Content)
	if full == "" {
		err := fmt.Errorf("openai: empty response")
		recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err)})
		return "", models.AIUsage{}, err
	}

	usage := openAIUsageFromChat(&acc.ChatCompletion, start)
	bytes, runes, digest := providerOutputMetadata(full)
	stopReason := ""
	if len(acc.Choices) > 0 {
		stopReason = strings.TrimSpace(acc.Choices[0].FinishReason)
	}
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
		StopReason:   stopReason,
	})
	return full, usage, nil
}

func newOpenAIMintConversationStreamParams(model string, systemPrompt string, messages []MintConversationMessage) openai.ChatCompletionNewParams {
	return openai.ChatCompletionNewParams{
		Model: openai.ChatModel(model),
		Messages: buildOpenAIConversationMessages(
			systemPrompt,
			messages,
		),
		MaxCompletionTokens: openai.Int(mintConversationOpenAIMaxCompletionTokens),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
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
	model, err := anthropicModelFromSet(modelSet)
	if err != nil {
		return "", models.AIUsage{}, err
	}
	recorder := newProviderTelemetryRecorder("anthropic", string(model), "assistant_stream", telemetry)
	recorder.emit(ProviderTelemetryEvent{EventType: "request_start"})

	client := anthropicClientForKey(apiKey)
	start := time.Now()
	stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages:  buildAnthropicConversationMessages(messages),
	})
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

func buildOpenAIConversationMessages(systemPrompt string, messages []MintConversationMessage) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)

	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt != "" {
		out = append(out, openai.SystemMessage(systemPrompt))
	}

	for _, m := range messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		switch role {
		case "user":
			out = append(out, openai.UserMessage(content))
		case "assistant":
			out = append(out, openai.AssistantMessage(content))
		}
	}

	return out
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
		case "user":
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(content)))
		case "assistant":
			out = append(out, anthropic.NewAssistantMessage(anthropic.NewTextBlock(content)))
		}
	}
	return out
}
