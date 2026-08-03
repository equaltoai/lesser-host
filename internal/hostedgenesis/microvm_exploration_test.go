package hostedgenesis

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/v3/runtime/microvm"
	microvmtestkit "github.com/theory-cloud/apptheory/v3/testkit/microvm"
)

func TestMicroVMRunRequestUsesAppTheoryM16Contract(t *testing.T) {
	require.NoError(t, ValidateAppTheoryMicroVMContracts())

	binding := MicroVMSessionBinding{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		ConversationID: "conv_123",
		TurnID:         "turn_123",
	}
	req, err := NewMicroVMRunRequest("req_123", binding, "image-arn", "egress-connector", []string{"ingress-connector"}, []string{"egress-connector"})
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandRun, req.Command)
	require.Equal(t, "slug:demo", req.TenantID)
	require.Equal(t, MicroVMNamespace, req.Namespace)
	require.Equal(t, "conv_123", req.SessionID)
	require.Equal(t, MicroVMSourceOfTruth, req.SessionSpec.Metadata["source_of_truth"])
	require.Equal(t, []string{"ingress-connector"}, req.IngressNetworkConnectorRefs)
	require.Equal(t, []string{"egress-connector"}, req.EgressNetworkConnectorRefs)
	require.NotContains(t, req.SessionSpec.Metadata, "bearer_token")
	require.NotContains(t, req.SessionSpec.Metadata, "raw_aws_credentials")

	controller, err := runtimemicrovm.NewRealController(
		microvmtestkit.NewFakeProviderWithTime(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)),
		runtimemicrovm.NewMemorySessionRegistry(),
		runtimemicrovm.WithControllerID(MicroVMControllerID),
		runtimemicrovm.WithControllerLogging(runtimemicrovm.ProviderLogging{Disabled: true}),
	)
	require.NoError(t, err)
	resp, err := controller.Handle(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandRun, resp.Command)
	require.Equal(t, runtimemicrovm.StateRunning, resp.State)
	require.Equal(t, "conv_123", resp.SessionID)
	require.Equal(t, int64(1), resp.RegistryVersion)
}

func TestMicroVMOperationRequestMapsExistingSession(t *testing.T) {
	binding := MicroVMSessionBinding{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		ConversationID: "conv_123",
	}
	for _, command := range []runtimemicrovm.Command{
		runtimemicrovm.CommandGet,
		runtimemicrovm.CommandList,
		runtimemicrovm.CommandSuspend,
		runtimemicrovm.CommandResume,
		runtimemicrovm.CommandTerminate,
		runtimemicrovm.CommandAuthToken,
		runtimemicrovm.CommandShellAuthToken,
	} {
		t.Run(string(command), func(t *testing.T) {
			req, err := NewMicroVMOperationRequest(command, "req_123", binding)
			require.NoError(t, err)
			require.Equal(t, command, req.Command)
			require.Equal(t, "slug:demo", req.TenantID)
			require.Equal(t, MicroVMNamespace, req.Namespace)
			if command == runtimemicrovm.CommandList {
				require.Empty(t, req.SessionID)
			} else {
				require.Equal(t, "conv_123", req.SessionID)
			}
			if command == runtimemicrovm.CommandAuthToken {
				require.Equal(t, []runtimemicrovm.ProviderPortScope{{Port: 443}}, req.AllowedPortScope)
			}
			require.Empty(t, req.SessionSpec.Metadata)
		})
	}

	_, err := NewMicroVMOperationRequest(runtimemicrovm.Command("create"), "req_123", binding)
	require.Error(t, err)
	_, err = NewMicroVMOperationRequest(runtimemicrovm.Command("start"), "req_123", binding)
	require.Error(t, err)
}

func TestMicroVMBindingFailsClosedWhenUnbound(t *testing.T) {
	_, err := NewMicroVMRunRequest("req_123", MicroVMSessionBinding{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		// Missing conversation id would break DynamoDB HostedGenesisSession recovery.
	}, "image-arn", "connector-arn", nil, nil)
	require.Error(t, err)

	_, err = NewMicroVMRunRequest("req_123", MicroVMSessionBinding{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		ConversationID: "conv_123",
	}, "", "connector-arn", nil, nil)
	require.Error(t, err)
}
