package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	raw, err := loadFirstSSMParameterCached(ctx, client, stripeSecretKeyCandidates(), 10*time.Minute)
	if err != nil {
		return "", err
	}
	return parseAPIKeyValue(raw)
}

// StripeWebhookSecret loads the Stripe webhook signing secret from SSM.
func StripeWebhookSecret(ctx context.Context, client SSMAPI) (string, error) {
	raw, err := loadFirstSSMParameterCached(ctx, client, stripeWebhookSecretCandidates(), 10*time.Minute)
	if err != nil {
		return "", err
	}
	return parseAPIKeyValue(raw)
}

func stripeStage() string {
	stage := strings.ToLower(strings.TrimSpace(os.Getenv("STAGE")))
	if stage == "" {
		stage = "lab"
	}
	return stage
}

func stripeSecretKeyCandidates() []string {
	stage := stripeStage()
	return []string{
		fmt.Sprintf("/lesser-host/stripe/%s/secret", stage),
		StripeSecretKeySSMParameterName,
	}
}

func stripeWebhookSecretCandidates() []string {
	stage := stripeStage()
	return []string{
		fmt.Sprintf("/lesser-host/stripe/%s/webhook", stage),
		StripeWebhookSecretSSMParameterName,
	}
}

func loadFirstSSMParameterCached(ctx context.Context, client SSMAPI, names []string, ttl time.Duration) (string, error) {
	var lastErr error
	for _, name := range names {
		raw, err := GetSSMParameterCached(ctx, client, name, ttl)
		if err == nil {
			return raw, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no parameter candidates provided")
}

func parseAPIKeyValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("api key is empty")
	}

	if parseValue, ok := parseAPIKeyValueFromJSON(raw); ok {
		return parseValue, nil
	}

	return raw, nil
}

func parseAPIKeyValueFromJSON(raw string) (string, bool) {
	if !looksLikeJSONObject(raw) {
		return "", false
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return "", false
	}

	for _, key := range []string{"api_key", "apiKey", "key", "token", "value"} {
		s, ok := obj[key].(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		return s, true
	}

	return "", false
}

func looksLikeJSONObject(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(raw, "{") && strings.HasSuffix(raw, "}")
}
