package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	OpenAIServiceSSMParameterName = "/lesser-host/api/openai/service"
	ClaudeSSMParameterName        = "/lesser-host/api/claude"
)

func OpenAIServiceKey(ctx context.Context, client SSMAPI) (string, error) {
	raw, err := GetSSMParameterCached(ctx, client, OpenAIServiceSSMParameterName, 10*time.Minute)
	if err != nil {
		return "", err
	}
	return parseAPIKeyValue(raw)
}

func ClaudeAPIKey(ctx context.Context, client SSMAPI) (string, error) {
	raw, err := GetSSMParameterCached(ctx, client, ClaudeSSMParameterName, 10*time.Minute)
	if err != nil {
		return "", err
	}
	return parseAPIKeyValue(raw)
}

func parseAPIKeyValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("api key is empty")
	}

	if strings.HasPrefix(raw, "{") && strings.HasSuffix(raw, "}") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err == nil {
			for _, key := range []string{"api_key", "apiKey", "key", "token", "value"} {
				if v, ok := obj[key]; ok {
					if s, ok := v.(string); ok {
						s = strings.TrimSpace(s)
						if s != "" {
							return s, nil
						}
					}
				}
			}
		}
	}

	return raw, nil
}
