package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/equaltoai/lesser-host/internal/ai/modelselection"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type openAIJSONSchemaBatchConfig struct {
	SchemaName        string
	SchemaDescription string
	Schema            map[string]any
	SystemPrompt      string
	Temperature       float64
	Telemetry         ProviderTelemetrySink
}

var openAIHTTPClient option.HTTPClient

func openAIModelFromSet(modelSet string) (string, error) {
	definition, err := modelselection.ResolveModelSetForProvider(modelSet, modelselection.ProviderOpenAI)
	if err != nil {
		return "", fmt.Errorf("unsupported openai model set %q: %w", strings.TrimSpace(modelSet), err)
	}
	return definition.ConcreteModel, nil
}

func openAIClientForKey(apiKey string) openai.Client {
	apiKey = strings.TrimSpace(apiKey)
	opts := []option.RequestOption{option.WithMaxRetries(DefaultProviderSDKRetryBudget)}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if openAIHTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(openAIHTTPClient))
	}
	return openai.NewClient(opts...)
}

func openAIContentFromChat(chat *openai.ChatCompletion) (string, error) {
	if chat == nil || len(chat.Choices) == 0 {
		return "", fmt.Errorf("openai: empty choices")
	}

	raw := strings.TrimSpace(chat.Choices[0].Message.Content)
	if raw == "" {
		return "", fmt.Errorf("openai: empty content")
	}

	return raw, nil
}

func openAIUsageFromChat(chat *openai.ChatCompletion, start time.Time) models.AIUsage {
	if chat == nil {
		return models.AIUsage{}
	}
	return models.AIUsage{
		Provider:     "openai",
		Model:        strings.TrimSpace(chat.Model),
		InputTokens:  chat.Usage.PromptTokens,
		OutputTokens: chat.Usage.CompletionTokens,
		TotalTokens:  chat.Usage.TotalTokens,
		DurationMs:   time.Since(start).Milliseconds(),
		ToolCalls:    1,
	}
}

func openAIJSONSchemaChatCompletion(
	ctx context.Context,
	apiKey string,
	model string,
	system string,
	payload []byte,
	schemaParam openai.ResponseFormatJSONSchemaJSONSchemaParam,
	temperature float64,
) (*openai.ChatCompletion, time.Time, error) {
	client := openAIClientForKey(apiKey)
	start := time.Now()
	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(system),
			openai.UserMessage(string(payload)),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{JSONSchema: schemaParam},
		},
	}
	if openAIModelSupportsTemperature(model) {
		params.Temperature = openai.Float(temperature)
	}
	chat, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, start, err
	}

	return chat, start, nil
}

func openAIModelSupportsTemperature(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(model, "gpt-5") {
		return false
	}
	for _, prefix := range []string{"o1", "o3", "o4"} {
		if model == prefix || strings.HasPrefix(model, prefix+"-") {
			return false
		}
	}
	return true
}

func openAIJSONSchemaBatch[Prompt any, Parsed any, Out any](
	ctx context.Context,
	apiKey string,
	modelSet string,
	prompt Prompt,
	cfg openAIJSONSchemaBatchConfig,
	parse func(string) (Parsed, error),
	normalize func(Parsed) Out,
) (Out, models.AIUsage, error) {
	var zero Out

	model, err := openAIModelFromSet(modelSet)
	if err != nil {
		return zero, models.AIUsage{}, err
	}
	recorder := newProviderTelemetryRecorder("openai", model, "json_text_batch", cfg.Telemetry)

	payload, err := json.Marshal(prompt)
	if err != nil {
		recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err)})
		return zero, models.AIUsage{}, err
	}
	payloadBytes, payloadHash := providerPayloadMetadata(payload)
	recorder.emit(ProviderTelemetryEvent{EventType: "request_start", PayloadBytes: payloadBytes, PayloadSHA256: payloadHash, SchemaName: cfg.SchemaName})

	schemaParam := openai.ResponseFormatJSONSchemaJSONSchemaParam{
		Name:        cfg.SchemaName,
		Description: openai.String(cfg.SchemaDescription),
		Schema:      cfg.Schema,
		Strict:      openai.Bool(true),
	}

	chat, start, err := openAIJSONSchemaChatCompletion(ctx, apiKey, model, cfg.SystemPrompt, payload, schemaParam, cfg.Temperature)
	if err != nil {
		recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err), PayloadBytes: payloadBytes, PayloadSHA256: payloadHash, SchemaName: cfg.SchemaName})
		return zero, models.AIUsage{}, err
	}
	usage := openAIUsageFromChat(chat, start)
	stopReason := ""
	toolCalls := int64(0)
	if len(chat.Choices) > 0 {
		stopReason = strings.TrimSpace(chat.Choices[0].FinishReason)
		toolCalls = int64(len(chat.Choices[0].Message.ToolCalls))
	}
	recorder.emit(ProviderTelemetryEvent{EventType: "response_received", InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens, ToolCalls: toolCalls, OutputCount: int64(len(chat.Choices)), StopReason: stopReason, SchemaName: cfg.SchemaName})

	raw, err := openAIContentFromChat(chat)
	if err != nil {
		err = withProviderFailureClass(err, string(hostedgenesis.FailureClassInvalidProviderOutput))
		recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err)})
		return zero, models.AIUsage{}, err
	}
	rawBytes, rawRunes, rawHash := providerOutputMetadata(raw)
	recorder.emit(ProviderTelemetryEvent{EventType: "schema_output_received", OutputBytes: rawBytes, OutputRunes: rawRunes, OutputSHA256: rawHash, OutputCount: 1, SchemaName: cfg.SchemaName})
	recorder.emit(ProviderTelemetryEvent{EventType: "parse_start", OutputBytes: rawBytes, OutputRunes: rawRunes, OutputSHA256: rawHash, SchemaName: cfg.SchemaName})

	parsed, err := parse(raw)
	if err != nil {
		err = withProviderFailureClass(err, string(hostedgenesis.FailureClassParseValidation))
		recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: ProviderFailureClass(err), OutputBytes: rawBytes, OutputRunes: rawRunes, OutputSHA256: rawHash, SchemaName: cfg.SchemaName})
		return zero, models.AIUsage{}, err
	}
	recorder.emit(ProviderTelemetryEvent{EventType: "parse_completed", OutputBytes: rawBytes, OutputRunes: rawRunes, OutputSHA256: rawHash, SchemaName: cfg.SchemaName})

	recorder.emit(ProviderTelemetryEvent{EventType: "validation_start", OutputBytes: rawBytes, OutputRunes: rawRunes, OutputSHA256: rawHash, SchemaName: cfg.SchemaName})
	out := normalize(parsed)
	recorder.emit(ProviderTelemetryEvent{EventType: "validation_completed", OutputBytes: rawBytes, OutputRunes: rawRunes, OutputSHA256: rawHash, SchemaName: cfg.SchemaName})
	recorder.emit(ProviderTelemetryEvent{EventType: "provider_call_completed", LastEvent: true, OutputBytes: rawBytes, OutputRunes: rawRunes, OutputSHA256: rawHash, OutputCount: 1, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens, ToolCalls: maxInt64(1, toolCalls), StopReason: stopReason, SchemaName: cfg.SchemaName})
	return out, usage, nil
}
