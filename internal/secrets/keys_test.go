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
