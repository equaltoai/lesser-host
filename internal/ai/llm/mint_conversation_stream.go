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
	model, err := openAIModelFromSet(modelSet)
	if err != nil {
		return "", models.AIUsage{}, err
	}

	client := openAIClientForKey(apiKey)
	start := time.Now()
	stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model: openai.ChatModel(model),
		Messages: buildOpenAIConversationMessages(
			systemPrompt,
			messages,
		),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	})

	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		chunk := stream.Current()
		_ = acc.AddChunk(chunk)
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		if onDelta != nil {
			onDelta(delta)
		}
	}
	if err := stream.Err(); err != nil {
		return "", models.AIUsage{}, err
	}

	full := strings.TrimSpace(acc.Choices[0].Message.Content)
	if full == "" {
		return "", models.AIUsage{}, fmt.Errorf("openai: empty response")
	}

	usage := openAIUsageFromChat(&acc.ChatCompletion, start)
	return full, usage, nil
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
	model, err := anthropicModelFromSet(modelSet)
	if err != nil {
		return "", models.AIUsage{}, err
	}

	client := anthropicClientForKey(apiKey)
	start := time.Now()
	stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages:  buildAnthropicConversationMessages(messages),
	})
	defer stream.Close()

	var full strings.Builder

	for stream.Next() {
		event := stream.Current()
		switch delta := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			textDelta := delta.Delta.AsTextDelta()
			if textDelta.Text == "" {
				continue
			}
			full.WriteString(textDelta.Text)
			if onDelta != nil {
				onDelta(textDelta.Text)
			}
		}
	}
	if stream.Err() != nil {
		return "", models.AIUsage{}, stream.Err()
	}

	out := strings.TrimSpace(full.String())
	if out == "" {
		return "", models.AIUsage{}, fmt.Errorf("anthropic: empty response")
	}

	usage := models.AIUsage{
		Provider:    "anthropic",
		Model:       strings.TrimSpace(string(model)),
		DurationMs:  time.Since(start).Milliseconds(),
		ToolCalls:   1,
		InputTokens: 0,
	}
	return out, usage, nil
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
