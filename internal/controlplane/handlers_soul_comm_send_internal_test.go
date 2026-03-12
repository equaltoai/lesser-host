package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	commSendTelnyxMessageID    = "telnyx-out-1"
	commSendTestEmailRecipient = "alice@example.com"
)

func allowCommQueryOps(queries ...*ttmocks.MockQuery) {
	for _, q := range queries {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("All", mock.Anything).Return(nil).Maybe()
		q.On("Create").Return(nil).Maybe()
	}
}

func assertSoulCommSendResponse(t *testing.T, resp *apptheory.Response, wantStatus string, wantProvider string, wantChannel string, wantProviderMessageID string) {
	t.Helper()
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulCommSendResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != wantStatus || out.Provider != wantProvider || out.Channel != wantChannel {
		t.Fatalf("unexpected response: %#v", out)
	}
	if wantProviderMessageID != "" && out.ProviderMessageID != wantProviderMessageID {
		t.Fatalf("expected provider message id %q, got %q", wantProviderMessageID, out.ProviderMessageID)
	}
}

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
	if appErr.Code != commCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected %s/401, got %q/%d", commCodeUnauthorized, appErr.Code, appErr.StatusCode)
	}
}

func TestEnforceSoulCommSendGuards_AllowsFirstContactToExternalRecipient(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qCommActivity := new(ttmocks.MockQuery)
	qEmailIdx := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qCommActivity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(qEmailIdx).Maybe()

	allowCommQueryOps(qKey, qDomain, qIdentity, qChannel, qCommActivity, qEmailIdx)

	agentID := soulLifecycleTestAgentIDHex

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
			Identifier:    provisionTestEmailAddress,
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
	qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	s := &Server{
		store: store.New(db),
		cfg:   config.Config{SoulEnabled: true},
	}

	decision, err := s.enforceSoulCommSendGuards(&apptheory.Context{RequestID: "r-comm-first-contact"}, &models.SoulAgentIdentity{
		AgentID: agentID,
		Domain:  "example.com",
	}, validatedSoulCommSendRequest{
		channel:    commChannelEmail,
		agentIDHex: agentID,
		to:         commSendTestEmailRecipient,
		subject:    "Hello",
		body:       "Test",
	}, time.Now().UTC(), newSoulCommSendMetrics("lab", "inst1"))
	if err != nil {
		t.Fatalf("expected unsolicited external first contact to be allowed, got %v", err)
	}
	if decision.preferenceRespected != nil {
		t.Fatalf("expected nil preferenceRespected for external recipient, got %#v", decision.preferenceRespected)
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
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qCommActivity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	allowCommQueryOps(qKey, qDomain, qIdentity, qChannel, qCommActivity, qStatus, qAudit)

	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()}
	}).Once()

	agentID := soulLifecycleTestAgentIDHex

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
			Identifier:    provisionTestEmailAddress,
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
			if username != provisionTestEmailAddress || password != "pw" || from != provisionTestEmailAddress {
				t.Fatalf("unexpected smtp args username=%q password=%q from=%q", username, password, from)
			}
			if len(recipients) != 1 || recipients[0] != commSendTestEmailRecipient {
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
		"to":        commSendTestEmailRecipient,
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
	if !sendCalled {
		t.Fatalf("expected smtp send called")
	}
	assertSoulCommSendResponse(t, resp, commMetricSent, commDeliveryProviderMigadu, commChannelEmail, "")

	var out soulCommSendResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
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
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qCommActivity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	allowCommQueryOps(qKey, qDomain, qIdentity, qChannel, qCommActivity, qStatus, qInstance, qBudget, qAudit)

	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()}
	}).Once()

	agentID := soulLifecycleTestAgentIDHex

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
			return commSendTelnyxMessageID, nil
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
	if !sendCalled {
		t.Fatalf("expected telnyx send called")
	}
	assertSoulCommSendResponse(t, resp, commMetricSent, commDeliveryProviderTelnyx, commChannelSMS, commSendTelnyxMessageID)
}

func TestHandleSoulCommSend_StartsVoiceCallAndStoresInstruction(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qCommActivity := new(ttmocks.MockQuery)
	qStatus := new(ttmocks.MockQuery)
	qVoice := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qCommActivity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(qVoice).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	allowCommQueryOps(qKey, qDomain, qIdentity, qChannel, qCommActivity, qStatus, qVoice, qAudit)

	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()}
	}).Once()

	agentID := soulLifecycleTestAgentIDHex

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

	var voiceCalled bool
	s := &Server{
		store: store.New(db),
		cfg:   config.Config{SoulEnabled: true, Stage: "lab", PublicBaseURL: "https://lab.lesser.host"},
		telnyxCallVoice: func(ctx context.Context, from string, to string, texmlURL string, statusCallbackURL string) (string, error) {
			voiceCalled = true
			if from != "+15550142" || to != "+15550143" {
				t.Fatalf("unexpected voice args from=%q to=%q", from, to)
			}
			if !strings.HasPrefix(texmlURL, "https://lab.lesser.host/webhooks/comm/voice/texml/comm-msg-") {
				t.Fatalf("unexpected texmlURL: %q", texmlURL)
			}
			if !strings.HasPrefix(statusCallbackURL, "https://lab.lesser.host/webhooks/comm/voice/status/comm-msg-") {
				t.Fatalf("unexpected statusCallbackURL: %q", statusCallbackURL)
			}
			return "call-control-1", nil
		},
	}

	body, _ := json.Marshal(map[string]any{
		"channel":   "voice",
		"agentId":   agentID,
		"to":        "+15550143",
		"body":      "Test by voice",
		"inReplyTo": "call-back-1",
	})

	ctx := &apptheory.Context{
		RequestID: "r-comm-send-voice",
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
	if !voiceCalled {
		t.Fatalf("expected telnyx voice call")
	}
	assertSoulCommSendResponse(t, resp, models.SoulCommMessageStatusAccepted, commDeliveryProviderTelnyx, commChannelVoice, "call-control-1")
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

	agentID := soulLifecycleTestAgentIDHex

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
			return commSendTelnyxMessageID, nil
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

	agentID := soulLifecycleTestAgentIDHex
	messageID := "comm-msg-test"

	qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0)
		confidence := 0.91
		*dest = models.SoulCommMessageStatus{
			MessageID:         messageID,
			AgentID:           agentID,
			ChannelType:       "voice",
			To:                "+15550143",
			Provider:          "telnyx",
			ProviderMessageID: "call-1",
			Status:            models.SoulCommMessageStatusSent,
			ReplyMessageID:    "comm-msg-test-reply-call-1",
			ReplyBody:         commVoiceReplyBody,
			ReplyConfidence:   &confidence,
			ReplyReceivedAt:   time.Date(2026, 3, 4, 12, 0, 30, 0, time.UTC),
			CreatedAt:         time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
			UpdatedAt:         time.Date(2026, 3, 4, 12, 0, 31, 0, time.UTC),
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
	if out.MessageID != messageID || out.Status != commMetricSent || out.AgentID != strings.ToLower(agentID) {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.ReplyBody != commVoiceReplyBody || out.ReplyMessageID != "comm-msg-test-reply-call-1" || out.ReplyReceivedAt == "" {
		t.Fatalf("expected reply metadata, got %#v", out)
	}
	if out.ReplyConfidence == nil || *out.ReplyConfidence != 0.91 {
		t.Fatalf("expected reply confidence, got %#v", out.ReplyConfidence)
	}
}
