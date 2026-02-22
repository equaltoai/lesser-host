package soulreputationworker

import (
	"os"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
)

func TestRegister_NilAndNoop(t *testing.T) {
	t.Parallel()

	if got := Register(nil, nil); got != nil {
		t.Fatalf("expected nil")
	}

	app := apptheory.New()
	if got := Register(app, nil); got != app {
		t.Fatalf("expected same app returned")
	}
}

func TestResolveTipRPCURLFromSSM_EarlyReturns(t *testing.T) {
	t.Parallel()

	resolveTipRPCURLFromSSM(nil)

	cfg := config.Config{TipRPCURL: "https://rpc", TipRPCURLSSMParam: "/x"}
	resolveTipRPCURLFromSSM(&cfg)
	if cfg.TipRPCURL != "https://rpc" {
		t.Fatalf("expected TipRPCURL unchanged")
	}

	cfg = config.Config{TipRPCURL: "", TipRPCURLSSMParam: ""}
	resolveTipRPCURLFromSSM(&cfg)
	if cfg.TipRPCURL != "" {
		t.Fatalf("expected empty TipRPCURL")
	}

	prevFn := os.Getenv("AWS_LAMBDA_FUNCTION_NAME")
	prevEnv := os.Getenv("AWS_EXECUTION_ENV")
	_ = os.Unsetenv("AWS_LAMBDA_FUNCTION_NAME")
	_ = os.Unsetenv("AWS_EXECUTION_ENV")
	t.Cleanup(func() {
		_ = os.Setenv("AWS_LAMBDA_FUNCTION_NAME", prevFn)
		_ = os.Setenv("AWS_EXECUTION_ENV", prevEnv)
	})

	cfg = config.Config{TipRPCURL: "", TipRPCURLSSMParam: "/param"}
	resolveTipRPCURLFromSSM(&cfg)
	if cfg.TipRPCURL != "" {
		t.Fatalf("expected no resolution outside Lambda env")
	}
}

func TestResolveSoulPackBucketNameFromSSM_EarlyReturns(t *testing.T) {
	t.Parallel()

	resolveSoulPackBucketNameFromSSM(nil)

	cfg := config.Config{SoulPackBucketName: "b"}
	resolveSoulPackBucketNameFromSSM(&cfg)
	if cfg.SoulPackBucketName != "b" {
		t.Fatalf("expected SoulPackBucketName unchanged")
	}

	cfg = config.Config{SoulPackBucketName: "", SoulPackBucketNameSSMParam: "", Stage: ""}
	resolveSoulPackBucketNameFromSSM(&cfg)
	if cfg.SoulPackBucketName != "" {
		t.Fatalf("expected empty SoulPackBucketName")
	}

	prevFn := os.Getenv("AWS_LAMBDA_FUNCTION_NAME")
	prevEnv := os.Getenv("AWS_EXECUTION_ENV")
	_ = os.Unsetenv("AWS_LAMBDA_FUNCTION_NAME")
	_ = os.Unsetenv("AWS_EXECUTION_ENV")
	t.Cleanup(func() {
		_ = os.Setenv("AWS_LAMBDA_FUNCTION_NAME", prevFn)
		_ = os.Setenv("AWS_EXECUTION_ENV", prevEnv)
	})

	cfg = config.Config{SoulPackBucketName: "", SoulPackBucketNameSSMParam: "", Stage: "LAB"}
	resolveSoulPackBucketNameFromSSM(&cfg)
	if cfg.SoulPackBucketName != "" {
		t.Fatalf("expected no resolution outside Lambda env")
	}
}
