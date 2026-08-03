package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/v3/runtime/microvm"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHostedGenesisMicroVMReconstructionHookLoadsTenantBoundSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", ctx).Return(db).Once()
	db.On("Model", mock.AnythingOfType("*models.HostedGenesisSession")).Return(q).Once()
	q.On("Where", "PK", "=", "HOSTED_GENESIS#INSTANCE#demo").Return(q).Once()
	q.On("Where", "SK", "=", "SESSION#conv_123").Return(q).Once()
	q.On("ConsistentRead").Return(q).Once()
	q.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		*dest = *validHostedGenesisMicroVMReconstructionSession(t)
	}).Once()

	hook := New(db).HostedGenesisMicroVMReconstructionHook(HostedGenesisMicroVMReconstructionConfig{
		ImageRef:                    "image-ref-from-config",
		NetworkConnectorRef:         "network-ref-from-config",
		IngressNetworkConnectorRefs: []string{" ingress-ref ", ""},
		EgressNetworkConnectorRefs:  []string{" egress-ref "},
		ControllerID:                "controller-from-config",
		TTL:                         30 * time.Minute,
	})
	got, err := hook(ctx, runtimemicrovm.SessionReconstructionRequest{
		TenantID:    "slug:demo",
		Namespace:   hostedgenesis.MicroVMNamespace,
		SessionID:   "conv_123",
		RequestID:   "req-reconstruct",
		AuthSubject: "subject",
		Now:         time.Date(2026, 7, 7, 15, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, "slug:demo", got.TenantID)
	require.Equal(t, "conv_123", got.SessionID)
	require.Equal(t, "microvm-123", got.ProviderMicroVMID)
	require.Equal(t, "image-ref-from-config", got.ImageRef)
	require.Equal(t, "network-ref-from-config", got.NetworkConnectorRef)
	require.Equal(t, []string{"ingress-ref"}, got.IngressNetworkConnectorRefs)
	require.Equal(t, "controller-from-config", got.ControllerID)
	require.Equal(t, "req-reconstruct", got.LastCommandID)
	require.Equal(t, "reg_123", got.Metadata["registration_id"])

	db.AssertExpectations(t)
	q.AssertExpectations(t)
}

func TestHostedGenesisMicroVMReconstructionHookFailsClosedForInvalidOrUnavailableStore(t *testing.T) {
	t.Parallel()

	cfg := HostedGenesisMicroVMReconstructionConfig{ImageRef: "image-ref", NetworkConnectorRef: "network-ref"}
	hook := New(ttmocks.NewMockExtendedDBStrict()).HostedGenesisMicroVMReconstructionHook(cfg)
	_, err := hook(context.Background(), runtimemicrovm.SessionReconstructionRequest{
		TenantID:  "account:demo",
		Namespace: hostedgenesis.MicroVMNamespace,
		SessionID: "conv_123",
	})
	require.Error(t, err)

	var store *Store
	hook = store.HostedGenesisMicroVMReconstructionHook(cfg)
	_, err = hook(context.Background(), runtimemicrovm.SessionReconstructionRequest{
		TenantID:  "slug:demo",
		Namespace: hostedgenesis.MicroVMNamespace,
		SessionID: "conv_123",
	})
	require.Error(t, err)

	require.Equal(t, "", slugFromMicroVMTenantID("account:demo"))
	require.Equal(t, "demo", slugFromMicroVMTenantID(" slug:Demo "))
}

func TestReconstructMicroVMRegistryRecordFromHostedGenesisSessionFailureBranches(t *testing.T) {
	t.Parallel()

	request := runtimemicrovm.SessionReconstructionRequest{TenantID: "slug:demo", Namespace: hostedgenesis.MicroVMNamespace, SessionID: "conv_123"}
	cfg := HostedGenesisMicroVMReconstructionConfig{ImageRef: "image-ref", NetworkConnectorRef: "network-ref"}

	_, err := ReconstructMicroVMRegistryRecordFromHostedGenesisSession(nil, request, cfg)
	require.Error(t, err)

	session := validHostedGenesisMicroVMReconstructionSession(t)
	session.MicroVMLifecycleRef = nil
	_, err = ReconstructMicroVMRegistryRecordFromHostedGenesisSession(session, request, cfg)
	require.Error(t, err)

	session = validHostedGenesisMicroVMReconstructionSession(t)
	request.SessionID = "other"
	_, err = ReconstructMicroVMRegistryRecordFromHostedGenesisSession(session, request, cfg)
	require.Error(t, err)
}

func validHostedGenesisMicroVMReconstructionSession(t *testing.T) *models.HostedGenesisSession {
	t.Helper()

	now := time.Date(2026, 7, 7, 14, 0, 0, 0, time.UTC)
	session := &models.HostedGenesisSession{
		InstanceSlug:   "demo",
		RegistrationID: "reg_123",
		AgentID:        "agent_123",
		ConversationID: "conv_123",
		Status:         string(hostedgenesis.StatusInProgress),
		LatestTurnID:   "turn_123",
		MessageCount:   1,
		RequestID:      "req-original",
		CreatedAt:      now,
		UpdatedAt:      now.Add(time.Minute),
		Version:        2,
	}
	binding := session.MicroVMSessionBinding()
	ref, err := hostedgenesis.MicroVMLifecycleRefFromResponse(binding, runtimemicrovm.ControllerResponse{
		Command:           runtimemicrovm.CommandRun,
		RequestID:         "req-run",
		TenantID:          binding.TenantID(),
		Namespace:         hostedgenesis.MicroVMNamespace,
		SessionID:         binding.ConversationID,
		State:             runtimemicrovm.StateRunning,
		LifecycleState:    runtimemicrovm.StateRunning,
		ProviderMicroVMID: "microvm-123",
		LastAction:        runtimemicrovm.CommandRun,
		LastTransition:    now.Add(2 * time.Minute),
		RegistryVersion:   7,
	}, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.NoError(t, session.ApplyMicroVMLifecycleRef(ref))
	return session
}
