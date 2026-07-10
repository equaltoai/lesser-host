package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const validStoreHostedGenesisSessionPK = "HOSTED_GENESIS#INSTANCE#demo"

func TestStore_GetHostedGenesisSessionScopesByTenantSlug(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", ctx).Return(db).Once()
	db.On("Model", mock.AnythingOfType("*models.HostedGenesisSession")).Return(q).Once()
	q.On("Where", "PK", "=", models.HostedGenesisSessionPK(" Demo ")).Return(q).Once()
	q.On("Where", "SK", "=", models.HostedGenesisSessionSK(" conv_123 ")).Return(q).Once()
	q.On("ConsistentRead").Return(q).Once()
	q.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		*dest = *validStoreHostedGenesisSession()
	}).Once()

	st := New(db)
	got, err := st.GetHostedGenesisSession(ctx, " Demo ", " conv_123 ")
	require.NoError(t, err)
	require.Equal(t, "demo", got.InstanceSlug)
	require.Equal(t, "conv_123", got.ConversationID)
	require.Equal(t, validStoreHostedGenesisSessionPK, got.PK)

	db.AssertExpectations(t)
	q.AssertExpectations(t)
}

func TestStore_CreateHostedGenesisSessionUsesTransactionalCreate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	tx := new(ttmocks.MockTransactionBuilder)
	db.TransactWriteBuilder = tx
	db.On("TransactWrite", ctx, mock.Anything).Return(nil).Once()
	tx.On("Create", mock.MatchedBy(func(item any) bool {
		session, ok := item.(*models.HostedGenesisSession)
		return ok && session.PK == validStoreHostedGenesisSessionPK && session.Version == 0
	}), mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return len(conditions) == 0
	})).Return(tx).Once()

	st := New(db)
	require.NoError(t, st.CreateHostedGenesisSession(ctx, validStoreHostedGenesisSession()))

	db.AssertExpectations(t)
	tx.AssertExpectations(t)
}

func TestStore_UpdateHostedGenesisSessionUsesExpectedVersion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	tx := new(ttmocks.MockTransactionBuilder)
	db.TransactWriteBuilder = tx
	db.On("TransactWrite", ctx, mock.Anything).Return(nil).Once()
	ub := &captureHostedGenesisUpdateBuilder{}
	tx.UpdateBuilder = ub
	tx.On("UpdateWithBuilder", mock.MatchedBy(func(item any) bool {
		session, ok := item.(*models.HostedGenesisSession)
		return ok && session.PK == validStoreHostedGenesisSessionPK && session.SK == "SESSION#conv_123"
	}), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return hasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
			hasVersionCondition(conditions, 7) &&
			hasStatusCondition(conditions, hostedgenesis.StatusInProgress)
	})).Return(tx).Once()

	st := New(db)
	session := validStoreHostedGenesisSession()
	session.Status = string(hostedgenesis.StatusAssistantTurnReady)
	require.NoError(t, st.UpdateHostedGenesisSession(ctx, session, 7, hostedgenesis.StatusInProgress))
	require.Equal(t, int64(1), ub.adds["Version"])
	require.Equal(t, string(hostedgenesis.StatusAssistantTurnReady), ub.sets["Status"])
	require.Len(t, ub.sets["TurnLedger"], 1)
	require.NotContains(t, ub.sets, "Version")

	db.AssertExpectations(t)
	tx.AssertExpectations(t)
}

func TestStore_UpdateHostedGenesisSessionPropagatesVersionConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	db.On("TransactWrite", ctx, mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()

	st := New(db)
	err := st.UpdateHostedGenesisSession(ctx, validStoreHostedGenesisSession(), 3, hostedgenesis.StatusInProgress)
	require.ErrorIs(t, err, theoryErrors.ErrConditionFailed)
	db.AssertExpectations(t)
}

func TestStore_UpdateHostedGenesisSessionRejectsIllegalTransitionBeforeWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	st := New(db)
	session := validStoreHostedGenesisSession()
	session.Status = string(hostedgenesis.StatusDeclarationReady)
	checkpoint := validStoreDeclarationCheckpoint()
	session.DeclarationCheckpoint = &checkpoint

	err := st.UpdateHostedGenesisSession(ctx, session, 7, hostedgenesis.StatusCreated)
	require.ErrorIs(t, err, hostedgenesis.ErrInvalidStatusTransition)
	db.AssertExpectations(t)
}

func TestStore_FailHostedGenesisSessionAndConversationUsesOneGuardedTransaction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	tx := new(ttmocks.MockTransactionBuilder)
	db.TransactWriteBuilder = tx
	db.On("TransactWrite", ctx, mock.Anything).Return(nil).Once()
	tx.UpdateBuilder = &captureHostedGenesisUpdateBuilder{}
	tx.On("UpdateWithBuilder", mock.MatchedBy(func(item any) bool {
		session, ok := item.(*models.HostedGenesisSession)
		return ok && session.PK == validStoreHostedGenesisSessionPK && session.Status == string(hostedgenesis.StatusFailed)
	}), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return hasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
			hasVersionCondition(conditions, 7) &&
			hasStatusCondition(conditions, hostedgenesis.StatusInProgress)
	})).Return(tx).Once()
	tx.On("UpdateWithBuilder", mock.MatchedBy(func(item any) bool {
		conversation, ok := item.(*models.SoulAgentMintConversation)
		return ok && conversation.PK == "SOUL#AGENT#0x2222222222222222222222222222222222222222222222222222222222222222" &&
			conversation.SK == "MINT_CONVERSATION#conv_123" &&
			conversation.Status == models.SoulMintConversationStatusFailed
	}), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return len(conditions) == 1 && hasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists)
	})).Return(tx).Once()

	session, conversation := validStoreHostedGenesisFailure()
	st := New(db)
	require.NoError(t, st.FailHostedGenesisSessionAndConversation(ctx, session, 7, hostedgenesis.StatusInProgress, conversation))

	db.AssertExpectations(t)
	tx.AssertExpectations(t)
}

func TestStore_FailHostedGenesisSessionAndConversationPropagatesTransactionFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	db.On("TransactWrite", ctx, mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()
	session, conversation := validStoreHostedGenesisFailure()

	err := New(db).FailHostedGenesisSessionAndConversation(ctx, session, 7, hostedgenesis.StatusInProgress, conversation)
	require.ErrorIs(t, err, theoryErrors.ErrConditionFailed)
	db.AssertExpectations(t)
}

func TestStore_FailHostedGenesisSessionAndConversationRejectsMismatchedIdentityBeforeWrite(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	session, conversation := validStoreHostedGenesisFailure()
	conversation.AgentID = "0x" + strings.Repeat("33", 32)

	err := New(db).FailHostedGenesisSessionAndConversation(context.Background(), session, 7, hostedgenesis.StatusInProgress, conversation)
	require.ErrorContains(t, err, "matching session and conversation identity")
	db.AssertExpectations(t)
}

func validStoreHostedGenesisFailure() (*models.HostedGenesisSession, *models.SoulAgentMintConversation) {
	now := time.Date(2026, 6, 24, 12, 5, 0, 0, time.UTC)
	session := validStoreHostedGenesisSession()
	session.Status = string(hostedgenesis.StatusFailed)
	session.Failure = &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeMicroVMUnavailable,
		Message:   "hosted genesis MicroVM unavailable",
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 30,
			Reason:            "microvm_unavailable",
		},
	}
	session.CompletedAt = now
	conversation := &models.SoulAgentMintConversation{
		AgentID:        session.AgentID,
		ConversationID: session.ConversationID,
		Model:          "deterministic",
		Status:         models.SoulMintConversationStatusFailed,
		StatusReason:   "microvm_unavailable",
		RequestID:      session.RequestID,
		CreatedAt:      session.CreatedAt,
		UpdatedAt:      now,
		CompletedAt:    now,
	}
	return session, conversation
}

func validStoreHostedGenesisSession() *models.HostedGenesisSession {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	session := &models.HostedGenesisSession{
		InstanceSlug:   " Demo ",
		RegistrationID: "reg_123",
		AgentID:        "0x2222222222222222222222222222222222222222222222222222222222222222",
		ConversationID: "conv_123",
		Status:         string(hostedgenesis.StatusInProgress),
		LatestTurnID:   "turn_123",
		MessageCount:   1,
		TurnLedger: []hostedgenesis.TurnLedgerEntry{{
			TurnID:           "turn_123",
			IdempotencyKey:   "idem_123",
			RequestHash:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			BillingLedgerRef: "usage:mint:conv_123:turn_123",
			ChargedCredits:   1,
			MessageCount:     1,
			AcceptedAt:       now,
		}},
		InputCheckpointRef: "checkpoint://hosted-genesis/input_123",
		RequestID:          "req_123",
		TraceIDs:           &hostedgenesis.TraceIDs{HostRequestID: "req_123", CorrelationID: "corr_123", IdempotencyKey: "idem_123"},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	_ = session.BeforeCreate()
	return session
}

func validStoreDeclarationCheckpoint() hostedgenesis.DeclarationCheckpoint {
	return hostedgenesis.DeclarationCheckpoint{
		DeclarationID:   "decl_123",
		DeclarationHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CheckpointRef:   "checkpoint://hosted-genesis/decl_123",
		ProducedAt:      time.Date(2026, 6, 24, 12, 3, 0, 0, time.UTC),
		RegistrationID:  "reg_123",
		ConversationID:  "conv_123",
		AgentID:         "0x2222222222222222222222222222222222222222222222222222222222222222",
		MessageCount:    2,
		Model:           "openai:gpt-5.4",
		RequestID:       "req_123",
	}
}

func hasConditionKind(conditions []core.TransactCondition, kind core.TransactConditionKind) bool {
	for _, condition := range conditions {
		if condition.Kind == kind {
			return true
		}
	}
	return false
}

func hasVersionCondition(conditions []core.TransactCondition, version int64) bool {
	for _, condition := range conditions {
		if condition.Kind == core.TransactConditionKindVersionEquals && condition.Value == version {
			return true
		}
	}
	return false
}

func hasStatusCondition(conditions []core.TransactCondition, status hostedgenesis.Status) bool {
	for _, condition := range conditions {
		if condition.Kind == core.TransactConditionKindField &&
			condition.Field == "Status" &&
			condition.Operator == "=" &&
			condition.Value == string(status) {
			return true
		}
	}
	return false
}

type captureHostedGenesisUpdateBuilder struct {
	sets map[string]any
	adds map[string]any
}

func (b *captureHostedGenesisUpdateBuilder) ensure() {
	if b.sets == nil {
		b.sets = map[string]any{}
	}
	if b.adds == nil {
		b.adds = map[string]any{}
	}
}

func (b *captureHostedGenesisUpdateBuilder) Set(field string, value any) core.UpdateBuilder {
	b.ensure()
	b.sets[field] = value
	return b
}
func (b *captureHostedGenesisUpdateBuilder) SetIfNotExists(field string, value any, _ any) core.UpdateBuilder {
	return b.Set(field, value)
}
func (b *captureHostedGenesisUpdateBuilder) Add(field string, value any) core.UpdateBuilder {
	b.ensure()
	b.adds[field] = value
	return b
}
func (b *captureHostedGenesisUpdateBuilder) Increment(field string) core.UpdateBuilder {
	return b.Add(field, int64(1))
}
func (b *captureHostedGenesisUpdateBuilder) Decrement(field string) core.UpdateBuilder {
	return b.Add(field, int64(-1))
}
func (b *captureHostedGenesisUpdateBuilder) Remove(_ string) core.UpdateBuilder        { return b }
func (b *captureHostedGenesisUpdateBuilder) Delete(_ string, _ any) core.UpdateBuilder { return b }
func (b *captureHostedGenesisUpdateBuilder) AppendToList(_ string, _ any) core.UpdateBuilder {
	return b
}
func (b *captureHostedGenesisUpdateBuilder) PrependToList(_ string, _ any) core.UpdateBuilder {
	return b
}
func (b *captureHostedGenesisUpdateBuilder) RemoveFromListAt(_ string, _ int) core.UpdateBuilder {
	return b
}
func (b *captureHostedGenesisUpdateBuilder) SetListElement(_ string, _ int, _ any) core.UpdateBuilder {
	return b
}
func (b *captureHostedGenesisUpdateBuilder) Condition(_ string, _ string, _ any) core.UpdateBuilder {
	return b
}
func (b *captureHostedGenesisUpdateBuilder) OrCondition(_ string, _ string, _ any) core.UpdateBuilder {
	return b
}
func (b *captureHostedGenesisUpdateBuilder) ConditionExists(_ string) core.UpdateBuilder    { return b }
func (b *captureHostedGenesisUpdateBuilder) ConditionNotExists(_ string) core.UpdateBuilder { return b }
func (b *captureHostedGenesisUpdateBuilder) ConditionVersion(_ int64) core.UpdateBuilder    { return b }
func (b *captureHostedGenesisUpdateBuilder) ReturnValues(_ string) core.UpdateBuilder       { return b }
func (b *captureHostedGenesisUpdateBuilder) Execute() error                                 { return nil }
func (b *captureHostedGenesisUpdateBuilder) ExecuteWithResult(_ any) error                  { return nil }

func TestReconstructMicroVMRegistryRecordFromHostedGenesisSessionTruth(t *testing.T) {
	t.Parallel()

	session := validStoreHostedGenesisSession()
	binding := session.MicroVMSessionBinding()
	now := time.Date(2026, 6, 25, 21, 0, 0, 0, time.UTC)
	ref := hostedgenesis.MicroVMLifecycleRef{
		SourceOfTruth:       hostedgenesis.MicroVMSourceOfTruth,
		TenantID:            binding.TenantID(),
		Namespace:           hostedgenesis.MicroVMNamespace,
		SessionID:           session.ConversationID,
		LifecycleState:      runtimemicrovm.StateRunning,
		DesiredState:        runtimemicrovm.StateRunning,
		MicroVMID:           "provider-microvm-123",
		ImageRef:            "image-from-host-ref",
		NetworkConnectorRef: "egress-from-host-ref",
		LastAction:          runtimemicrovm.CommandRun,
		LastTransition:      now,
		RegistryVersion:     3,
		UpdatedAt:           now,
	}
	require.NoError(t, session.ApplyMicroVMLifecycleRef(ref))

	record, err := ReconstructMicroVMRegistryRecordFromHostedGenesisSession(session, runtimemicrovm.SessionReconstructionRequest{
		RequestID:   "req-reconstruct",
		TenantID:    binding.TenantID(),
		Namespace:   hostedgenesis.MicroVMNamespace,
		SessionID:   session.ConversationID,
		AuthSubject: hostedgenesis.MicroVMAuthSubject,
		Now:         now,
	}, HostedGenesisMicroVMReconstructionConfig{
		ImageRef:                    "image-fallback",
		NetworkConnectorRef:         "egress-fallback",
		IngressNetworkConnectorRefs: []string{"ingress-ref"},
		EgressNetworkConnectorRefs:  []string{"egress-ref"},
		TTL:                         time.Hour,
	})
	require.NoError(t, err)
	require.Equal(t, binding.TenantID(), record.TenantID)
	require.Equal(t, hostedgenesis.MicroVMNamespace, record.Namespace)
	require.Equal(t, session.ConversationID, record.SessionID)
	require.Equal(t, "provider-microvm-123", record.ProviderMicroVMID)
	require.Equal(t, runtimemicrovm.AWSLambdaMicroVMProviderID, record.ProviderID)
	require.Equal(t, "image-from-host-ref", record.ImageRef)
	require.Equal(t, "egress-from-host-ref", record.NetworkConnectorRef)
	require.Equal(t, []string{"ingress-ref"}, record.IngressNetworkConnectorRefs)
	require.Equal(t, []string{"egress-ref"}, record.EgressNetworkConnectorRefs)
	require.Equal(t, hostedgenesis.MicroVMSourceOfTruth, record.Metadata["source_of_truth"])
	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"bearer_token", "provider_token", "token_value", "raw transcript", "aws_secret_access_key"} {
		require.NotContains(t, lower, forbidden)
	}
}

func TestReconstructMicroVMRegistryRecordFailsClosedOnMismatchAndMissingTruth(t *testing.T) {
	t.Parallel()

	session := validStoreHostedGenesisSession()
	binding := session.MicroVMSessionBinding()
	_, err := ReconstructMicroVMRegistryRecordFromHostedGenesisSession(session, runtimemicrovm.SessionReconstructionRequest{
		RequestID: "req-missing",
		TenantID:  binding.TenantID(),
		Namespace: hostedgenesis.MicroVMNamespace,
		SessionID: session.ConversationID,
		Now:       time.Date(2026, 6, 25, 21, 0, 0, 0, time.UTC),
	}, HostedGenesisMicroVMReconstructionConfig{ImageRef: "image", NetworkConnectorRef: "egress"})
	require.Error(t, err, "missing Host MicroVMLifecycleRef must fail closed")

	now := time.Date(2026, 6, 25, 21, 0, 0, 0, time.UTC)
	ref := hostedgenesis.MicroVMLifecycleRef{
		SourceOfTruth:  hostedgenesis.MicroVMSourceOfTruth,
		TenantID:       binding.TenantID(),
		Namespace:      hostedgenesis.MicroVMNamespace,
		SessionID:      session.ConversationID,
		LifecycleState: runtimemicrovm.StateRunning,
		DesiredState:   runtimemicrovm.StateRunning,
		MicroVMID:      "provider-microvm-123",
		LastAction:     runtimemicrovm.CommandRun,
		LastTransition: now,
		UpdatedAt:      now,
	}
	require.NoError(t, session.ApplyMicroVMLifecycleRef(ref))
	_, err = ReconstructMicroVMRegistryRecordFromHostedGenesisSession(session, runtimemicrovm.SessionReconstructionRequest{
		RequestID: "req-cross",
		TenantID:  "slug:other",
		Namespace: hostedgenesis.MicroVMNamespace,
		SessionID: session.ConversationID,
		Now:       now,
	}, HostedGenesisMicroVMReconstructionConfig{ImageRef: "image", NetworkConnectorRef: "egress"})
	require.Error(t, err, "tenant mismatch must fail closed")
}
