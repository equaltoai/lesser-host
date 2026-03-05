package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

// SSMWriteAPI is the subset of the AWS SSM client used for writing parameters.
type SSMWriteAPI interface {
	PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

// PutSSMSecureString writes a SecureString parameter to SSM.
func PutSSMSecureString(ctx context.Context, client SSMWriteAPI, name string, value string, overwrite bool) error {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" {
		return fmt.Errorf("parameter name is required")
	}
	if value == "" {
		return fmt.Errorf("parameter value is required")
	}

	if client == nil {
		c, err := defaultClient(ctx)
		if err != nil {
			return err
		}
		wc, ok := c.(SSMWriteAPI)
		if !ok {
			return fmt.Errorf("ssm client does not support PutParameter")
		}
		client = wc
	}

	_, err := client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      &name,
		Value:     &value,
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: aws.Bool(overwrite),
	})
	if err != nil {
		return fmt.Errorf("ssm put parameter %q: %w", name, err)
	}
	return nil
}

// IsSSMParameterAlreadyExists reports whether an SSM PutParameter failed because the parameter already exists.
func IsSSMParameterAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "ParameterAlreadyExists"
	}
	var typed *ssmtypes.ParameterAlreadyExists
	return errors.As(err, &typed)
}
