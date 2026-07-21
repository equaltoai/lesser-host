package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
	"github.com/theory-cloud/tabletheory/v2/pkg/testing/fakedb"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
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
			hasStatusCondition(conditions, hostedgenesis.StatusInProgress) &&
			hasFieldCondition(conditions, "LatestTurnID", "turn_123")
	})).Return(tx).Once()
	tx.On("UpdateWithBuilder", mock.MatchedBy(func(item any) bool {
		conversation, ok := item.(*models.SoulAgentMintConversation)
		return ok && conversation.PK == "SOUL#AGENT#0x2222222222222222222222222222222222222222222222222222222222222222" &&
			conversation.SK == "MINT_CONVERSATION#conv_123" &&
			conversation.Status == models.SoulMintConversationStatusFailed
	}), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return hasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
			hasStatusCondition(conditions, hostedgenesis.StatusInProgress) &&
			hasFieldCondition(conditions, "LatestTurnID", "turn_123")
	})).Return(tx).Once()
	tx.On("UpdateWithBuilder", mock.MatchedBy(matchesFailedHostedGenesisIdempotency), mock.Anything,
		mock.MatchedBy(hasFailedHostedGenesisIdempotencyConditions)).Return(tx).Once()

	session, conversation := validStoreHostedGenesisFailure()
	st := New(db)
	require.NoError(t, st.FailHostedGenesisSessionAndConversation(ctx, session, 7, hostedgenesis.StatusInProgress, conversation))

	db.AssertExpectations(t)
	tx.AssertExpectations(t)
}

func TestStore_FailHostedGenesisSessionAndConversationAcceptsExactAlreadyFailedIdempotency(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fake := fakedb.New()
	rawDB, err := tabletheory.NewWithClient(session.Config{Region: "us-east-1"}, fake)
	require.NoError(t, err)
	require.NoError(t, rawDB.CreateTable(&models.HostedGenesisSession{}))
	db, ok := rawDB.(DB)
	require.True(t, ok, "TableTheory ExtendedDB must satisfy the Host Store transaction contract")

	current, conversation, idempotency := recoveredTurnAlreadyFailedIdempotencyFixture(t)
	require.NoError(t, db.Model(current).Create())
	require.NoError(t, db.Model(conversation).Create())
	require.NoError(t, db.Model(idempotency).Create())
	st := New(db)
	seededIdempotency, err := st.GetSoulMintConversationIdempotency(ctx, current.InstanceSlug, current.RegistrationID, idempotency.IdempotencyKey)
	require.NoError(t, err)

	terminalAt := time.Date(2026, 7, 22, 17, 5, 0, 0, time.UTC)
	writer := completion.NewCompletionWriter(st, func() time.Time { return terminalAt })
	failedSession, err := writer.RecordFailure(ctx, completion.CompletionTurn{
		InstanceSlug:   current.InstanceSlug,
		ConversationID: current.ConversationID,
		TurnID:         current.LatestTurnID,
		RequestID:      "req-observe-terminal",
	}, completion.CompletionFailure{
		Code:      hostedgenesis.FailureCodeMicroVMUnavailable,
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 5,
			Reason:            string(hostedgenesis.FailureCodeMicroVMUnavailable),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, failedSession)
	require.NotNil(t, failedSession.Failure)
	require.Equal(t, hostedgenesis.RecoveryActionRestartSoulBootstrap, failedSession.Failure.Recovery.Action)
	require.False(t, failedSession.Failure.Retryable)

	gotSession, err := st.GetHostedGenesisSession(ctx, current.InstanceSlug, current.ConversationID)
	require.NoError(t, err)
	require.Equal(t, int64(52), gotSession.Version)
	require.Equal(t, string(hostedgenesis.StatusFailed), gotSession.Status)
	require.NotNil(t, gotSession.Failure)
	require.Equal(t, hostedgenesis.RecoveryActionRestartSoulBootstrap, gotSession.Failure.Recovery.Action)
	require.False(t, gotSession.Failure.Retryable)

	gotConversation, err := st.GetSoulAgentMintConversation(ctx, current.AgentID, current.ConversationID)
	require.NoError(t, err)
	require.Equal(t, models.SoulMintConversationStatusFailed, gotConversation.Status)
	require.Equal(t, current.LatestTurnID, gotConversation.LatestTurnID)

	gotIdempotency, err := st.GetSoulMintConversationIdempotency(ctx, current.InstanceSlug, current.RegistrationID, idempotency.IdempotencyKey)
	require.NoError(t, err)
	require.Equal(t, models.SoulMintConversationIdempotencyStatusFailed, gotIdempotency.Status)
	require.Equal(t, idempotency.TurnID, gotIdempotency.TurnID)
	require.Equal(t, idempotency.RequestHash, gotIdempotency.RequestHash)
	require.Equal(t, seededIdempotency.CreatedAt, gotIdempotency.CreatedAt)
	require.Equal(t, seededIdempotency.TTL, gotIdempotency.TTL)
}

func recoveredTurnAlreadyFailedIdempotencyFixture(t *testing.T) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *models.SoulMintConversationIdempotency) {
	t.Helper()

	acceptedAt := time.Date(2026, 7, 22, 11, 35, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 7, 22, 13, 27, 52, 62_432_003, time.UTC)
	current := validStoreHostedGenesisSession()
	current.Version = 51
	current.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	current.Failure = &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeDeclarationExtractionFailed,
		Message:   hostedgenesis.FailureMessage(hostedgenesis.FailureCodeDeclarationExtractionFailed),
		Retryable: false,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			RetryAfterSeconds: 5,
			Reason:            string(hostedgenesis.FailureCodeDeclarationExtractionFailed),
		},
	}
	current.RequestID = "req-recovered-turn"
	current.UpdatedAt = updatedAt
	current.CompletedAt = time.Time{}
	current.TurnLedger[0].AcceptedAt = acceptedAt
	require.NoError(t, current.BeforeCreate())

	conversation := &models.SoulAgentMintConversation{
		AgentID:        current.AgentID,
		ConversationID: current.ConversationID,
		Model:          current.Model,
		Status:         models.SoulMintConversationStatusDeclarationExtractionPending,
		LatestTurnID:   current.LatestTurnID,
		RequestID:      current.RequestID,
		CreatedAt:      current.CreatedAt,
		UpdatedAt:      updatedAt,
	}
	require.NoError(t, conversation.BeforeCreate())

	idempotency := &models.SoulMintConversationIdempotency{
		InstanceSlug:   current.InstanceSlug,
		RegistrationID: current.RegistrationID,
		AgentID:        current.AgentID,
		ConversationID: current.ConversationID,
		TurnID:         current.LatestTurnID,
		IdempotencyKey: current.TraceIDs.IdempotencyKey,
		RequestHash:    current.TurnLedger[0].RequestHash,
		RequestID:      "req-original-failure",
		Status:         models.SoulMintConversationIdempotencyStatusFailed,
		CreatedAt:      acceptedAt,
		UpdatedAt:      time.Date(2026, 7, 22, 11, 36, 16, 57_666_784, time.UTC),
	}
	require.NoError(t, idempotency.BeforeCreate())
	return current, conversation, idempotency
}

func TestStore_FailHostedGenesisSessionRejectsUnboundIdempotencyBeforeWrite(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	session, conversation := validStoreHostedGenesisFailure()
	session.TraceIDs.IdempotencyKey = "idem_other"

	err := New(db).FailHostedGenesisSessionAndConversation(context.Background(), session, 7, hostedgenesis.StatusInProgress, conversation)
	require.ErrorContains(t, err, "idempotency binding is absent")
	db.AssertExpectations(t)
}

func TestStore_PublishHostedGenesisSessionAndConversationUsesOneGuardedTransaction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	tx := new(ttmocks.MockTransactionBuilder)
	db.TransactWriteBuilder = tx
	db.On("TransactWrite", ctx, mock.Anything).Return(nil).Once()
	tx.UpdateBuilder = &captureHostedGenesisUpdateBuilder{}
	tx.On("UpdateWithBuilder", mock.MatchedBy(func(item any) bool {
		session, ok := item.(*models.HostedGenesisSession)
		return ok && session.PK == validStoreHostedGenesisSessionPK && session.Status == string(hostedgenesis.StatusPublished)
	}), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return hasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
			hasVersionCondition(conditions, 7) &&
			hasStatusCondition(conditions, hostedgenesis.StatusDeclarationReady)
	})).Return(tx).Once()
	tx.On("UpdateWithBuilder", mock.MatchedBy(func(item any) bool {
		conversation, ok := item.(*models.SoulAgentMintConversation)
		return ok && conversation.Status == models.SoulMintConversationStatusPublished
	}), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return hasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
			hasStatusCondition(conditions, hostedgenesis.StatusDeclarationReady)
	})).Return(tx).Once()

	session, conversation := validStoreHostedGenesisPublication()
	require.NoError(t, New(db).PublishHostedGenesisSessionAndConversation(ctx, session, 7, hostedgenesis.StatusDeclarationReady, conversation))

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
		LatestTurnID:   session.LatestTurnID,
		RequestID:      session.RequestID,
		CreatedAt:      session.CreatedAt,
		UpdatedAt:      now,
		CompletedAt:    now,
	}
	return session, conversation
}

func validStoreHostedGenesisPublication() (*models.HostedGenesisSession, *models.SoulAgentMintConversation) {
	now := time.Date(2026, 6, 24, 12, 5, 0, 0, time.UTC)
	session := validStoreHostedGenesisSession()
	checkpoint := validStoreDeclarationCheckpoint()
	session.Status = string(hostedgenesis.StatusPublished)
	session.DeclarationCheckpoint = &checkpoint
	session.Publication = &hostedgenesis.PublicationCheckpoint{
		RegistrationID:       session.RegistrationID,
		ConversationID:       session.ConversationID,
		AgentID:              session.AgentID,
		Version:              1,
		RegistrationSHA256:   strings.Repeat("b", 64),
		RegistrationIssuedAt: checkpoint.ProducedAt,
		PublishedAt:          now,
	}
	conversation := &models.SoulAgentMintConversation{
		AgentID:        session.AgentID,
		ConversationID: session.ConversationID,
		Model:          "deterministic",
		Status:         models.SoulMintConversationStatusPublished,
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
		SchemaVersion:   hostedgenesis.DeclarationSchemaVersionV2,
		GuidanceVersion: hostedgenesis.GuidanceVersionV2,
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

func hasFieldCondition(conditions []core.TransactCondition, field string, value any) bool {
	return hasFieldConditionWithOperator(conditions, field, "=", value)
}

func hasFieldConditionWithOperator(conditions []core.TransactCondition, field string, operator string, value any) bool {
	for _, condition := range conditions {
		if condition.Kind == core.TransactConditionKindField &&
			condition.Field == field &&
			condition.Operator == operator &&
			reflect.DeepEqual(condition.Value, value) {
			return true
		}
	}
	return false
}

func matchesFailedHostedGenesisIdempotency(item any) bool {
	idempotency, ok := item.(*models.SoulMintConversationIdempotency)
	return ok && idempotency.PK == models.SoulMintConversationIdempotencyPK("demo", "reg_123", "idem_123") &&
		idempotency.SK == "STATE" && idempotency.Status == models.SoulMintConversationIdempotencyStatusFailed
}

func hasFailedHostedGenesisIdempotencyConditions(conditions []core.TransactCondition) bool {
	return hasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
		hasFieldConditionWithOperator(conditions, "Status", "IN", []string{
			models.SoulMintConversationIdempotencyStatusProcessing,
			models.SoulMintConversationIdempotencyStatusFailed,
		}) &&
		hasFieldCondition(conditions, "RegistrationID", "reg_123") &&
		hasFieldCondition(conditions, "ConversationID", "conv_123") &&
		hasFieldCondition(conditions, "TurnID", "turn_123") &&
		hasFieldCondition(conditions, "IdempotencyKey", "idem_123") &&
		hasFieldCondition(conditions, "RequestHash", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
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
