package trust

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"

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

func TestResolveTrustSoulRPCURLFromSSM_EarlyReturns(t *testing.T) {
	resolveTrustSoulRPCURLFromSSM(nil)

	cfg := config.Config{SoulRPCURL: appTestRPCURL, SoulRPCURLSSMParam: "/x"}
	resolveTrustSoulRPCURLFromSSM(&cfg)
	if cfg.SoulRPCURL != appTestRPCURL {
		t.Fatalf("expected SoulRPCURL unchanged")
	}

	cfg = config.Config{}
	resolveTrustSoulRPCURLFromSSM(&cfg)
	if cfg.SoulRPCURL != "" {
		t.Fatalf("expected empty SoulRPCURL")
	}

	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	t.Setenv("AWS_EXECUTION_ENV", "")

	cfg = config.Config{SoulRPCURLSSMParam: "/param"}
	resolveTrustSoulRPCURLFromSSM(&cfg)
	if cfg.SoulRPCURL != "" {
		t.Fatalf("expected no resolution outside Lambda env")
	}
}

func TestResolveTrustSoulPackBucketNameFromSSM_EarlyReturns(t *testing.T) {
	resolveTrustSoulPackBucketNameFromSSM(nil)

	cfg := config.Config{SoulPackBucketName: "bucket"}
	resolveTrustSoulPackBucketNameFromSSM(&cfg)
	if cfg.SoulPackBucketName != "bucket" {
		t.Fatalf("expected SoulPackBucketName unchanged")
	}

	cfg = config.Config{}
	resolveTrustSoulPackBucketNameFromSSM(&cfg)
	if cfg.SoulPackBucketName != "" {
		t.Fatalf("expected empty SoulPackBucketName")
	}

	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "")
	t.Setenv("AWS_EXECUTION_ENV", "")

	cfg = config.Config{Stage: "LAB"}
	resolveTrustSoulPackBucketNameFromSSM(&cfg)
	if cfg.SoulPackBucketName != "" {
		t.Fatalf("expected no resolution outside Lambda env")
	}
}
