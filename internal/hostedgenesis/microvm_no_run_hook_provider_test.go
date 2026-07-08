package hostedgenesis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	microvmtestkit "github.com/theory-cloud/apptheory/testkit/microvm"
)

func TestNoRunHookProviderStartsMicroVMWithoutRunHookPayload(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 7, 18, 15, 0, 0, time.UTC)
	api := &fakeNoRunHookAPI{
		runOutput: &lambdamicrovms.RunMicrovmOutput{
			ImageArn:                 aws.String("arn:aws:lambda:us-east-1:123456789012:microvm-image:lesser-host-lab_hosted_genesis"),
			ImageVersion:             aws.String("3.0"),
			MicrovmId:                aws.String("microvm-123"),
			StartedAt:                aws.Time(startedAt),
			State:                    lambdatypes.MicrovmStateRunning,
			EgressNetworkConnectors:  []string{"INTERNET_EGRESS"},
			IngressNetworkConnectors: []string{"HTTP_INGRESS"},
			ExecutionRoleArn:         aws.String("arn:aws:iam::123456789012:role/lesser-host-lab-hosted-genesis-microvm-execution"),
			MaximumDurationInSeconds: aws.Int32(300),
		},
	}
	provider, err := NewNoRunHookAWSLambdaMicroVMProvider(api, microvmtestkit.NewFakeProvider())
	require.NoError(t, err)

	session, err := provider.Run(context.Background(), validNoRunHookProviderRunInput())
	require.NoError(t, err)

	require.NotNil(t, api.runInput)
	require.Nil(t, api.runInput.RunHookPayload, "Host's no-hooks MicroVM image must be started without AWS run-hook payload")
	require.Equal(t, "arn:aws:lambda:us-east-1:123456789012:microvm-image:lesser-host-lab_hosted_genesis", aws.ToString(api.runInput.ImageIdentifier))
	require.Equal(t, "req-run", aws.ToString(api.runInput.ClientToken))
	require.Equal(t, "arn:aws:iam::123456789012:role/lesser-host-lab-hosted-genesis-microvm-execution", aws.ToString(api.runInput.ExecutionRoleArn))
	require.Equal(t, int32(300), aws.ToInt32(api.runInput.MaximumDurationInSeconds))
	require.Equal(t, []string{"HTTP_INGRESS"}, api.runInput.IngressNetworkConnectors)
	require.Equal(t, []string{"INTERNET_EGRESS"}, api.runInput.EgressNetworkConnectors)
	logging, ok := api.runInput.Logging.(*lambdatypes.LoggingMemberCloudWatch)
	require.True(t, ok, "run logging must be enabled for workload execution proof")
	require.Equal(t, "/aws/lambda/microvms/lesser-host-lab_hosted_genesis", aws.ToString(logging.Value.LogGroup))
	require.Equal(t, "runtime/conversation-123/req-run", aws.ToString(logging.Value.LogStream))

	require.Equal(t, "slug:pan", session.TenantID)
	require.Equal(t, MicroVMNamespace, session.Namespace)
	require.Equal(t, "conversation-123", session.SessionID)
	require.Equal(t, "microvm-123", session.ProviderMicroVMID)
	require.Equal(t, runtimemicrovm.StateRunning, session.State)
	require.Equal(t, "running", session.ProviderState)
	require.False(t, session.Terminal)
	require.Equal(t, startedAt, session.StartedAt)
}

func TestNoRunHookProviderDoesNotDuplicateLegacyEgressConnector(t *testing.T) {
	t.Parallel()

	api := &fakeNoRunHookAPI{
		runOutput: &lambdamicrovms.RunMicrovmOutput{
			ImageArn:                 aws.String("arn:aws:lambda:us-east-1:123456789012:microvm-image:lesser-host-lab_hosted_genesis"),
			ImageVersion:             aws.String("3.0"),
			MicrovmId:                aws.String("microvm-123"),
			StartedAt:                aws.Time(time.Date(2026, 7, 7, 19, 30, 0, 0, time.UTC)),
			State:                    lambdatypes.MicrovmStateRunning,
			EgressNetworkConnectors:  []string{"INTERNET_EGRESS"},
			IngressNetworkConnectors: []string{"HTTP_INGRESS"},
			ExecutionRoleArn:         aws.String("arn:aws:iam::123456789012:role/lesser-host-lab-hosted-genesis-microvm-execution"),
		},
	}
	provider, err := NewNoRunHookAWSLambdaMicroVMProvider(api, microvmtestkit.NewFakeProvider())
	require.NoError(t, err)

	input := validNoRunHookProviderRunInput()
	input.NetworkConnectorRef = "INTERNET_EGRESS"
	input.EgressNetworkConnectorRefs = []string{" INTERNET_EGRESS ", "INTERNET_EGRESS"}

	_, err = provider.Run(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, []string{"INTERNET_EGRESS"}, api.runInput.EgressNetworkConnectors)
}

func TestNoRunHookProviderFallsBackToLegacyEgressConnector(t *testing.T) {
	t.Parallel()

	api := &fakeNoRunHookAPI{
		runOutput: &lambdamicrovms.RunMicrovmOutput{
			ImageArn:                 aws.String("arn:aws:lambda:us-east-1:123456789012:microvm-image:lesser-host-lab_hosted_genesis"),
			ImageVersion:             aws.String("3.0"),
			MicrovmId:                aws.String("microvm-123"),
			StartedAt:                aws.Time(time.Date(2026, 7, 7, 19, 31, 0, 0, time.UTC)),
			State:                    lambdatypes.MicrovmStateRunning,
			EgressNetworkConnectors:  []string{"INTERNET_EGRESS"},
			IngressNetworkConnectors: []string{"HTTP_INGRESS"},
			ExecutionRoleArn:         aws.String("arn:aws:iam::123456789012:role/lesser-host-lab-hosted-genesis-microvm-execution"),
		},
	}
	provider, err := NewNoRunHookAWSLambdaMicroVMProvider(api, microvmtestkit.NewFakeProvider())
	require.NoError(t, err)

	input := validNoRunHookProviderRunInput()
	input.NetworkConnectorRef = " INTERNET_EGRESS "
	input.EgressNetworkConnectorRefs = nil

	_, err = provider.Run(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, []string{"INTERNET_EGRESS"}, api.runInput.EgressNetworkConnectors)
}

func TestNoRunHookProviderSanitizesRunErrors(t *testing.T) {
	t.Parallel()

	api := &fakeNoRunHookAPI{
		runErr: errors.New("ValidationException: The run hook must be enabled in the MicroVM image to pass the run hook payload"),
	}
	provider, err := NewNoRunHookAWSLambdaMicroVMProvider(api, microvmtestkit.NewFakeProvider())
	require.NoError(t, err)

	_, err = provider.Run(context.Background(), validNoRunHookProviderRunInput())
	require.Error(t, err)
	var safe runtimemicrovm.SafeError
	require.ErrorAs(t, err, &safe)
	require.Equal(t, runtimemicrovm.ErrorCodeProviderOperationFailed, safe.Code)
	require.Equal(t, "req-run", safe.RequestID)
	require.NotContains(t, err.Error(), "ValidationException")
	require.NotContains(t, err.Error(), "run hook payload")
}

func TestNoRunHookLoggingParsesImageNameFromColonArn(t *testing.T) {
	t.Parallel()
	input := validNoRunHookProviderRunInput()
	logging, ok := noRunHookLogging(input).(*lambdatypes.LoggingMemberCloudWatch)
	require.True(t, ok)
	require.Equal(t, "/aws/lambda/microvms/lesser-host-lab_hosted_genesis", aws.ToString(logging.Value.LogGroup))
}

func TestNoRunHookLoggingSanitizesStreamName(t *testing.T) {
	t.Parallel()
	input := validNoRunHookProviderRunInput()
	input.SessionID = "conv:bad"
	input.RequestID = "req*bad"
	logging, ok := noRunHookLogging(input).(*lambdatypes.LoggingMemberCloudWatch)
	require.True(t, ok)
	require.Equal(t, "runtime/conv-bad/req-bad", aws.ToString(logging.Value.LogStream))
}

type fakeNoRunHookAPI struct {
	runInput  *lambdamicrovms.RunMicrovmInput
	runOutput *lambdamicrovms.RunMicrovmOutput
	runErr    error
}

func (f *fakeNoRunHookAPI) RunMicrovm(_ context.Context, input *lambdamicrovms.RunMicrovmInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.RunMicrovmOutput, error) {
	f.runInput = input
	if f.runErr != nil {
		return nil, f.runErr
	}
	return f.runOutput, nil
}

func validNoRunHookProviderRunInput() runtimemicrovm.ProviderRunInput {
	return runtimemicrovm.ProviderRunInput{
		RequestID:   "req-run",
		TenantID:    "slug:pan",
		Namespace:   MicroVMNamespace,
		SessionID:   "conversation-123",
		AuthContext: runtimemicrovm.AuthContext{Subject: MicroVMControllerID, TenantID: "slug:pan", Namespace: MicroVMNamespace},
		ImageRef:    "arn:aws:lambda:us-east-1:123456789012:microvm-image:lesser-host-lab_hosted_genesis",
		IngressNetworkConnectorRefs: []string{
			"HTTP_INGRESS",
		},
		EgressNetworkConnectorRefs: []string{
			"INTERNET_EGRESS",
		},
		MaximumDurationSeconds: 300,
		ExecutionRoleArn:       "arn:aws:iam::123456789012:role/lesser-host-lab-hosted-genesis-microvm-execution",
	}
}
