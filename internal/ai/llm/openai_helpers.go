package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type openAIJSONSchemaBatchConfig struct {
	SchemaName        string
	SchemaDescription string
	Schema            map[string]any
	SystemPrompt      string
	Temperature       float64
}

func openAIModelFromSet(modelSet string) (string, error) {
	modelSet = strings.TrimSpace(modelSet)
	if !strings.HasPrefix(strings.ToLower(modelSet), "openai:") {
		return "", fmt.Errorf("unsupported openai model set %q", modelSet)
	}

	model := strings.TrimSpace(strings.TrimPrefix(modelSet, "openai:"))
	if model == "" {
		return "", fmt.Errorf("openai model is required")
	}

	return model, nil
}

func openAIClientForKey(apiKey string) openai.Client {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		return openai.NewClient(option.WithAPIKey(apiKey))
	}
	return openai.NewClient()
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
	chat, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: openai.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(system),
			openai.UserMessage(string(payload)),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{JSONSchema: schemaParam},
		},
		Temperature: openai.Float(temperature),
	})
	if err != nil {
		return nil, start, err
	}

	return chat, start, nil
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

	payload, err := json.Marshal(prompt)
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	schemaParam := openai.ResponseFormatJSONSchemaJSONSchemaParam{
		Name:        cfg.SchemaName,
		Description: openai.String(cfg.SchemaDescription),
		Schema:      cfg.Schema,
		Strict:      openai.Bool(true),
	}

	chat, start, err := openAIJSONSchemaChatCompletion(ctx, apiKey, model, cfg.SystemPrompt, payload, schemaParam, cfg.Temperature)
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	raw, err := openAIContentFromChat(chat)
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	parsed, err := parse(raw)
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	out := normalize(parsed)
	return out, openAIUsageFromChat(chat, start), nil
}
