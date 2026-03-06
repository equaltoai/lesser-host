package controlplane

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
)

const appTestRPCURL = "https://rpc"

func TestRegister_NilAndNoop(t *testing.T) {
	t.Parallel()

	if got := Register(nil, nil); got != nil {
		t.Fatalf("expected nil app")
	}

	app := apptheory.New()
	if got := Register(app, nil); got != app {
		t.Fatalf("expected same app returned")
	}
}

func TestResolveTipRPCURLFromSSM_EarlyReturns(t *testing.T) {
	resolveTipRPCURLFromSSM(nil)

	cfg := config.Config{TipRPCURL: appTestRPCURL, TipRPCURLSSMParam: "/x"}
	resolveTipRPCURLFromSSM(&cfg)
	if cfg.TipRPCURL != appTestRPCURL {
		t.Fatalf("expected TipRPCURL unchanged")
	}

	cfg = config.Config{}
	resolveTipRPCURLFromSSM(&cfg)
	if cfg.TipRPCURL != "" {
		t.Fatalf("expected empty TipRPCURL")
	}

	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	t.Setenv("AWS_EXECUTION_ENV", "")

	cfg = config.Config{TipRPCURLSSMParam: "/param"}
	resolveTipRPCURLFromSSM(&cfg)
	if cfg.TipRPCURL != "" {
		t.Fatalf("expected no resolution outside Lambda env")
	}
}

func TestResolveSoulRPCURLFromSSM_EarlyReturns(t *testing.T) {
	resolveSoulRPCURLFromSSM(nil)

	cfg := config.Config{SoulRPCURL: appTestRPCURL, SoulRPCURLSSMParam: "/x"}
	resolveSoulRPCURLFromSSM(&cfg)
	if cfg.SoulRPCURL != appTestRPCURL {
		t.Fatalf("expected SoulRPCURL unchanged")
	}

	cfg = config.Config{}
	resolveSoulRPCURLFromSSM(&cfg)
	if cfg.SoulRPCURL != "" {
		t.Fatalf("expected empty SoulRPCURL")
	}

	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	t.Setenv("AWS_EXECUTION_ENV", "")

	cfg = config.Config{SoulRPCURLSSMParam: "/param"}
	resolveSoulRPCURLFromSSM(&cfg)
	if cfg.SoulRPCURL != "" {
		t.Fatalf("expected no resolution outside Lambda env")
	}
}

func TestResolveSoulPackBucketNameFromSSM_EarlyReturns(t *testing.T) {
	resolveSoulPackBucketNameFromSSM(nil)

	cfg := config.Config{SoulPackBucketName: "bucket"}
	resolveSoulPackBucketNameFromSSM(&cfg)
	if cfg.SoulPackBucketName != "bucket" {
		t.Fatalf("expected SoulPackBucketName unchanged")
	}

	cfg = config.Config{}
	resolveSoulPackBucketNameFromSSM(&cfg)
	if cfg.SoulPackBucketName != "" {
		t.Fatalf("expected empty SoulPackBucketName")
	}

	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	t.Setenv("AWS_EXECUTION_ENV", "")

	cfg = config.Config{Stage: "LAB"}
	resolveSoulPackBucketNameFromSSM(&cfg)
	if cfg.SoulPackBucketName != "" {
		t.Fatalf("expected no resolution outside Lambda env")
	}
}

func TestResolveSoulMintSignerKeyFromSSM_EarlyReturns(t *testing.T) {
	resolveSoulMintSignerKeyFromSSM(nil)

	cfg := config.Config{SoulMintSignerKey: "key", SoulMintSignerKeySSMParam: "/x"}
	resolveSoulMintSignerKeyFromSSM(&cfg)
	if cfg.SoulMintSignerKey != "key" {
		t.Fatalf("expected SoulMintSignerKey unchanged")
	}

	cfg = config.Config{}
	resolveSoulMintSignerKeyFromSSM(&cfg)
	if cfg.SoulMintSignerKey != "" {
		t.Fatalf("expected empty SoulMintSignerKey")
	}

	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	t.Setenv("AWS_EXECUTION_ENV", "")

	cfg = config.Config{SoulMintSignerKeySSMParam: "/param"}
	resolveSoulMintSignerKeyFromSSM(&cfg)
	if cfg.SoulMintSignerKey != "" {
		t.Fatalf("expected no resolution outside Lambda env")
	}
}
