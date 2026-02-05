package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// OpenAIServiceSSMParameterName and ClaudeSSMParameterName are SSM parameter paths for provider API keys.
const (
	OpenAIServiceSSMParameterName = "/lesser-host/api/openai/service"
	ClaudeSSMParameterName        = "/lesser-host/api/claude"
	// #nosec G101 -- SSM parameter path, not a hardcoded credential.
	StripeSecretKeySSMParameterName = "/lesser-host/api/stripe/secret"
	// #nosec G101 -- SSM parameter path, not a hardcoded credential.
	StripeWebhookSecretSSMParameterName = "/lesser-host/api/stripe/webhook"
)

// OpenAIServiceKey loads the OpenAI service API key from SSM.
func OpenAIServiceKey(ctx context.Context, client SSMAPI) (string, error) {
	raw, err := GetSSMParameterCached(ctx, client, OpenAIServiceSSMParameterName, 10*time.Minute)
	if err != nil {
		return "", err
	}
	return parseAPIKeyValue(raw)
}

// ClaudeAPIKey loads the Claude API key from SSM.
func ClaudeAPIKey(ctx context.Context, client SSMAPI) (string, error) {
	raw, err := GetSSMParameterCached(ctx, client, ClaudeSSMParameterName, 10*time.Minute)
	if err != nil {
		return "", err
	}
	return parseAPIKeyValue(raw)
}

// StripeSecretKey loads the Stripe secret key from SSM.
func StripeSecretKey(ctx context.Context, client SSMAPI) (string, error) {
	raw, err := GetSSMParameterCached(ctx, client, StripeSecretKeySSMParameterName, 10*time.Minute)
	if err != nil {
		return "", err
	}
	return parseAPIKeyValue(raw)
}

// StripeWebhookSecret loads the Stripe webhook signing secret from SSM.
func StripeWebhookSecret(ctx context.Context, client SSMAPI) (string, error) {
	raw, err := GetSSMParameterCached(ctx, client, StripeWebhookSecretSSMParameterName, 10*time.Minute)
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
