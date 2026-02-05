package llm

import (
	"context"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// ClaimVerifyBatchAnthropic verifies claims for a batch of inputs using Anthropic.
func ClaimVerifyBatchAnthropic(ctx context.Context, apiKey string, modelSet string, items []ClaimVerifyBatchItem) (map[string]ai.ClaimVerifyResultV1, models.AIUsage, error) {
	if len(items) == 0 {
		return map[string]ai.ClaimVerifyResultV1{}, models.AIUsage{}, nil
	}

	prompt := claimVerifyPrompt{Items: buildClaimVerifyPromptItems(items)}
	if len(prompt.Items) == 0 {
		return map[string]ai.ClaimVerifyResultV1{}, models.AIUsage{}, nil
	}

	return anthropicToolBatch(
		ctx,
		apiKey,
		modelSet,
		prompt,
		anthropicToolBatchConfig{
			ToolName:        "claim_verify_batch",
			ToolDescription: "Return claim verification results with citations for the provided items.",
			Schema:          claimVerifyJSONSchemaV1(),
			SystemPrompt:    claimVerifySystemPromptV1(),
			Temperature:     0.2,
			MaxTokens:       4096,
		},
		parseClaimVerifyBatchOutput,
		normalizeClaimVerifyBatchOutput,
	)
}
