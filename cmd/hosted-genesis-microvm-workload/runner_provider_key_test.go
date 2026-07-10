package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/equaltoai/lesser-host/internal/secrets"
)

// setProviderSSMLoader swaps the SSM fallback loader for one provider family
// and restores it on cleanup. Tests use this to avoid standing up a real AWS
// SSM client (secrets.OpenAIServiceKey with nil client calls the real
// defaultClient, which needs AWS credentials). The loader signature matches
// secrets.OpenAIServiceKey / secrets.ClaudeAPIKey so the production defaults can
// be swapped for a fake that returns a fixed value or error.
func setProviderSSMLoader(t *testing.T, family string, loader ssmKeyLoader) {
	t.Helper()
	prev := providerSSMLoaders[family]
	providerSSMLoaders[family] = loader
	t.Cleanup(func() {
		providerSSMLoaders[family] = prev
	})
}

// clearProviderEnv unsets the provider key env vars and restores them on
// cleanup, so each subtest starts from a known empty env.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	keys := []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "CLAUDE_API_KEY"}
	saved := map[string]string{}
	for _, k := range keys {
		saved[k] = os.Getenv(k)
		_ = os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range keys {
			_ = os.Setenv(k, saved[k])
		}
	})
}

// TestProviderAPIKey_EnvFirst verifies the env path still wins when set, even
// if an SSM loader is wired. This preserves the local-test path (local tests set
// OPENAI_API_KEY directly via withOpenAIKey) so SSM fallback is purely additive.
func TestProviderAPIKey_EnvFirst(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENAI_API_KEY", "openai-env-key")
	// Wire a loader that would fail; env must win so the loader is never called.
	called := false
	setProviderSSMLoader(t, "openai", func(_ context.Context, _ secrets.SSMAPI) (string, error) {
		called = true
		return "", errors.New("should not be called")
	})

	k, err := providerAPIKey(context.Background(), "openai:gpt-test")
	if err != nil {
		t.Fatalf("env-first openai: unexpected err: %v", err)
	}
	if k != "openai-env-key" {
		t.Fatalf("env-first openai: got %q, want openai-env-key", k)
	}
	if called {
		t.Fatal("env-first openai: SSM loader must not be called when env is set")
	}

	t.Setenv("ANTHROPIC_API_KEY", "anthropic-env-key")
	setProviderSSMLoader(t, "anthropic", func(_ context.Context, _ secrets.SSMAPI) (string, error) {
		called = true
		return "", errors.New("should not be called")
	})
	called = false
	k, err = providerAPIKey(context.Background(), "anthropic:claude-test")
	if err != nil {
		t.Fatalf("env-first anthropic: unexpected err: %v", err)
	}
	if k != "anthropic-env-key" {
		t.Fatalf("env-first anthropic: got %q, want anthropic-env-key", k)
	}
	if called {
		t.Fatal("env-first anthropic: SSM loader must not be called when env is set")
	}
}

// TestProviderAPIKey_SSMFallback verifies that when env is empty, the SSM loader
// is the production path (the in-VM execution role grants ssm:GetParameter on
// the provider-key SecureString params). This is the AppTheory
// execution-role correction: raw provider keys no longer need to live in the
// MicroVM image env.
func TestProviderAPIKey_SSMFallback(t *testing.T) {
	clearProviderEnv(t)
	setProviderSSMLoader(t, "openai", func(_ context.Context, _ secrets.SSMAPI) (string, error) {
		return "openai-ssm-key", nil
	})
	k, err := providerAPIKey(context.Background(), "openai:gpt-test")
	if err != nil {
		t.Fatalf("ssm fallback openai: unexpected err: %v", err)
	}
	if k != "openai-ssm-key" {
		t.Fatalf("ssm fallback openai: got %q, want openai-ssm-key", k)
	}

	setProviderSSMLoader(t, "anthropic", func(_ context.Context, _ secrets.SSMAPI) (string, error) {
		return "anthropic-ssm-key", nil
	})
	k, err = providerAPIKey(context.Background(), "anthropic:claude-test")
	if err != nil {
		t.Fatalf("ssm fallback anthropic: unexpected err: %v", err)
	}
	if k != "anthropic-ssm-key" {
		t.Fatalf("ssm fallback anthropic: got %q, want anthropic-ssm-key", k)
	}
}

// TestProviderAPIKey_FailClosed verifies that a missing key in both env and SSM
// fails closed with a typed error (no silent empty-string return, no panic).
func TestProviderAPIKey_FailClosed(t *testing.T) {
	clearProviderEnv(t)
	setProviderSSMLoader(t, "openai", func(_ context.Context, _ secrets.SSMAPI) (string, error) {
		return "", errors.New("ssm: parameter not found")
	})
	if _, err := providerAPIKey(context.Background(), "openai:gpt-test"); err == nil {
		t.Fatal("fail-closed openai: expected error when env + SSM both empty")
	}

	setProviderSSMLoader(t, "anthropic", func(_ context.Context, _ secrets.SSMAPI) (string, error) {
		return "", errors.New("ssm: parameter not found")
	})
	if _, err := providerAPIKey(context.Background(), "anthropic:claude-test"); err == nil {
		t.Fatal("fail-closed anthropic: expected error when env + SSM both empty")
	}
}

// TestProviderAPIKey_UnsupportedModelSet verifies an unknown model-set prefix is
// rejected regardless of env/loader state.
func TestProviderAPIKey_UnsupportedModelSet(t *testing.T) {
	clearProviderEnv(t)
	if _, err := providerAPIKey(context.Background(), "mistral:some-model"); err == nil {
		t.Fatal("expected error for unsupported model set")
	}
}
