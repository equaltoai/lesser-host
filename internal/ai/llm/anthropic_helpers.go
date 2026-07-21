package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
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

type anthropicJSONTextBatchConfig struct {
	Schema       map[string]any
	SystemPrompt string
	Temperature  float64
	MaxTokens    int64
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
	schema = sanitizeAnthropicToolSchemaMap(schema)

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

const anthropicSchemaTypeObject = "object"

// anthropicUnsupportedSchemaKeywords maps a JSON-schema node type to constraint
// keywords Anthropic strict custom tools reject with a 400 (e.g. "For 'array'
// type, property 'maxItems' is not supported"). Stripping them only relaxes
// provider-side validation: Host re-enforces these limits locally after the
// provider responds (normalizeMintConversationDeclarationsDraft, hostedgenesis
// five-body normalization/validation).
var anthropicUnsupportedSchemaKeywords = map[string][]string{
	"array":   {"minItems", "maxItems", "uniqueItems", "contains", "minContains", "maxContains"},
	"string":  {"minLength", "maxLength"},
	"number":  {"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"},
	"integer": {"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"},
}

// Anthropic documents simple {n,m} regex quantifiers as supported but complex
// quantifiers with large ranges as unsupported, without publishing a numeric
// cutoff. Do not guess that cutoff: strip any ranged-quantifier pattern from
// the provider schema and rely on the field description plus Host's original
// post-response validation. Fixed quantifiers such as RFC3339's {4} and {2}
// remain in the strict tool schema.
var anthropicRangedRegexQuantifier = regexp.MustCompile(`\{[0-9]+,[0-9]+\}`)

func anthropicSchemaKeywordUnsupported(nodeType, keyword string) bool {
	for _, unsupported := range anthropicUnsupportedSchemaKeywords[nodeType] {
		if keyword == unsupported {
			return true
		}
	}
	return false
}

func sanitizeAnthropicToolSchemaMap(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return nil
	}
	out := make(map[string]any, len(schema))
	rawType, _ := schema["type"].(string)
	nodeType := strings.ToLower(strings.TrimSpace(rawType))
	for k, v := range schema {
		if k == "additionalProperties" && nodeType == anthropicSchemaTypeObject {
			continue
		}
		if anthropicSchemaKeywordUnsupported(nodeType, k) {
			continue
		}
		if nodeType == "string" && k == "pattern" {
			if pattern, ok := v.(string); ok && anthropicRangedRegexQuantifier.MatchString(pattern) {
				continue
			}
		}
		out[k] = sanitizeAnthropicToolSchemaValue(v)
	}
	if nodeType == anthropicSchemaTypeObject {
		if _, ok := out["properties"]; !ok {
			out["properties"] = map[string]any{}
		}
		out["additionalProperties"] = false
	}
	return out
}

func sanitizeAnthropicToolSchemaValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return sanitizeAnthropicToolSchemaMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = sanitizeAnthropicToolSchemaValue(typed[i])
		}
		return out
	default:
		return v
	}
}

func anthropicValidateStopReason(message *anthropic.Message, allowed ...anthropic.StopReason) error {
	if message == nil {
		return fmt.Errorf("anthropic: nil message")
	}
	stopReason := message.StopReason
	switch stopReason {
	case "":
		return fmt.Errorf("anthropic: missing stop_reason")
	case anthropic.StopReasonMaxTokens, anthropic.StopReason("model_context_window_exceeded"):
		return fmt.Errorf("anthropic: response truncated: stop_reason=%s", stopReason)
	case anthropic.StopReasonPauseTurn:
		return fmt.Errorf("anthropic: response incomplete: stop_reason=%s", stopReason)
	case anthropic.StopReasonRefusal:
		return fmt.Errorf("anthropic: response refused: stop_reason=%s", stopReason)
	}
	for _, okReason := range allowed {
		if stopReason == okReason {
			return nil
		}
	}
	return fmt.Errorf("anthropic: unexpected stop_reason=%s", stopReason)
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

func anthropicTextOutput(message *anthropic.Message) (string, error) {
	if message == nil {
		return "", fmt.Errorf("anthropic: nil message")
	}

	var sb strings.Builder
	for _, block := range message.Content {
		switch block := block.AsAny().(type) {
		case anthropic.TextBlock:
			sb.WriteString(block.Text)
		}
	}

	raw := strings.TrimSpace(sb.String())
	if raw == "" {
		return "", fmt.Errorf("anthropic: empty response")
	}
	return raw, nil
}

func extractJSONObjectFromText(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "```") {
		trimmed := strings.TrimPrefix(raw, "```json")
		if trimmed == raw {
			trimmed = strings.TrimPrefix(raw, "```")
		}
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
		raw = strings.TrimSpace(trimmed)
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end >= start {
		return strings.TrimSpace(raw[start : end+1])
	}
	return raw
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
	if stopErr := anthropicValidateStopReason(message, anthropic.StopReasonToolUse); stopErr != nil {
		return zero, models.AIUsage{}, stopErr
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

func anthropicJSONTextBatch[Prompt any, Parsed any, Out any](
	ctx context.Context,
	apiKey string,
	modelSet string,
	prompt Prompt,
	cfg anthropicJSONTextBatchConfig,
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

	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 4096
	}

	systemPrompt := strings.TrimSpace(cfg.SystemPrompt)
	if len(cfg.Schema) > 0 {
		schemaJSON, schemaErr := json.Marshal(cfg.Schema)
		if schemaErr != nil {
			return zero, models.AIUsage{}, schemaErr
		}
		systemPrompt = strings.TrimSpace(systemPrompt + "\n\nReturn only valid JSON matching this schema exactly:\n" + string(schemaJSON))
	}

	client := anthropicClientForKey(apiKey)
	start := time.Now()
	message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:       model,
		MaxTokens:   cfg.MaxTokens,
		System:      []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages:    []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(string(payload)))},
		Temperature: anthropic.Float(cfg.Temperature),
	})
	if err != nil {
		return zero, models.AIUsage{}, err
	}
	if stopErr := anthropicValidateStopReason(message, anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence); stopErr != nil {
		return zero, models.AIUsage{}, stopErr
	}

	raw, err := anthropicTextOutput(message)
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	parsed, err := parse(extractJSONObjectFromText(raw))
	if err != nil {
		return zero, models.AIUsage{}, err
	}

	out := normalize(parsed)
	return out, anthropicUsageFromMessage(message, start), nil
}
