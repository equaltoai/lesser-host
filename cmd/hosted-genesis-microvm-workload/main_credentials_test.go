package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
)

func TestAssumeMicroVMExecutionRoleSetsTemporaryAWSCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")

	api := &fakeMicroVMSTS{out: &sts.AssumeRoleOutput{Credentials: &ststypes.Credentials{
		AccessKeyId:     aws.String("access-key"),
		SecretAccessKey: aws.String("secret-key"),
		SessionToken:    aws.String("session-token"),
	}}}

	if err := assumeMicroVMExecutionRole(context.Background(), api, " arn:aws:iam::123456789012:role/lesser-host-lab-hosted-genesis-microvm-execution "); err != nil {
		t.Fatalf("assumeMicroVMExecutionRole: %v", err)
	}
	if got := api.roleArn; got != "arn:aws:iam::123456789012:role/lesser-host-lab-hosted-genesis-microvm-execution" {
		t.Fatalf("roleArn = %q", got)
	}
	if api.sessionName != "hosted-genesis-microvm-workload" || api.duration != 3600 {
		t.Fatalf("unexpected assume role envelope: session=%q duration=%d", api.sessionName, api.duration)
	}
	if got := getenvForTest("AWS_ACCESS_KEY_ID"); got != "access-key" {
		t.Fatalf("AWS_ACCESS_KEY_ID = %q", got)
	}
	if got := getenvForTest("AWS_SECRET_ACCESS_KEY"); got != "secret-key" {
		t.Fatalf("AWS_SECRET_ACCESS_KEY = %q", got)
	}
	if got := getenvForTest("AWS_SESSION_TOKEN"); got != "session-token" {
		t.Fatalf("AWS_SESSION_TOKEN = %q", got)
	}
}

func TestAssumeMicroVMExecutionRoleFailsClosed(t *testing.T) {
	if err := assumeMicroVMExecutionRole(context.Background(), nil, "arn:aws:iam::123456789012:role/microvm-exec"); err == nil {
		t.Fatal("expected nil STS client to fail")
	}
	if err := assumeMicroVMExecutionRole(context.Background(), &fakeMicroVMSTS{err: errors.New("denied")}, "arn:aws:iam::123456789012:role/microvm-exec"); err == nil {
		t.Fatal("expected assume role failure to propagate")
	}
	if err := assumeMicroVMExecutionRole(context.Background(), &fakeMicroVMSTS{out: &sts.AssumeRoleOutput{}}, "arn:aws:iam::123456789012:role/microvm-exec"); err == nil {
		t.Fatal("expected incomplete STS credentials to fail")
	}
	if err := assumeMicroVMExecutionRole(context.Background(), &fakeMicroVMSTS{}, ""); err != nil {
		t.Fatalf("empty role ARN should be no-op: %v", err)
	}
}

type fakeMicroVMSTS struct {
	out         *sts.AssumeRoleOutput
	err         error
	roleArn     string
	sessionName string
	duration    int32
}

func (f *fakeMicroVMSTS) AssumeRole(_ context.Context, input *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	if input != nil {
		f.roleArn = aws.ToString(input.RoleArn)
		f.sessionName = aws.ToString(input.RoleSessionName)
		f.duration = aws.ToInt32(input.DurationSeconds)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

func getenvForTest(key string) string { return os.Getenv(key) }
