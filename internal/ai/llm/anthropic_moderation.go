package llm

import (
	"context"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// ModerationTextBatchAnthropic runs moderation for a batch of text inputs using Anthropic.
func ModerationTextBatchAnthropic(ctx context.Context, apiKey string, modelSet string, items []ModerationTextBatchItem) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
	prompt := moderationPrompt{Items: buildModerationTextPromptItems(items)}
	return moderationBatchAnthropic(ctx, apiKey, modelSet, "moderation_text", prompt)
}

// ModerationImageBatchAnthropic runs moderation for a batch of image inputs using Anthropic.
func ModerationImageBatchAnthropic(ctx context.Context, apiKey string, modelSet string, items []ModerationImageBatchItem) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
	prompt := moderationPrompt{Items: buildModerationImagePromptItems(items)}
	return moderationBatchAnthropic(ctx, apiKey, modelSet, "moderation_image", prompt)
}

func moderationBatchAnthropic(ctx context.Context, apiKey string, modelSet string, kind string, prompt moderationPrompt) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
	if len(prompt.Items) == 0 {
		return map[string]ai.ModerationResultV1{}, models.AIUsage{}, nil
	}

	parsed, usage, err := anthropicToolBatch(
		ctx,
		apiKey,
		modelSet,
		prompt,
		anthropicToolBatchConfig{
			ToolName:        "moderation_batch",
			ToolDescription: "Return batch moderation decisions for the provided items.",
			Schema:          moderationJSONSchemaV1(),
			SystemPrompt:    moderationSystemPromptV1(),
			Temperature:     0.2,
			MaxTokens:       2048,
		},
		parseModerationBatchOutput,
		func(v moderationBatchOutput) moderationBatchOutput { return v },
	)
	if err != nil {
		return nil, models.AIUsage{}, err
	}

	return normalizeModerationBatchOutput(parsed, kind), usage, nil
}
