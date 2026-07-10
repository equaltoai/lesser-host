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

func TestMicroVMControllerRuntimeExercisesAppTheoryM16Commands(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	provider := microvmtestkit.NewFakeProviderWithTime(time.Date(2026, 6, 25, 18, 0, 0, 0, time.UTC))
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:                    provider,
		Registry:                    runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:                    "arn:aws:lambda:us-east-1:123456789012:microvm-image/hosted-genesis:1",
		NetworkConnectorRef:         "arn:aws:lambda:us-east-1:123456789012:network-connector/hosted-genesis-egress",
		IngressNetworkConnectorRefs: []string{"HTTP_INGRESS"},
		EgressNetworkConnectorRefs:  []string{"arn:aws:lambda:us-east-1:123456789012:network-connector/hosted-genesis-egress"},
	})
	require.NoError(t, err)

	run, err := runtime.Run(context.Background(), "req-run", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandRun, run.Command)
	require.Equal(t, runtimemicrovm.StateRunning, run.State)
	require.Equal(t, "conv_123", run.SessionID)
	require.NotEmpty(t, run.ProviderMicroVMID)

	get, err := runtime.Command(context.Background(), runtimemicrovm.CommandGet, "req-get", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandGet, get.Command)
	require.Equal(t, runtimemicrovm.StateRunning, get.State)

	list, err := runtime.Command(context.Background(), runtimemicrovm.CommandList, "req-list", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandList, list.Command)
	require.Len(t, list.Sessions, 1)
	require.Equal(t, "slug:demo", list.Sessions[0].TenantID)

	suspend, err := runtime.Command(context.Background(), runtimemicrovm.CommandSuspend, "req-suspend", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.StateSuspended, suspend.State)

	resume, err := runtime.Command(context.Background(), runtimemicrovm.CommandResume, "req-resume", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.StateReady, resume.State)

	authToken, err := runtime.Command(context.Background(), runtimemicrovm.CommandAuthToken, "req-auth-token", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandAuthToken, authToken.Command)
	require.Equal(t, "auth", authToken.TokenType)
	require.NotEmpty(t, authToken.TokenID)
	require.Empty(t, authToken.ProviderState)

	shellToken, err := runtime.Command(context.Background(), runtimemicrovm.CommandShellAuthToken, "req-shell-auth-token", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandShellAuthToken, shellToken.Command)
	require.Equal(t, "shell", shellToken.TokenType)
	require.NotEmpty(t, shellToken.TokenID)

	terminate, err := runtime.Command(context.Background(), runtimemicrovm.CommandTerminate, "req-terminate", binding)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.StateTerminated, terminate.State)

	calls := provider.Calls()
	require.Len(t, calls, 8)
	require.Equal(t, []runtimemicrovm.Operation{
		runtimemicrovm.OperationRun,
		runtimemicrovm.OperationGet,
		runtimemicrovm.OperationList,
		runtimemicrovm.OperationSuspend,
		runtimemicrovm.OperationResume,
		runtimemicrovm.OperationAuthToken,
		runtimemicrovm.OperationShellAuthToken,
		runtimemicrovm.OperationTerminate,
	}, []runtimemicrovm.Operation{calls[0].Operation, calls[1].Operation, calls[2].Operation, calls[3].Operation, calls[4].Operation, calls[5].Operation, calls[6].Operation, calls[7].Operation})
	for _, call := range calls {
		require.Equal(t, "slug:demo", call.TenantID)
		require.Equal(t, MicroVMNamespace, call.Namespace)
		if call.Operation != runtimemicrovm.OperationList {
			require.Equal(t, "conv_123", call.SessionID)
		}
	}
}

func TestMicroVMControllerRuntimeSafeEnvelopeRejectsForbiddenFields(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:            microvmtestkit.NewFakeProvider(),
		Registry:            runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:            "image-ref",
		NetworkConnectorRef: "network-ref",
	})
	require.NoError(t, err)

	req, err := NewMicroVMRunRequest("req-forbidden", binding, "image-ref", "network-ref", nil, nil)
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
	provider := microvmtestkit.NewFakeProvider()
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{Provider: provider, Registry: runtimemicrovm.NewMemorySessionRegistry(), ImageRef: "image-ref", NetworkConnectorRef: "network-ref"})
	require.NoError(t, err)

	run, err := runtime.Run(context.Background(), "req-run", binding)
	require.NoError(t, err)
	ref, err := MicroVMLifecycleRefFromResponse(binding, run, time.Date(2026, 6, 25, 18, 1, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, MicroVMSourceOfTruth, ref.SourceOfTruth)
	require.Equal(t, "slug:demo", ref.TenantID)
	require.Equal(t, "conv_123", ref.SessionID)
	require.Equal(t, runtimemicrovm.CommandRun, ref.LastAction)
	require.Equal(t, run.ProviderMicroVMID, ref.MicroVMID)

	get, err := runtime.Command(context.Background(), runtimemicrovm.CommandGet, "req-get", binding)
	require.NoError(t, err)
	reconciled, err := ReconcileMicroVMRegistryStatus(binding, ref, runtimemicrovm.SessionStatus{
		TenantID:        get.TenantID,
		Namespace:       get.Namespace,
		SessionID:       get.SessionID,
		State:           get.State,
		DesiredState:    get.DesiredState,
		LifecycleState:  get.LifecycleState,
		MicroVMID:       get.ProviderMicroVMID,
		LastAction:      get.Command,
		LastTransition:  time.Date(2026, 6, 25, 18, 2, 0, 0, time.UTC),
		RegistryVersion: get.RegistryVersion,
	})
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandGet, reconciled.LastAction)

	_, err = ReconcileMicroVMRegistryStatus(binding, ref, runtimemicrovm.SessionStatus{
		TenantID:        "slug:other",
		Namespace:       MicroVMNamespace,
		SessionID:       "conv_123",
		State:           runtimemicrovm.StateRunning,
		DesiredState:    runtimemicrovm.StateRunning,
		LifecycleState:  runtimemicrovm.StateRunning,
		LastAction:      runtimemicrovm.CommandGet,
		LastTransition:  time.Now().UTC(),
		RegistryVersion: 1,
	})
	require.ErrorIs(t, err, ErrStaleMicroVMRegistryState)

	lostRegistryRuntime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{Provider: microvmtestkit.NewFakeProvider(), Registry: runtimemicrovm.NewMemorySessionRegistry(), ImageRef: "image-ref", NetworkConnectorRef: "network-ref"})
	require.NoError(t, err)
	_, err = lostRegistryRuntime.Command(context.Background(), runtimemicrovm.CommandGet, "req-lost", binding)
	require.Error(t, err, "registry/cache loss must not invent Host business state without reconstruction")
	require.NoError(t, ref.Validate(binding), "Host can still reconstruct the MicroVM binding from HostedGenesisSession truth")
}

func TestMicroVMControllerRuntimeUsesAppTheoryM16WithoutLocalAdapter(t *testing.T) {
	t.Parallel()

	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{
		Provider:            microvmtestkit.NewFakeProvider(),
		Registry:            runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:            "image-ref",
		NetworkConnectorRef: "network-ref",
	})
	require.NoError(t, err)
	require.NotNil(t, runtime.Controller())
	require.NoError(t, ValidateAppTheoryMicroVMContracts())

	_, currentFile, _, ok := runtimepkgCaller()
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
		retiredAdapter := "ProvisionalDogfood" + "MicroVMClient"
		require.NotContains(t, source, retiredAdapter, "AppTheory M16 adoption must retire Host's provisional adapter")
		forbiddenEscape := "RawAWSSDK" + ": true"
		require.NotContains(t, source, forbiddenEscape, "AppTheory raw SDK escape hatch must remain disabled")
	}
}

func TestMicroVMLabCanaryHarnessExercisesM16LifecycleAndSecretChecks(t *testing.T) {
	t.Parallel()

	binding := testMicroVMBinding()
	provider := microvmtestkit.NewFakeProviderWithTime(time.Date(2026, 6, 25, 19, 0, 0, 0, time.UTC))
	runtime, err := NewMicroVMControllerRuntime(MicroVMControllerRuntimeConfig{Provider: provider, Registry: runtimemicrovm.NewMemorySessionRegistry(), ImageRef: "image-ref", NetworkConnectorRef: "network-ref"})
	require.NoError(t, err)

	responses := make([]runtimemicrovm.ControllerResponse, 0, 8)
	for _, step := range []struct {
		command runtimemicrovm.Command
		request string
	}{
		{runtimemicrovm.CommandRun, "canary-run"},
		{runtimemicrovm.CommandGet, "canary-get"},
		{runtimemicrovm.CommandList, "canary-list"},
		{runtimemicrovm.CommandSuspend, "canary-suspend"},
		{runtimemicrovm.CommandResume, "canary-resume"},
		{runtimemicrovm.CommandAuthToken, "canary-auth-token"},
		{runtimemicrovm.CommandShellAuthToken, "canary-shell-auth-token"},
		{runtimemicrovm.CommandTerminate, "canary-terminate"},
	} {
		var resp runtimemicrovm.ControllerResponse
		if step.command == runtimemicrovm.CommandRun {
			resp, err = runtime.Run(context.Background(), step.request, binding)
		} else {
			resp, err = runtime.Command(context.Background(), step.command, step.request, binding)
		}
		require.NoError(t, err)
		responses = append(responses, resp)
	}

	evidence, err := json.MarshalIndent(struct {
		Canary string                              `json:"canary"`
		Steps  []runtimemicrovm.ControllerResponse `json:"steps"`
	}{Canary: "hosted-genesis-microvm-m16-non-deploying", Steps: responses}, "", "  ")
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
		"provider_token",
		"token_value",
		"wallet_signature",
		"raw transcript",
		"endpoint_token",
		"x-aws-proxy-auth",
	} {
		require.NotContains(t, lower, forbidden)
	}
}

func runtimepkgCaller() (uintptr, string, int, bool) { return runtime.Caller(0) }

func testMicroVMBinding() MicroVMSessionBinding {
	return MicroVMSessionBinding{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		ConversationID: "conv_123",
		TurnID:         "turn_123",
	}
}
