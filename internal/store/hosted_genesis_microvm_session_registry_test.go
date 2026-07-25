package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/v2/runtime/microvm"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHostedGenesisMicroVMRegistryPutUsesHostExecutionModel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", ctx).Return(db).Once()
	db.On("Model", mock.MatchedBy(func(item any) bool {
		exec, ok := item.(*models.HostedGenesisMicroVMExecution)
		return ok &&
			exec.PK == "HOSTED_GENESIS_MICROVM#INSTANCE#demo#NAMESPACE#hosted-genesis" &&
			exec.SK == "SESSION#conv_123" &&
			exec.TenantID == "slug:demo" &&
			exec.Namespace == hostedgenesis.MicroVMNamespace
	})).Return(q).Once()
	q.On("CreateOrUpdate").Return(nil).Once()

	registry, err := NewHostedGenesisMicroVMRegistry(New(db))
	require.NoError(t, err)
	got, err := registry.Put(ctx, validStoreMicroVMExecutionRecord("conv_123"))
	require.NoError(t, err)
	require.Equal(t, "slug:demo", got.TenantID)
	require.Equal(t, "conv_123", got.SessionID)

	db.AssertExpectations(t)
	q.AssertExpectations(t)
}

func TestHostedGenesisMicroVMRegistryGetListAndDeleteUseSemanticKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	getQ := new(ttmocks.MockQuery)
	listQ := new(ttmocks.MockQuery)
	deleteQ := new(ttmocks.MockQuery)

	db.On("WithContext", ctx).Return(db).Times(3)
	db.On("Model", mock.AnythingOfType("*models.HostedGenesisMicroVMExecution")).Return(getQ).Once()
	getQ.On("Where", "PK", "=", "HOSTED_GENESIS_MICROVM#INSTANCE#demo#NAMESPACE#hosted-genesis").Return(getQ).Once()
	getQ.On("Where", "SK", "=", "SESSION#conv_123").Return(getQ).Once()
	getQ.On("ConsistentRead").Return(getQ).Once()
	getQ.On("First", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.HostedGenesisMicroVMExecution](t, args, 0)
		item, err := models.NewHostedGenesisMicroVMExecutionFromSessionRecord(validStoreMicroVMExecutionRecord("conv_123"))
		require.NoError(t, err)
		*dest = *item
	}).Once()

	db.On("Model", mock.AnythingOfType("*models.HostedGenesisMicroVMExecution")).Return(listQ).Once()
	listQ.On("Where", "PK", "=", "HOSTED_GENESIS_MICROVM#INSTANCE#demo#NAMESPACE#hosted-genesis").Return(listQ).Once()
	listQ.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.HostedGenesisMicroVMExecution](t, args, 0)
		b, err := models.NewHostedGenesisMicroVMExecutionFromSessionRecord(validStoreMicroVMExecutionRecord("conv_b"))
		require.NoError(t, err)
		a, err := models.NewHostedGenesisMicroVMExecutionFromSessionRecord(validStoreMicroVMExecutionRecord("conv_a"))
		require.NoError(t, err)
		*dest = []*models.HostedGenesisMicroVMExecution{b, a}
	}).Once()

	db.On("Model", mock.AnythingOfType("*models.HostedGenesisMicroVMExecution")).Return(deleteQ).Once()
	deleteQ.On("Where", "PK", "=", "HOSTED_GENESIS_MICROVM#INSTANCE#demo#NAMESPACE#hosted-genesis").Return(deleteQ).Once()
	deleteQ.On("Where", "SK", "=", "SESSION#conv_123").Return(deleteQ).Once()
	deleteQ.On("Delete").Return(nil).Once()

	registry, err := NewHostedGenesisMicroVMRegistry(New(db))
	require.NoError(t, err)
	key := runtimemicrovm.SessionKey{TenantID: "slug:demo", Namespace: hostedgenesis.MicroVMNamespace, SessionID: "conv_123"}

	got, err := registry.Get(ctx, key)
	require.NoError(t, err)
	require.Equal(t, "conv_123", got.SessionID)

	listed, err := registry.List(ctx, runtimemicrovm.SessionListInput{TenantID: "slug:demo", Namespace: hostedgenesis.MicroVMNamespace})
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.Equal(t, "conv_a", listed[0].SessionID)
	require.Equal(t, "conv_b", listed[1].SessionID)

	require.NoError(t, registry.Delete(ctx, key))

	db.AssertExpectations(t)
	getQ.AssertExpectations(t)
	listQ.AssertExpectations(t)
	deleteQ.AssertExpectations(t)
}

func TestHostedGenesisMicroVMRegistryRejectsUnboundKeys(t *testing.T) {
	t.Parallel()

	registry, err := NewHostedGenesisMicroVMRegistry(New(ttmocks.NewMockExtendedDBStrict()))
	require.NoError(t, err)
	_, err = registry.Get(context.Background(), runtimemicrovm.SessionKey{TenantID: "account:demo", Namespace: hostedgenesis.MicroVMNamespace, SessionID: "conv_123"})
	require.Error(t, err)
	_, err = registry.List(context.Background(), runtimemicrovm.SessionListInput{TenantID: "slug:demo"})
	require.Error(t, err)
	require.Error(t, registry.Delete(context.Background(), runtimemicrovm.SessionKey{TenantID: "slug:demo", Namespace: hostedgenesis.MicroVMNamespace}))
}

func TestHostedGenesisMicroVMRegistryAndRepositoryGuards(t *testing.T) {
	t.Parallel()

	_, err := NewHostedGenesisMicroVMRegistry(nil)
	require.Error(t, err)
	_, err = NewHostedGenesisMicroVMRegistry(&Store{})
	require.Error(t, err)

	var registry *HostedGenesisMicroVMRegistry
	_, err = registry.Put(context.Background(), validStoreMicroVMExecutionRecord("conv_123"))
	require.Error(t, err)
	_, err = registry.Get(context.Background(), runtimemicrovm.SessionKey{TenantID: "slug:demo", Namespace: hostedgenesis.MicroVMNamespace, SessionID: "conv_123"})
	require.Error(t, err)
	_, err = registry.List(context.Background(), runtimemicrovm.SessionListInput{TenantID: "slug:demo", Namespace: hostedgenesis.MicroVMNamespace})
	require.Error(t, err)
	require.Error(t, registry.Delete(context.Background(), runtimemicrovm.SessionKey{TenantID: "slug:demo", Namespace: hostedgenesis.MicroVMNamespace, SessionID: "conv_123"}))

	var store *Store
	require.Error(t, store.PutHostedGenesisMicroVMExecution(context.Background(), nil))
	_, err = store.ListHostedGenesisMicroVMExecutions(context.Background(), "demo", hostedgenesis.MicroVMNamespace)
	require.Error(t, err)
	require.Error(t, store.DeleteHostedGenesisMicroVMExecution(context.Background(), "demo", hostedgenesis.MicroVMNamespace, "conv_123"))

	store = New(ttmocks.NewMockExtendedDBStrict())
	require.Error(t, store.PutHostedGenesisMicroVMExecution(context.Background(), nil))
	_, err = store.GetHostedGenesisMicroVMExecution(context.Background(), "", hostedgenesis.MicroVMNamespace, "conv_123")
	require.True(t, theoryErrors.IsNotFound(err))
	_, err = store.GetHostedGenesisMicroVMExecution(context.Background(), "demo", "", "conv_123")
	require.True(t, theoryErrors.IsNotFound(err))
	_, err = store.GetHostedGenesisMicroVMExecution(context.Background(), "demo", hostedgenesis.MicroVMNamespace, "")
	require.True(t, theoryErrors.IsNotFound(err))
	_, err = store.ListHostedGenesisMicroVMExecutions(context.Background(), "", hostedgenesis.MicroVMNamespace)
	require.Error(t, err)
	require.Error(t, store.DeleteHostedGenesisMicroVMExecution(context.Background(), "demo", hostedgenesis.MicroVMNamespace, ""))
}

func TestHostedGenesisMicroVMExecutionRepositoryListCacheMissReturnsEmpty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", ctx).Return(db).Once()
	db.On("Model", mock.AnythingOfType("*models.HostedGenesisMicroVMExecution")).Return(q).Once()
	q.On("Where", "PK", "=", "HOSTED_GENESIS_MICROVM#INSTANCE#demo#NAMESPACE#hosted-genesis").Return(q).Once()
	q.On("All", mock.Anything).Return(theoryErrors.ErrItemNotFound).Once()

	got, err := New(db).ListHostedGenesisMicroVMExecutions(ctx, " demo ", " hosted-genesis ")
	require.NoError(t, err)
	require.Empty(t, got)

	db.AssertExpectations(t)
	q.AssertExpectations(t)
}

func TestHostedGenesisMicroVMRegistryPutDefaultsNilContext(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", context.Background()).Return(db).Once()
	db.On("Model", mock.MatchedBy(func(item any) bool {
		exec, ok := item.(*models.HostedGenesisMicroVMExecution)
		return ok && exec.SessionID == "conv_123"
	})).Return(q).Once()
	q.On("CreateOrUpdate").Return(nil).Once()

	registry, err := NewHostedGenesisMicroVMRegistry(New(db))
	require.NoError(t, err)
	var nilCtx context.Context
	got, err := registry.Put(nilCtx, validStoreMicroVMExecutionRecord("conv_123"))
	require.NoError(t, err)
	require.Equal(t, "conv_123", got.SessionID)

	db.AssertExpectations(t)
	q.AssertExpectations(t)
}

func validStoreMicroVMExecutionRecord(sessionID string) runtimemicrovm.SessionRecord {
	now := time.Date(2026, 7, 7, 14, 0, 0, 0, time.UTC)
	return runtimemicrovm.SessionRecord{
		TenantID:                    "slug:demo",
		Namespace:                   hostedgenesis.MicroVMNamespace,
		SessionID:                   sessionID,
		State:                       runtimemicrovm.StateRunning,
		DesiredState:                runtimemicrovm.StateRunning,
		MicroVMID:                   "microvm-000001",
		ProviderID:                  runtimemicrovm.AWSLambdaMicroVMProviderID,
		ProviderMicroVMID:           "microvm-000001",
		ProviderState:               string(runtimemicrovm.StateRunning),
		AWSLifecycleState:           string(runtimemicrovm.StateRunning),
		ImageRef:                    "arn:aws:lambda:us-east-1:123456789012:microvm-image:hosted-genesis",
		NetworkConnectorRef:         "egress-ref",
		IngressNetworkConnectorRefs: []string{"ingress-ref"},
		EgressNetworkConnectorRefs:  []string{"egress-ref"},
		ControllerID:                hostedgenesis.MicroVMControllerID,
		CreatedAt:                   now,
		UpdatedAt:                   now.Add(time.Minute),
		LastObservedAt:              now.Add(time.Minute),
		ProviderStartedAt:           now.Add(10 * time.Second),
		ExpiresAt:                   now.Add(time.Hour),
		Generation:                  1,
		LastAction:                  runtimemicrovm.CommandRun,
		LastCommandID:               "req_123",
		AuthSubject:                 hostedgenesis.MicroVMAuthSubject,
		Metadata: map[string]string{
			"source_of_truth": hostedgenesis.MicroVMSourceOfTruth,
			"registration_id": "reg_123",
			"agent_id":        "agent_123",
			"conversation_id": sessionID,
			"turn_id":         "turn_123",
		},
	}
}
