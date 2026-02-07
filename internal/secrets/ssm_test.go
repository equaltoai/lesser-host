package secrets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type stubSSM struct {
	getParameter func(ctx context.Context, params *ssm.GetParameterInput) (*ssm.GetParameterOutput, error)
}

func (s stubSSM) GetParameter(ctx context.Context, params *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if s.getParameter == nil {
		return nil, errors.New("stub GetParameter not set")
	}
	return s.getParameter(ctx, params)
}

func clearParamCache() {
	paramCache.Range(func(k, _ any) bool {
		paramCache.Delete(k)
		return true
	})
}

func TestGetSSMParameter_TrimsAndValidates(t *testing.T) {
	t.Parallel()

	clearParamCache()

	client := stubSSM{
		getParameter: func(_ context.Context, params *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			if params == nil || params.Name == nil {
				t.Fatalf("expected Name set")
			}
			if aws.ToString(params.Name) != "/path/to/secret" {
				t.Fatalf("unexpected name %q", aws.ToString(params.Name))
			}
			if params.WithDecryption == nil || !aws.ToBool(params.WithDecryption) {
				t.Fatalf("expected WithDecryption true")
			}
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{
					Value: aws.String("  value  "),
				},
			}, nil
		},
	}

	got, err := GetSSMParameter(context.Background(), client, " /path/to/secret ")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got != "value" {
		t.Fatalf("expected trimmed value, got %q", got)
	}
}

func TestGetSSMParameter_ErrorsOnEmptyInputsAndResponses(t *testing.T) {
	t.Parallel()

	clearParamCache()

	client := stubSSM{
		getParameter: func(_ context.Context, _ *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			return &ssm.GetParameterOutput{}, nil
		},
	}

	if _, err := GetSSMParameter(context.Background(), client, ""); err == nil {
		t.Fatalf("expected error for empty name")
	}
	if _, err := GetSSMParameter(context.Background(), client, "x"); err == nil {
		t.Fatalf("expected error for empty response")
	}
}

func TestGetSSMParameterCached_CachesWithinTTL(t *testing.T) {
	t.Parallel()

	clearParamCache()

	calls := 0
	client := stubSSM{
		getParameter: func(_ context.Context, _ *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			calls++
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{Value: aws.String("cached")},
			}, nil
		},
	}

	ctx := context.Background()

	got1, err := GetSSMParameterCached(ctx, client, "/p", 10*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got2, err := GetSSMParameterCached(ctx, client, "/p", 10*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got1 != "cached" || got2 != "cached" {
		t.Fatalf("unexpected values: %q %q", got1, got2)
	}
	if calls != 1 {
		t.Fatalf("expected 1 SSM call, got %d", calls)
	}
}
