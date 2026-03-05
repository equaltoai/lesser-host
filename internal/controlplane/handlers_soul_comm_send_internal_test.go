package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulCommSend_UnauthorizedWithoutBearer(t *testing.T) {
	t.Parallel()

	s := &Server{
		store: store.New(ttmocks.NewMockExtendedDB()),
		cfg:   config.Config{SoulEnabled: true},
	}

	ctx := &apptheory.Context{
		RequestID: "r-comm-unauth",
		Request:   apptheory.Request{Body: []byte(`{}`)},
	}

	_, err := s.handleSoulCommSend(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppTheoryError, got %T", err)
	}
	if appErr.Code != "comm.unauthorized" || appErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected comm.unauthorized/401, got %q/%d", appErr.Code, appErr.StatusCode)
	}
}

func TestHandleSoulCommSend_BoundaryViolationRequiresInReplyTo(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()

	qKey.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qKey).Maybe()
	qKey.On("ConsistentRead").Return(qKey).Maybe()
	qKey.On("IfExists").Return(qKey).Maybe()
	qKey.On("Update", mock.Anything).Return(nil).Maybe()
	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()}
	}).Once()

	s := &Server{
		store: store.New(db),
		cfg:   config.Config{SoulEnabled: true},
	}

	body, _ := json.Marshal(map[string]any{
		"channel": "email",
		"agentId": "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab",
		"to":      "alice@example.com",
		"subject": "Hello",
		"body":    "Test",
	})

	ctx := &apptheory.Context{
		RequestID:    "r-comm-boundary",
		AuthIdentity: "",
		Request: apptheory.Request{
			Body: body,
			Headers: map[string][]string{
				"authorization": {"Bearer k1"},
			},
		},
	}

	_, err := s.handleSoulCommSend(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppTheoryError, got %T", err)
	}
	if appErr.Code != "comm.boundary_violation" || appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected comm.boundary_violation/403, got %q/%d", appErr.Code, appErr.StatusCode)
	}
}

func TestHandleSoulCommSend_SendsEmailAndRecordsStatus(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qCommActivity := new(ttmocks.MockQuery)
	qStatus := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qCommActivity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()

	for _, q := range []*ttmocks.MockQuery{qKey, qDomain, qIdentity, qChannel, qCommActivity, qStatus} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("All", mock.Anything).Return(nil).Maybe()
	}

	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()}
	}).Once()

	agentID := "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab"

	qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			LocalID:         "agent-alice",
			Status:          models.SoulAgentStatusActive,
			LifecycleStatus: models.SoulAgentStatusActive,
			UpdatedAt:       time.Now().UTC(),
		}
	}).Once()

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()

	qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:       agentID,
			ChannelType:   models.SoulChannelTypeEmail,
			Identifier:    "agent-alice@lessersoul.ai",
			Verified:      true,
			ProvisionedAt: time.Now().Add(-time.Hour).UTC(),
			Status:        models.SoulChannelStatusActive,
			UpdatedAt:     time.Now().UTC(),
		}
	}).Once()

	// Rate limit scan: no prior outbound activity.
	qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
		*dest = []*models.SoulAgentCommActivity{}
	}).Twice()

	var sendCalled bool
	s := &Server{
		store: store.New(db),
		cfg:   config.Config{SoulEnabled: true, Stage: "lab"},
		ssmGetParameter: func(ctx context.Context, name string) (string, error) {
			return "pw", nil
		},
		migaduSendSMTP: func(ctx context.Context, username string, password string, from string, recipients []string, data []byte) error {
			sendCalled = true
			if username != "agent-alice@lessersoul.ai" || password != "pw" || from != "agent-alice@lessersoul.ai" {
				t.Fatalf("unexpected smtp args username=%q password=%q from=%q", username, password, from)
			}
			if len(recipients) != 1 || recipients[0] != "alice@example.com" {
				t.Fatalf("unexpected recipients: %#v", recipients)
			}
			if !strings.Contains(string(data), "Subject: Hello") {
				t.Fatalf("expected subject header in email")
			}
			return nil
		},
	}

	body, _ := json.Marshal(map[string]any{
		"channel":   "email",
		"agentId":   agentID,
		"to":        "alice@example.com",
		"subject":   "Hello",
		"body":      "Test",
		"inReplyTo": "comm-msg-xyz",
	})

	ctx := &apptheory.Context{
		RequestID: "r-comm-send",
		Request: apptheory.Request{
			Body: body,
			Headers: map[string][]string{
				"authorization": {"Bearer k1"},
			},
		},
	}

	resp, err := s.handleSoulCommSend(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if !sendCalled {
		t.Fatalf("expected smtp send called")
	}

	var out soulCommSendResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != "sent" || out.Provider != "migadu" || out.Channel != "email" {
		t.Fatalf("unexpected response: %#v", out)
	}
	if !strings.HasPrefix(out.MessageID, "comm-msg-") {
		t.Fatalf("expected comm message id, got %q", out.MessageID)
	}
}

func TestHandleSoulCommSend_SendsSMSAndDebitsCredits(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qCommActivity := new(ttmocks.MockQuery)
	qStatus := new(ttmocks.MockQuery)
	qInstance := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qCommActivity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()

	for _, q := range []*ttmocks.MockQuery{qKey, qDomain, qIdentity, qChannel, qCommActivity, qStatus, qInstance, qBudget} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("All", mock.Anything).Return(nil).Maybe()
	}

	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()}
	}).Once()

	agentID := "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab"

	qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			LocalID:         "agent-alice",
			Status:          models.SoulAgentStatusActive,
			LifecycleStatus: models.SoulAgentStatusActive,
			UpdatedAt:       time.Now().UTC(),
		}
	}).Once()

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()

	qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:       agentID,
			ChannelType:   models.SoulChannelTypePhone,
			Identifier:    "+15550142",
			Verified:      true,
			ProvisionedAt: time.Now().Add(-time.Hour).UTC(),
			Status:        models.SoulChannelStatusActive,
			UpdatedAt:     time.Now().UTC(),
		}
	}).Once()

	qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
		*dest = []*models.SoulAgentCommActivity{}
	}).Twice()

	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", OveragePolicy: "block"}
	}).Once()

	qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "inst1", Month: time.Now().UTC().Format("2006-01"), IncludedCredits: 1000, UsedCredits: 0, UpdatedAt: time.Now().UTC()}
	}).Once()

	var sendCalled bool
	s := &Server{
		store: store.New(db),
		cfg:   config.Config{SoulEnabled: true, Stage: "lab"},
		telnyxSendSMS: func(ctx context.Context, from string, to string, text string) (string, error) {
			sendCalled = true
			if from != "+15550142" || to != "+15550143" || text != "Test" {
				t.Fatalf("unexpected sms args from=%q to=%q text=%q", from, to, text)
			}
			return "telnyx-out-1", nil
		},
	}

	body, _ := json.Marshal(map[string]any{
		"channel":   "sms",
		"agentId":   agentID,
		"to":        "+15550143",
		"body":      "Test",
		"inReplyTo": "telnyx-msg-1",
	})

	ctx := &apptheory.Context{
		RequestID: "r-comm-send-sms",
		Request: apptheory.Request{
			Body: body,
			Headers: map[string][]string{
				"authorization": {"Bearer k1"},
			},
		},
	}

	resp, err := s.handleSoulCommSend(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if !sendCalled {
		t.Fatalf("expected telnyx send called")
	}

	var out soulCommSendResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != "sent" || out.Provider != "telnyx" || out.Channel != "sms" {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.ProviderMessageID != "telnyx-out-1" {
		t.Fatalf("expected provider message id, got %q", out.ProviderMessageID)
	}
}

func TestHandleSoulCommSend_SMSInsufficientCreditsBlocksSend(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qCommActivity := new(ttmocks.MockQuery)
	qInstance := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qCommActivity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()

	for _, q := range []*ttmocks.MockQuery{qKey, qDomain, qIdentity, qChannel, qCommActivity, qInstance, qBudget} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("All", mock.Anything).Return(nil).Maybe()
	}

	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()}
	}).Once()

	agentID := "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab"

	qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			LocalID:         "agent-alice",
			Status:          models.SoulAgentStatusActive,
			LifecycleStatus: models.SoulAgentStatusActive,
			UpdatedAt:       time.Now().UTC(),
		}
	}).Once()

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()

	qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:       agentID,
			ChannelType:   models.SoulChannelTypePhone,
			Identifier:    "+15550142",
			Verified:      true,
			ProvisionedAt: time.Now().Add(-time.Hour).UTC(),
			Status:        models.SoulChannelStatusActive,
			UpdatedAt:     time.Now().UTC(),
		}
	}).Once()

	qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
		*dest = []*models.SoulAgentCommActivity{}
	}).Twice()

	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", OveragePolicy: "block"}
	}).Once()

	qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "inst1", Month: time.Now().UTC().Format("2006-01"), IncludedCredits: 0, UsedCredits: 0, UpdatedAt: time.Now().UTC()}
	}).Once()

	var sendCalled bool
	s := &Server{
		store: store.New(db),
		cfg:   config.Config{SoulEnabled: true, Stage: "lab"},
		telnyxSendSMS: func(ctx context.Context, from string, to string, text string) (string, error) {
			sendCalled = true
			return "telnyx-out-1", nil
		},
	}

	body, _ := json.Marshal(map[string]any{
		"channel":   "sms",
		"agentId":   agentID,
		"to":        "+15550143",
		"body":      "Test",
		"inReplyTo": "telnyx-msg-1",
	})

	ctx := &apptheory.Context{
		RequestID: "r-comm-send-sms-credits",
		Request: apptheory.Request{
			Body: body,
			Headers: map[string][]string{
				"authorization": {"Bearer k1"},
			},
		},
	}

	_, err := s.handleSoulCommSend(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppTheoryError, got %T", err)
	}
	if appErr.Code != "comm.insufficient_credits" || appErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected comm.insufficient_credits/409, got %q/%d", appErr.Code, appErr.StatusCode)
	}
	if sendCalled {
		t.Fatalf("expected telnyx send not called")
	}
}

func TestHandleSoulCommStatus_ReturnsStatusRecord(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qStatus := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()

	for _, q := range []*ttmocks.MockQuery{qKey, qDomain, qIdentity, qStatus} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()}
	}).Once()

	agentID := "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab"
	messageID := "comm-msg-test"

	qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0)
		*dest = models.SoulCommMessageStatus{
			MessageID:   messageID,
			AgentID:     agentID,
			ChannelType: "email",
			To:          "alice@example.com",
			Provider:    "migadu",
			Status:      models.SoulCommMessageStatusSent,
			CreatedAt:   time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2026, 3, 4, 12, 0, 1, 0, time.UTC),
		}
	}).Once()

	qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			Status:          models.SoulAgentStatusActive,
			LifecycleStatus: models.SoulAgentStatusActive,
			UpdatedAt:       time.Now().UTC(),
		}
	}).Once()

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()

	s := &Server{
		store: store.New(db),
		cfg:   config.Config{SoulEnabled: true},
	}

	ctx := &apptheory.Context{
		RequestID: "r-comm-status",
		Params:    map[string]string{"messageId": messageID},
		Request: apptheory.Request{
			Headers: map[string][]string{
				"authorization": {"Bearer k1"},
			},
		},
	}

	resp, err := s.handleSoulCommStatus(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	var out soulCommStatusResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.MessageID != messageID || out.Status != "sent" || out.AgentID != strings.ToLower(agentID) {
		t.Fatalf("unexpected response: %#v", out)
	}
}
