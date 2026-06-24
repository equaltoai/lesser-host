package hostedgenesis

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	microvmtestkit "github.com/theory-cloud/apptheory/testkit/microvm"
)

func TestMicroVMControllerRuntimeExercisesAppTheoryCommands(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	client := microvmtestkit.NewFakeClientWithTime(time.Date(2026, 6, 24, 18, 0, 0, 0, time.UTC))
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Client:              client,
		ImageRef:            "arn:aws:lambda:us-east-1:123456789012:microvm-image/hosted-genesis:1",
		NetworkConnectorRef: "arn:aws:lambda:us-east-1:123456789012:network-connector/hosted-genesis",
	})
	require.NoError(t, err)

	create, err := runtime.Create(context.Background(), "req-create", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandCreate, create.Command)
	require.Equal(t, runtimemicrovm.StateRequested, create.State)
	require.Equal(t, "conv_123", create.SessionID)

	start, err := runtime.Command(context.Background(), runtimemicrovm.CommandStart, "req-start", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.StateStarting, start.State)
	require.Equal(t, runtimemicrovm.StateStarted, start.DesiredState)

	status, err := runtime.Command(context.Background(), runtimemicrovm.CommandStatus, "req-status", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandStatus, status.Command)
	require.Equal(t, runtimemicrovm.StateStarting, status.LifecycleState)

	session, err := runtime.Command(context.Background(), runtimemicrovm.CommandSession, "req-session", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandSession, session.Command)
	require.Equal(t, "slug:demo", session.TenantID)

	stop, err := runtime.Command(context.Background(), runtimemicrovm.CommandStop, "req-stop", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.StateStopping, stop.State)
	require.Equal(t, runtimemicrovm.StateStopped, stop.DesiredState)

	calls := client.Calls()
	require.Len(t, calls, 5)
	require.Equal(t, []runtimemicrovm.Command{
		runtimemicrovm.CommandCreate,
		runtimemicrovm.CommandStart,
		runtimemicrovm.CommandStatus,
		runtimemicrovm.CommandSession,
		runtimemicrovm.CommandStop,
	}, []runtimemicrovm.Command{calls[0].Command, calls[1].Command, calls[2].Command, calls[3].Command, calls[4].Command})
	for _, call := range calls {
		require.Equal(t, "slug:demo", call.TenantID)
		require.Equal(t, MicroVMNamespace, call.Namespace)
		require.Equal(t, "conv_123", call.SessionID)
	}
}

func TestMicroVMControllerRuntimeSafeEnvelopeRejectsForbiddenFields(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Client:              microvmtestkit.NewFakeClient(),
		ImageRef:            "image-ref",
		NetworkConnectorRef: "network-ref",
	})
	require.NoError(t, err)

	req, err := NewMicroVMCreateRequest("req-forbidden", binding, "image-ref", "network-ref")
	require.NoError(t, err)
	req.SessionSpec.Metadata["bearer_token"] = "must-not-cross-boundary"

	resp, err := runtime.Handle(context.Background(), req)
	require.Error(t, err)
	require.NotNil(t, resp.Error)
	require.Equal(t, runtimemicrovm.ErrorCodeForbiddenField, resp.Error.Code)
	payload, marshalErr := json.Marshal(resp)
	require.NoError(t, marshalErr)
	require.NotContains(t, strings.ToLower(string(payload)), "must-not-cross-boundary")
}

func TestMicroVMLifecycleRefReconcilesExecutionCacheOnly(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	client := microvmtestkit.NewFakeClient()
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{Client: client, ImageRef: "image-ref", NetworkConnectorRef: "network-ref"})
	require.NoError(t, err)

	create, err := runtime.Create(context.Background(), "req-create", binding)
	require.NoError(t, err)
	ref, err := MicroVMLifecycleRefFromResponse(binding, create, time.Date(2026, 6, 24, 18, 1, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, MicroVMSourceOfTruth, ref.SourceOfTruth)
	require.Equal(t, "slug:demo", ref.TenantID)
	require.Equal(t, "conv_123", ref.SessionID)
	require.Equal(t, runtimemicrovm.CommandCreate, ref.LastAction)

	_, err = runtime.Command(context.Background(), runtimemicrovm.CommandStart, "req-start", binding)
	require.NoError(t, err)
	status, err := runtime.Command(context.Background(), runtimemicrovm.CommandStatus, "req-status", binding)
	require.NoError(t, err)
	reconciled, err := ReconcileMicroVMRegistryStatus(binding, ref, runtimemicrovm.SessionStatus{
		TenantID:        status.TenantID,
		Namespace:       status.Namespace,
		SessionID:       status.SessionID,
		State:           status.State,
		DesiredState:    status.DesiredState,
		LifecycleState:  status.LifecycleState,
		MicroVMID:       status.MicroVMID,
		LastAction:      status.LastAction,
		LastTransition:  status.LastTransition,
		RegistryVersion: status.RegistryVersion,
	})
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandStart, reconciled.LastAction)

	_, err = ReconcileMicroVMRegistryStatus(binding, ref, runtimemicrovm.SessionStatus{
		TenantID:        "slug:other",
		Namespace:       MicroVMNamespace,
		SessionID:       "conv_123",
		State:           runtimemicrovm.StateStarted,
		DesiredState:    runtimemicrovm.StateStarted,
		LifecycleState:  runtimemicrovm.StateStarted,
		LastAction:      runtimemicrovm.CommandStatus,
		LastTransition:  time.Now().UTC(),
		RegistryVersion: 1,
	})
	require.ErrorIs(t, err, ErrStaleMicroVMRegistryState)

	lostRegistryRuntime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{Client: microvmtestkit.NewFakeClient(), ImageRef: "image-ref", NetworkConnectorRef: "network-ref"})
	require.NoError(t, err)
	_, err = lostRegistryRuntime.Command(context.Background(), runtimemicrovm.CommandStatus, "req-lost", binding)
	require.Error(t, err, "registry/cache loss must not invent Host business state")
	require.NoError(t, ref.Validate(binding), "Host can still reconstruct the MicroVM binding from HostedGenesisSession truth")
}

func TestProvisionalDogfoodMicroVMClientUsesAppTheoryRegistryWithoutRawSDK(t *testing.T) {
	t.Parallel()

	client, err := NewProvisionalDogfoodMicroVMClient(runtimemicrovm.NewMemorySessionRegistry(), time.Minute)
	require.NoError(t, err)
	var constrained runtimemicrovm.Client = client
	require.NotNil(t, constrained)
	require.NoError(t, ValidateAppTheoryMicroVMContracts())
	require.Equal(t, "delivery-bcb585616b891657", AppTheoryFeedbackDeliveryID)

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	packageDir := filepath.Dir(currentFile)
	entries, err := os.ReadDir(packageDir)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(packageDir, entry.Name()))
		require.NoError(t, err)
		source := string(b)
		forbiddenImport := "aws-sdk-go-v2/service/" + "lambda"
		require.NotContains(t, source, forbiddenImport, "dogfood adapter must not expose a raw Lambda SDK dependency")
		forbiddenEscape := "RawAWSSDK" + ": true"
		require.NotContains(t, source, forbiddenEscape, "AppTheory raw SDK escape hatch must remain disabled")
	}
}

func TestMicroVMLabCanaryHarnessExercisesLifecycleAndSecretChecks(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	client := microvmtestkit.NewFakeClientWithTime(time.Date(2026, 6, 24, 19, 0, 0, 0, time.UTC))
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{Client: client, ImageRef: "image-ref", NetworkConnectorRef: "network-ref"})
	require.NoError(t, err)

	responses := make([]runtimemicrovm.ControllerResponse, 0, 5)
	for _, step := range []struct {
		command runtimemicrovm.Command
		request string
	}{
		{runtimemicrovm.CommandCreate, "canary-create"},
		{runtimemicrovm.CommandStart, "canary-start"},
		{runtimemicrovm.CommandStatus, "canary-status"},
		{runtimemicrovm.CommandSession, "canary-session"},
		{runtimemicrovm.CommandStop, "canary-stop"},
	} {
		var resp runtimemicrovm.ControllerResponse
		if step.command == runtimemicrovm.CommandCreate {
			resp, err = runtime.Create(context.Background(), step.request, binding)
		} else {
			resp, err = runtime.Command(context.Background(), step.command, step.request, binding)
		}
		require.NoError(t, err)
		responses = append(responses, resp)
	}

	evidence, err := json.MarshalIndent(struct {
		Canary string                              `json:"canary"`
		Steps  []runtimemicrovm.ControllerResponse `json:"steps"`
	}{Canary: "hosted-genesis-microvm-non-deploying", Steps: responses}, "", "  ")
	require.NoError(t, err)
	lower := strings.ToLower(string(evidence))
	for _, forbidden := range []string{
		"bearer_token",
		"authorization",
		"aws_access_key_id",
		"aws_secret_access_key",
		"aws_session_token",
		"instance-api-key",
		"provider_secret",
		"wallet_signature",
		"raw transcript",
		"endpoint_token",
	} {
		require.NotContains(t, lower, forbidden)
	}
}

func testMicroVMBinding() MicroVMSessionBinding {
	return MicroVMSessionBinding{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		ConversationID: "conv_123",
		TurnID:         "turn_123",
	}
}
