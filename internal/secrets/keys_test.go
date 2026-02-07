package secrets

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestParseAPIKeyValue_AcceptsPlainAndJSON(t *testing.T) {
	t.Parallel()

	got, err := parseAPIKeyValue("  sk-test  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk-test" {
		t.Fatalf("expected trimmed key, got %q", got)
	}

	got, err = parseAPIKeyValue(`{"apiKey":"  k  "}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "k" {
		t.Fatalf("expected parsed json key, got %q", got)
	}
}

func TestLooksLikeJSONObject(t *testing.T) {
	t.Parallel()

	if looksLikeJSONObject("not json") {
		t.Fatalf("expected false")
	}
	if !looksLikeJSONObject(` {"a":1} `) {
		t.Fatalf("expected true")
	}
}

func TestLoadFirstSSMParameterCached_TriesCandidatesInOrder(t *testing.T) {
	t.Parallel()

	clearParamCache()

	client := stubSSM{
		getParameter: func(_ context.Context, params *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			switch aws.ToString(params.Name) {
			case "/missing":
				return nil, &ssmtypes.ParameterNotFound{}
			case "/present":
				return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String(" v ")}}, nil
			default:
				t.Fatalf("unexpected name %q", aws.ToString(params.Name))
				return nil, nil
			}
		},
	}

	got, err := loadFirstSSMParameterCached(context.Background(), client, []string{"/missing", "/present"}, 10*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v" {
		t.Fatalf("expected trimmed value, got %q", got)
	}
}

func TestStripeStageAndCandidates(t *testing.T) {
	t.Setenv("STAGE", "")
	if got := stripeStage(); got != "lab" {
		t.Fatalf("expected default stage lab, got %q", got)
	}

	t.Setenv("STAGE", " PROD ")
	if got := stripeStage(); got != "prod" {
		t.Fatalf("expected prod, got %q", got)
	}

	c1 := stripeSecretKeyCandidates()
	if len(c1) < 2 || c1[0] != "/lesser-host/stripe/prod/secret" || c1[1] != StripeSecretKeySSMParameterName {
		t.Fatalf("unexpected secret key candidates: %#v", c1)
	}

	c2 := stripeWebhookSecretCandidates()
	if len(c2) < 2 || c2[0] != "/lesser-host/stripe/prod/webhook" || c2[1] != StripeWebhookSecretSSMParameterName {
		t.Fatalf("unexpected webhook candidates: %#v", c2)
	}
}

func TestLoadFirstSSMParameterCached_ErrorsOnEmptyCandidates(t *testing.T) {
	t.Parallel()

	clearParamCache()

	if _, err := loadFirstSSMParameterCached(context.Background(), stubSSM{}, nil, 1*time.Minute); err == nil {
		t.Fatalf("expected error")
	}
}

func TestProviderKeyLoaders_UseSSMClientAndParse(t *testing.T) {
	t.Parallel()

	clearParamCache()

	client := stubSSM{
		getParameter: func(_ context.Context, params *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			name := aws.ToString(params.Name)
			switch name {
			case OpenAIServiceSSMParameterName:
				return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String(`{"api_key":"  sk-openai  "}`)}}, nil
			case ClaudeSSMParameterName:
				return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String("  claude-key  ")}}, nil
			case "/lesser-host/stripe/lab/secret":
				return nil, &ssmtypes.ParameterNotFound{}
			case StripeSecretKeySSMParameterName:
				return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String(`{"key":"  sk-stripe  "}`)}}, nil
			case "/lesser-host/stripe/lab/webhook":
				return nil, &ssmtypes.ParameterNotFound{}
			case StripeWebhookSecretSSMParameterName:
				return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String("  whsec_test  ")}}, nil
			default:
				t.Fatalf("unexpected parameter name %q", name)
				return nil, nil
			}
		},
	}

	got, err := OpenAIServiceKey(context.Background(), client)
	if err != nil || got != "sk-openai" {
		t.Fatalf("OpenAIServiceKey: got=%q err=%v", got, err)
	}

	got, err = ClaudeAPIKey(context.Background(), client)
	if err != nil || got != "claude-key" {
		t.Fatalf("ClaudeAPIKey: got=%q err=%v", got, err)
	}

	got, err = StripeSecretKey(context.Background(), client)
	if err != nil || got != "sk-stripe" {
		t.Fatalf("StripeSecretKey: got=%q err=%v", got, err)
	}

	got, err = StripeWebhookSecret(context.Background(), client)
	if err != nil || got != "whsec_test" {
		t.Fatalf("StripeWebhookSecret: got=%q err=%v", got, err)
	}
}
