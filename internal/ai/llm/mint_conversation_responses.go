package llm

import (
	"strings"

	"github.com/openai/openai-go/responses"
)

const (
	openAIResponseFunctionCallType = "function_call"
	mintConversationUserRole       = "user"
)

func buildOpenAIResponseInput(systemPrompt string, messages []MintConversationMessage) responses.ResponseInputParam {
	out := make(responses.ResponseInputParam, 0, len(messages)+1)

	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt != "" {
		out = append(out, responses.ResponseInputItemParamOfMessage(systemPrompt, responses.EasyInputMessageRoleSystem))
	}

	for _, m := range messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		switch role {
		case mintConversationUserRole:
			out = append(out, responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleUser))
		case "assistant":
			out = append(out, responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleAssistant))
		}
	}

	return out
}
