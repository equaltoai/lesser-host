package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

func TestMicroVMExecutionCredentialsProviderUsesFreshDefaultConfigCredentials(t *testing.T) {
	originalLoader := loadMicroVMDefaultConfig
	t.Cleanup(func() { loadMicroVMDefaultConfig = originalLoader })

	providers := []aws.CredentialsProvider{
		credentials.NewStaticCredentialsProvider("turn-one", "secret-one", "token-one"),
		credentials.NewStaticCredentialsProvider("turn-two", "secret-two", "token-two"),
	}
	loads := 0
	loadMicroVMDefaultConfig = func(_ context.Context, _ ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		provider := providers[loads]
		loads++
		return aws.Config{Credentials: provider}, nil
	}

	// The execution-role ARN may still be present in an older image environment.
	// It must never cause the workload to re-assume the role it already runs as.
	t.Setenv("HOSTED_GENESIS_MICROVM_EXECUTION_ROLE_ARN", "arn:aws:iam::123456789012:role/lesser-host-live-hosted-genesis-microvm-execution")

	first, err := microVMExecutionCredentialsProvider(context.Background())
	if err != nil {
		t.Fatalf("first provider: %v", err)
	}
	firstCredentials, err := first.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("retrieve first provider: %v", err)
	}
	second, err := microVMExecutionCredentialsProvider(context.Background())
	if err != nil {
		t.Fatalf("second provider: %v", err)
	}
	secondCredentials, err := second.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("retrieve second provider: %v", err)
	}

	if loads != 2 {
		t.Fatalf("expected one default-config load per turn, got %d", loads)
	}
	if firstCredentials.AccessKeyID != "turn-one" || secondCredentials.AccessKeyID != "turn-two" {
		t.Fatalf("expected fresh execution-role credentials per turn, got first=%q second=%q", firstCredentials.AccessKeyID, secondCredentials.AccessKeyID)
	}
}

func TestMicroVMExecutionCredentialsProviderPropagatesConfigLoadFailure(t *testing.T) {
	originalLoader := loadMicroVMDefaultConfig
	t.Cleanup(func() { loadMicroVMDefaultConfig = originalLoader })

	want := errors.New("credential source unavailable")
	loadMicroVMDefaultConfig = func(_ context.Context, _ ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, want
	}

	provider, err := microVMExecutionCredentialsProvider(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("expected config load failure, got provider=%#v err=%v", provider, err)
	}
}
