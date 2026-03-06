package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

type stubSSMWrite struct {
	putParameter func(ctx context.Context, params *ssm.PutParameterInput) (*ssm.PutParameterOutput, error)
}

func (s stubSSMWrite) PutParameter(ctx context.Context, params *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	if s.putParameter == nil {
		return nil, errors.New("stub PutParameter not set")
	}
	return s.putParameter(ctx, params)
}

func TestPutSSMSecureString_ValidatesAndWrites(t *testing.T) {
	if err := PutSSMSecureString(context.Background(), stubSSMWrite{}, "", "value", false); err == nil {
		t.Fatalf("expected error for empty name")
	}
	if err := PutSSMSecureString(context.Background(), stubSSMWrite{}, "/name", "", false); err == nil {
		t.Fatalf("expected error for empty value")
	}

	client := stubSSMWrite{
		putParameter: func(_ context.Context, params *ssm.PutParameterInput) (*ssm.PutParameterOutput, error) {
			if got := aws.ToString(params.Name); got != "/path" {
				t.Fatalf("unexpected name: %q", got)
			}
			if got := aws.ToString(params.Value); got != "secret" {
				t.Fatalf("unexpected value: %q", got)
			}
			if params.Type != ssmtypes.ParameterTypeSecureString {
				t.Fatalf("unexpected parameter type: %q", params.Type)
			}
			if params.Overwrite == nil || !aws.ToBool(params.Overwrite) {
				t.Fatalf("expected overwrite=true")
			}
			return &ssm.PutParameterOutput{}, nil
		},
	}

	if err := PutSSMSecureString(context.Background(), client, " /path ", " secret ", true); err != nil {
		t.Fatalf("PutSSMSecureString: %v", err)
	}
}

func TestPutSSMSecureString_WrapsClientErrors(t *testing.T) {
	err := PutSSMSecureString(context.Background(), stubSSMWrite{
		putParameter: func(_ context.Context, _ *ssm.PutParameterInput) (*ssm.PutParameterOutput, error) {
			return nil, errors.New("boom")
		},
	}, "/path", "value", false)
	if err == nil || err.Error() == "boom" {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}

func TestIsSSMParameterAlreadyExists(t *testing.T) {
	if IsSSMParameterAlreadyExists(nil) {
		t.Fatalf("expected false for nil")
	}
	if !IsSSMParameterAlreadyExists(&smithy.GenericAPIError{Code: "ParameterAlreadyExists"}) {
		t.Fatalf("expected true for smithy api error")
	}
	if !IsSSMParameterAlreadyExists(&ssmtypes.ParameterAlreadyExists{}) {
		t.Fatalf("expected true for typed error")
	}
	if IsSSMParameterAlreadyExists(errors.New("boom")) {
		t.Fatalf("expected false for generic error")
	}
}
