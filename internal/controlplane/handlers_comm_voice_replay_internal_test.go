package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleCommVoiceInboundWebhook_ReplayedSignedWebhookDebitsOnce(t *testing.T) {
	tdb := newCommWebhookTestDB()
	var usedCredits int64
	tx := newVoiceMeteringReplayTx(t, &usedCredits)
	tdb.db.TransactWriteBuilder = tx
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Twice()
	expectInboundVoiceMeteringLookups(t, &tdb, &usedCredits, 2)

	enqueueCount := 0
	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		if msg.Notification.MessageID != "call-replay-inbound" {
			t.Fatalf("unexpected queued voice message: %#v", msg)
		}
		enqueueCount++
		return nil
	})
	s.store = store.New(tdb.db)

	body := marshalCommWebhookBody(t, map[string]any{
		"data": map[string]any{
			"event_type":  commWebhookCallHangup,
			"occurred_at": commWebhookReceivedAt,
			"payload": map[string]any{
				"from":             map[string]any{"phone_number": "+15557654321"},
				"to":               map[string]any{"phone_number": "+15550142"},
				"call_session_id":  "call-replay-inbound",
				"duration_seconds": 61,
			},
		},
	})
	req := signedCommWebhookRequest(t, body)

	for i := 0; i < 2; i++ {
		resp, err := s.handleCommVoiceInboundWebhook(&apptheory.Context{Request: req})
		requireCommWebhookOK(t, resp, err)
	}

	if enqueueCount != 2 {
		t.Fatalf("expected replayed webhook to enqueue twice while billing once, got %d", enqueueCount)
	}
	assertVoiceMeteringDebitedOnce(t, tx, usedCredits, 16)
}

func TestHandleCommVoiceStatusWebhook_ReplayedSignedOutboundStatusDebitsOnce(t *testing.T) {
	db := ttmocks.NewMockExtendedDBStrict()
	qStatus := new(ttmocks.MockQuery)
	qVoice := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	var usedCredits int64
	tx := newVoiceMeteringReplayTx(t, &usedCredits)
	db.TransactWriteBuilder = tx

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(qVoice).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	for _, q := range []*ttmocks.MockQuery{qStatus, qVoice, qBudget} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	}
	qStatus.On("IfExists").Return(qStatus).Maybe()
	qStatus.On("Update", mock.Anything).Return(nil).Twice()
	qBudget.On("ConsistentRead").Return(qBudget).Maybe()

	status := models.SoulCommMessageStatus{
		MessageID:    "comm-msg-replay-voice",
		InstanceSlug: commWebhookTestInstanceSlug,
		AgentID:      "0xabc",
		ChannelType:  commChannelVoice,
		To:           "+15557654321",
		Provider:     commDeliveryProviderTelnyx,
		Status:       models.SoulCommMessageStatusAccepted,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0)
		*dest = status
	}).Twice()
	qVoice.On("First", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommVoiceInstruction](t, args, 0)
		*dest = models.SoulCommVoiceInstruction{
			MessageID: status.MessageID,
			AgentID:   status.AgentID,
			From:      "+15550142",
			To:        status.To,
			Body:      "hello",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
	}).Twice()
	qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: commWebhookTestInstanceSlug, IncludedCredits: 100, UsedCredits: usedCredits}
	}).Twice()
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Twice()

	body, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"event_type": commWebhookCallHangup,
			"payload": map[string]any{
				"from":             "+15550142",
				"to":               "+15557654321",
				"call_control_id":  commVoiceCallControlID,
				"duration_seconds": 61,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := signedCommWebhookRequest(t, body)

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		t.Fatalf("outbound status webhook should be handled before enqueue: %#v", msg)
		return nil
	})
	s.store = store.New(db)
	for i := 0; i < 2; i++ {
		resp, err := s.handleCommVoiceStatusWebhook(&apptheory.Context{
			Params:  map[string]string{"messageId": status.MessageID},
			Request: req,
		})
		if err != nil {
			t.Fatalf("callback %d unexpected err: %v", i+1, err)
		}
		if resp == nil || resp.Status != http.StatusOK {
			t.Fatalf("callback %d expected 200, got %#v", i+1, resp)
		}
	}

	assertVoiceMeteringDebitedOnce(t, tx, usedCredits, 16)
	db.AssertExpectations(t)
}

func expectInboundVoiceMeteringLookups(t *testing.T, tdb *commWebhookTestDB, usedCredits *int64, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		expectCommWebhookPhoneAgent(t, tdb.qPhone, "0xabc")
		expectCommWebhookIdentity(t, tdb.qIdentity, "0xabc", "example.com")
		expectCommWebhookDomain(t, tdb.qDomain, commWebhookTestInstanceSlug)
		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
			*dest = models.InstanceBudgetMonth{InstanceSlug: commWebhookTestInstanceSlug, IncludedCredits: 100, UsedCredits: *usedCredits}
		}).Once()
	}
}

func assertVoiceMeteringDebitedOnce(t *testing.T, tx *voiceMeteringReplayTx, usedCredits int64, wantCredits int64) {
	t.Helper()
	if usedCredits != wantCredits {
		t.Fatalf("expected UsedCredits increment exactly once by %d, got %d", wantCredits, usedCredits)
	}
	if tx.dedupCreateAttempts != 2 || tx.dedupCreates != 1 || tx.duplicateShortCircuits != 1 {
		t.Fatalf("expected one successful dedup create and one duplicate short-circuit, attempts=%d creates=%d duplicates=%d", tx.dedupCreateAttempts, tx.dedupCreates, tx.duplicateShortCircuits)
	}
	if tx.ledgerCreates != 1 {
		t.Fatalf("expected one usage ledger entry, got %d", tx.ledgerCreates)
	}
	if tx.budgetAddCalls != 1 {
		t.Fatalf("expected one UsedCredits update, got %d", tx.budgetAddCalls)
	}
}

type voiceMeteringReplayTx struct {
	t *testing.T

	seenDedup map[string]struct{}
	used      *int64

	pendingCreates   []any
	pendingUpdateFns []func(core.UpdateBuilder) error

	dedupCreateAttempts    int
	dedupCreates           int
	duplicateShortCircuits int
	ledgerCreates          int
	budgetAddCalls         int
}

func newVoiceMeteringReplayTx(t *testing.T, used *int64) *voiceMeteringReplayTx {
	t.Helper()
	return &voiceMeteringReplayTx{
		t:         t,
		seenDedup: map[string]struct{}{},
		used:      used,
	}
}

var _ core.TransactionBuilder = (*voiceMeteringReplayTx)(nil)

func (tx *voiceMeteringReplayTx) Put(_ any, _ ...core.TransactCondition) core.TransactionBuilder {
	return tx
}

func (tx *voiceMeteringReplayTx) Create(model any, _ ...core.TransactCondition) core.TransactionBuilder {
	tx.pendingCreates = append(tx.pendingCreates, model)
	return tx
}

func (tx *voiceMeteringReplayTx) Update(_ any, _ []string, _ ...core.TransactCondition) core.TransactionBuilder {
	return tx
}

func (tx *voiceMeteringReplayTx) UpdateWithBuilder(_ any, updateFn func(core.UpdateBuilder) error, _ ...core.TransactCondition) core.TransactionBuilder {
	if updateFn != nil {
		tx.pendingUpdateFns = append(tx.pendingUpdateFns, updateFn)
	}
	return tx
}

func (tx *voiceMeteringReplayTx) Delete(_ any, _ ...core.TransactCondition) core.TransactionBuilder {
	return tx
}

func (tx *voiceMeteringReplayTx) ConditionCheck(_ any, _ ...core.TransactCondition) core.TransactionBuilder {
	return tx
}

func (tx *voiceMeteringReplayTx) WithContext(_ context.Context) core.TransactionBuilder { return tx }

func (tx *voiceMeteringReplayTx) Execute() error { return tx.execute() }

func (tx *voiceMeteringReplayTx) ExecuteWithContext(_ context.Context) error { return tx.execute() }

func (tx *voiceMeteringReplayTx) execute() error {
	creates := append([]any(nil), tx.pendingCreates...)
	updates := append([]func(core.UpdateBuilder) error(nil), tx.pendingUpdateFns...)
	tx.pendingCreates = nil
	tx.pendingUpdateFns = nil

	var dedup *models.UsageMeteringDedup
	for _, model := range creates {
		if item, ok := model.(*models.UsageMeteringDedup); ok {
			copyItem := *item
			_ = copyItem.UpdateKeys()
			dedup = &copyItem
			break
		}
	}
	if dedup == nil {
		tx.t.Fatalf("voice metering transaction did not create a dedup record")
		return theoryErrors.ErrConditionFailed
	}

	dedupKey := dedup.PK + "|" + dedup.SK
	tx.dedupCreateAttempts++
	if _, exists := tx.seenDedup[dedupKey]; exists {
		tx.duplicateShortCircuits++
		return theoryErrors.ErrConditionFailed
	}
	tx.seenDedup[dedupKey] = struct{}{}
	tx.dedupCreates++

	ledgerCount := 0
	for _, model := range creates {
		if entry, ok := model.(*models.UsageLedgerEntry); ok {
			if entry.Module != commVoiceCallUsageModule || entry.Target == "" || entry.RequestID == "" {
				tx.t.Fatalf("unexpected voice usage ledger entry: %#v", entry)
			}
			ledgerCount++
		}
	}
	if ledgerCount != 1 {
		tx.t.Fatalf("expected exactly one voice usage ledger create, got %d", ledgerCount)
	}
	tx.ledgerCreates += ledgerCount

	ub := &voiceMeteringReplayUpdateBuilder{t: tx.t, used: tx.used, addCalls: &tx.budgetAddCalls}
	for _, fn := range updates {
		if err := fn(ub); err != nil {
			return err
		}
	}
	return nil
}

type voiceMeteringReplayUpdateBuilder struct {
	t        *testing.T
	used     *int64
	addCalls *int
}

var _ core.UpdateBuilder = (*voiceMeteringReplayUpdateBuilder)(nil)

func (ub *voiceMeteringReplayUpdateBuilder) Set(_ string, _ any) core.UpdateBuilder { return ub }
func (ub *voiceMeteringReplayUpdateBuilder) SetIfNotExists(_ string, _ any, _ any) core.UpdateBuilder {
	return ub
}
func (ub *voiceMeteringReplayUpdateBuilder) Add(field string, value any) core.UpdateBuilder {
	if field != "UsedCredits" {
		return ub
	}
	delta, ok := value.(int64)
	if !ok {
		ub.t.Fatalf("expected int64 UsedCredits delta, got %T", value)
	}
	*ub.used += delta
	*ub.addCalls++
	return ub
}
func (ub *voiceMeteringReplayUpdateBuilder) Increment(_ string) core.UpdateBuilder { return ub }
func (ub *voiceMeteringReplayUpdateBuilder) Decrement(_ string) core.UpdateBuilder { return ub }
func (ub *voiceMeteringReplayUpdateBuilder) Remove(_ string) core.UpdateBuilder    { return ub }
func (ub *voiceMeteringReplayUpdateBuilder) Delete(_ string, _ any) core.UpdateBuilder {
	return ub
}
func (ub *voiceMeteringReplayUpdateBuilder) AppendToList(_ string, _ any) core.UpdateBuilder {
	return ub
}
func (ub *voiceMeteringReplayUpdateBuilder) PrependToList(_ string, _ any) core.UpdateBuilder {
	return ub
}
func (ub *voiceMeteringReplayUpdateBuilder) RemoveFromListAt(_ string, _ int) core.UpdateBuilder {
	return ub
}
func (ub *voiceMeteringReplayUpdateBuilder) SetListElement(_ string, _ int, _ any) core.UpdateBuilder {
	return ub
}
func (ub *voiceMeteringReplayUpdateBuilder) Condition(_ string, _ string, _ any) core.UpdateBuilder {
	return ub
}
func (ub *voiceMeteringReplayUpdateBuilder) OrCondition(_ string, _ string, _ any) core.UpdateBuilder {
	return ub
}
func (ub *voiceMeteringReplayUpdateBuilder) ConditionExists(_ string) core.UpdateBuilder { return ub }
func (ub *voiceMeteringReplayUpdateBuilder) ConditionNotExists(_ string) core.UpdateBuilder {
	return ub
}
func (ub *voiceMeteringReplayUpdateBuilder) ConditionVersion(_ int64) core.UpdateBuilder { return ub }
func (ub *voiceMeteringReplayUpdateBuilder) ReturnValues(_ string) core.UpdateBuilder    { return ub }
func (ub *voiceMeteringReplayUpdateBuilder) Execute() error                              { return nil }
func (ub *voiceMeteringReplayUpdateBuilder) ExecuteWithResult(_ any) error               { return nil }
