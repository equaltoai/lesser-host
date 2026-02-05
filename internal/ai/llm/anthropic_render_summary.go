package llm

import (
	"context"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// RenderSummaryBatchAnthropic generates summaries for a batch of render artifacts using Anthropic.
func RenderSummaryBatchAnthropic(ctx context.Context, apiKey string, modelSet string, items []RenderSummaryBatchItem) (map[string]ai.RenderSummaryResultV1, models.AIUsage, error) {
	if len(items) == 0 {
		return map[string]ai.RenderSummaryResultV1{}, models.AIUsage{}, nil
	}

	prompt := renderSummaryPrompt{Items: buildRenderSummaryPromptItems(items)}
	if len(prompt.Items) == 0 {
		return map[string]ai.RenderSummaryResultV1{}, models.AIUsage{}, nil
	}

	return anthropicToolBatch(
		ctx,
		apiKey,
		modelSet,
		prompt,
		anthropicToolBatchConfig{
			ToolName:        "render_summary_batch",
			ToolDescription: "Return batch render summaries for the provided items.",
			Schema:          renderSummaryJSONSchemaV1(),
			SystemPrompt:    renderSummarySystemPromptV1(),
			Temperature:     0.2,
			MaxTokens:       4096,
		},
		parseRenderSummaryBatchOutput,
		normalizeRenderSummaryBatchOutput,
	)
}
