package hostedgenesis

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	microvmtestkit "github.com/theory-cloud/apptheory/testkit/microvm"
)

func TestMicroVMCreateRequestUsesAppTheoryContract(t *testing.T) {
	require.NoError(t, ValidateAppTheoryMicroVMContracts())

	binding := MicroVMSessionBinding{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		ConversationID: "conv_123",
		TurnID:         "turn_123",
	}
	req, err := NewMicroVMCreateRequest("req_123", binding, "image-arn", "connector-arn")
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandCreate, req.Command)
	require.Equal(t, "slug:demo", req.TenantID)
	require.Equal(t, MicroVMNamespace, req.Namespace)
	require.Equal(t, "conv_123", req.SessionID)
	require.Equal(t, MicroVMSourceOfTruth, req.SessionSpec.Metadata["source_of_truth"])
	require.NotContains(t, req.SessionSpec.Metadata, "bearer_token")
	require.NotContains(t, req.SessionSpec.Metadata, "raw_aws_credentials")

	controller, err := runtimemicrovm.NewController(
		microvmtestkit.NewFakeClient(),
		runtimemicrovm.WithControllerID(MicroVMControllerID),
	)
	require.NoError(t, err)
	resp, err := controller.Handle(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.CommandCreate, resp.Command)
	require.Equal(t, runtimemicrovm.StateRequested, resp.State)
	require.Equal(t, "conv_123", resp.SessionID)
	require.Equal(t, int64(1), resp.RegistryVersion)
}

func TestMicroVMCommandRequestMapsExistingSession(t *testing.T) {
	binding := MicroVMSessionBinding{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		ConversationID: "conv_123",
	}
	for _, command := range []runtimemicrovm.Command{
		runtimemicrovm.CommandStart,
		runtimemicrovm.CommandStop,
		runtimemicrovm.CommandStatus,
		runtimemicrovm.CommandSession,
	} {
		t.Run(string(command), func(t *testing.T) {
			req, err := NewMicroVMCommandRequest(command, "req_123", binding)
			require.NoError(t, err)
			require.Equal(t, command, req.Command)
			require.Equal(t, "slug:demo", req.TenantID)
			require.Equal(t, MicroVMNamespace, req.Namespace)
			require.Equal(t, "conv_123", req.SessionID)
			require.Empty(t, req.SessionSpec.Metadata)
		})
	}

	_, err := NewMicroVMCommandRequest(runtimemicrovm.CommandCreate, "req_123", binding)
	require.Error(t, err)
}

func TestMicroVMBindingFailsClosedWhenUnbound(t *testing.T) {
	_, err := NewMicroVMCreateRequest("req_123", MicroVMSessionBinding{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		// Missing conversation id would break DynamoDB HostedGenesisSession recovery.
	}, "image-arn", "connector-arn")
	require.Error(t, err)

	_, err = NewMicroVMCreateRequest("req_123", MicroVMSessionBinding{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		ConversationID: "conv_123",
	}, "", "connector-arn")
	require.Error(t, err)
}
