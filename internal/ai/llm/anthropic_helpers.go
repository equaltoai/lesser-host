package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	anthropicconstant "github.com/anthropics/anthropic-sdk-go/shared/constant"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type anthropicToolBatchConfig struct {
	ToolName        string
	ToolDescription string
	Schema          map[string]any
	SystemPrompt    string
	Temperature     float64
	MaxTokens       int64
}

var anthropicHTTPClient option.HTTPClient

func anthropicModelFromSet(modelSet string) (anthropic.Model, error) {
	modelSet = strings.TrimSpace(modelSet)
	if !strings.HasPrefix(strings.ToLower(modelSet), "anthropic:") {
		return "", fmt.Errorf("unsupported anthropic model set %q", modelSet)
	}

	model := strings.TrimSpace(strings.TrimPrefix(modelSet, "anthropic:"))
	if model == "" {
		return "", fmt.Errorf("anthropic model is required")
	}

	return anthropic.Model(model), nil
}

func anthropicClientForKey(apiKey string) anthropic.Client {
	apiKey = strings.TrimSpace(apiKey)
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if baseURL := strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL")); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if anthropicHTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(anthropicHTTPClient))
	}
	return anthropic.NewClient(opts...)
}

func anthropicToolInputSchemaFromJSONSchema(schema map[string]any) anthropic.ToolInputSchemaParam {
	out := anthropic.ToolInputSchemaParam{
		Type: anthropicconstant.ValueOf[anthropicconstant.Object](),
	}
	if schema == nil {
		return out
	}

	if props, ok := schema["properties"]; ok {
		out.Properties = props
	}

	switch req := schema["required"].(type) {
	case []string:
		out.Required = append([]string(nil), req...)
	case []any:
		out.Required = make([]string, 0, len(req))
		for _, it := range req {
			s, ok := it.(string)
			if !ok {
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out.Required = append(out.Required, s)
		}
	}

	extra := map[string]any{}
	for k, v := range schema {
		switch k {
		case "properties", "required", "type":
			continue
		default:
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		out.ExtraFields = extra
	}

	return out
}

func anthropicToolUseInput(message *anthropic.Message, toolName string) (json.RawMessage, error) {
	if message == nil {
		return nil, fmt.Errorf("anthropic: nil message")
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return nil, fmt.Errorf("anthropic: tool name is required")
	}

	for _, block := range message.Content {
		switch block := block.AsAny().(type) {
		case anthropic.ToolUseBlock:
			if strings.TrimSpace(block.Name) != toolName {
				continue
			}
			raw := json.RawMessage(block.Input)
			if len(raw) == 0 {
				return nil, fmt.Errorf("anthropic: empty tool input")
			}
			return raw, nil
		}
	}

	return nil, fmt.Errorf("anthropic: missing tool output")
}

func anthropicUsageFromMessage(message *anthropic.Message, start time.Time) models.AIUsage {
	if message == nil {
		return models.AIUsage{}
	}

	inputTokens := message.Usage.InputTokens + message.Usage.CacheCreationInputTokens + message.Usage.CacheReadInputTokens
	outputTokens := message.Usage.OutputTokens
	totalTokens := inputTokens + outputTokens

	return models.AIUsage{
		Provider:     "anthropic",
		Model:        strings.TrimSpace(string(message.Model)),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
		DurationMs:   time.Since(start).Milliseconds(),
		ToolCalls:    1,
	}
}

func anthropicToolBatch[Prompt any, Parsed any, Out any](
	ctx context.Context,
	apiKey string,
	modelSet string,
	prompt Prompt,
	cfg anthropicToolBatchConfig,
	parse func(string) (Parsed, error),
	normalize func(Parsed) Out,
) (Out, models.AIUsage, error) {
	var zero Out

	model, err := anthropicModelFromSet(modelSet)
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	payload, err := json.Marshal(prompt)
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	toolName := strings.TrimSpace(cfg.ToolName)
	if toolName == "" {
		return zero, models.AIUsage{}, fmt.Errorf("anthropic: tool name is required")
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 2048
	}

	tool := anthropic.ToolParam{
		Name:        toolName,
		Description: anthropic.String(strings.TrimSpace(cfg.ToolDescription)),
		InputSchema: anthropicToolInputSchemaFromJSONSchema(cfg.Schema),
		Strict:      anthropic.Bool(true),
		Type:        anthropic.ToolTypeCustom,
	}

	client := anthropicClientForKey(apiKey)
	start := time.Now()
	message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: cfg.MaxTokens,
		System:    []anthropic.TextBlockParam{{Text: cfg.SystemPrompt}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(string(payload)))},
		Tools:     []anthropic.ToolUnionParam{{OfTool: &tool}},
		ToolChoice: anthropic.ToolChoiceUnionParam{
			OfTool: &anthropic.ToolChoiceToolParam{Name: toolName},
		},
		Temperature: anthropic.Float(cfg.Temperature),
	})
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	raw, err := anthropicToolUseInput(message, toolName)
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	parsed, err := parse(string(raw))
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	out := normalize(parsed)
	return out, anthropicUsageFromMessage(message, start), nil
}
