package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type soulCommSendMoreTestDB struct {
	db            *ttmocks.MockExtendedDB
	qKey          *ttmocks.MockQuery
	qDomain       *ttmocks.MockQuery
	qIdentity     *ttmocks.MockQuery
	qChannel      *ttmocks.MockQuery
	qEmailIdx     *ttmocks.MockQuery
	qPhoneIdx     *ttmocks.MockQuery
	qPrefs        *ttmocks.MockQuery
	qReputation   *ttmocks.MockQuery
	qCommActivity *ttmocks.MockQuery
	qIdem         *ttmocks.MockQuery
	qStatus       *ttmocks.MockQuery
	qInstance     *ttmocks.MockQuery
	qBudget       *ttmocks.MockQuery
	qAudit        *ttmocks.MockQuery
}

func newSoulCommSendMoreTestDB() soulCommSendMoreTestDB {
	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qEmailIdx := new(ttmocks.MockQuery)
	qPhoneIdx := new(ttmocks.MockQuery)
	qPrefs := new(ttmocks.MockQuery)
	qReputation := new(ttmocks.MockQuery)
	qCommActivity := new(ttmocks.MockQuery)
	qIdem := new(ttmocks.MockQuery)
	qStatus := new(ttmocks.MockQuery)
	qInstance := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(qEmailIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(qPhoneIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(qPrefs).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentReputation")).Return(qReputation).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qCommActivity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommSendIdempotency")).Return(qIdem).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qKey, qDomain, qIdentity, qChannel, qEmailIdx, qPhoneIdx, qPrefs, qReputation, qCommActivity, qIdem, qStatus, qInstance, qBudget, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Create").Return(nil).Maybe()
	}

	return soulCommSendMoreTestDB{
		db:            db,
		qKey:          qKey,
		qDomain:       qDomain,
		qIdentity:     qIdentity,
		qChannel:      qChannel,
		qEmailIdx:     qEmailIdx,
		qPhoneIdx:     qPhoneIdx,
		qPrefs:        qPrefs,
		qReputation:   qReputation,
		qCommActivity: qCommActivity,
		qIdem:         qIdem,
		qStatus:       qStatus,
		qInstance:     qInstance,
		qBudget:       qBudget,
		qAudit:        qAudit,
	}
}

func assertCommTheoryErrorCode(t *testing.T, err error, code string, status int) {
	t.Helper()
	appErr := requireCommTheoryError(t, err)
	if appErr.Code != code || appErr.StatusCode != status {
		t.Fatalf("expected %s/%d, got %q/%d", code, status, appErr.Code, appErr.StatusCode)
	}
}

func mustMarshalCommSendBody(t *testing.T, body map[string]any) []byte {
	t.Helper()
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func TestHandleSoulCommSend_ValidationErrors(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex

	t.Run("invalid json", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		_, err := s.handleSoulCommSend(newCommSendCtx([]byte("{"), nil))
		assertCommTheoryErrorCode(t, err, commCodeInvalidRequest, http.StatusBadRequest)
	})

	t.Run("invalid channel", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel": "fax",
			"agentId": agentID,
			"to":      "alice@example.com",
			"body":    "hello",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, nil))
		assertCommTheoryErrorCode(t, err, commCodeInvalidRequest, http.StatusBadRequest)
	})

	t.Run("invalid email target", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel": "email",
			"agentId": agentID,
			"to":      "not-an-email",
			"subject": "Hello",
			"body":    "hello",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, nil))
		assertCommTheoryErrorCode(t, err, commCodeInvalidRequest, http.StatusBadRequest)
	})

	t.Run("missing email subject", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel": "email",
			"agentId": agentID,
			"to":      "alice@example.com",
			"body":    "hello",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, nil))
		assertCommTheoryErrorCode(t, err, commCodeInvalidRequest, http.StatusBadRequest)
	})

	t.Run("invalid sms target", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel": "sms",
			"agentId": agentID,
			"to":      "15550143",
			"body":    "hello",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, nil))
		assertCommTheoryErrorCode(t, err, commCodeInvalidRequest, http.StatusBadRequest)
	})

	t.Run("body required", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel": "sms",
			"agentId": agentID,
			"to":      "+15550143",
			"body":    " ",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, nil))
		assertCommTheoryErrorCode(t, err, commCodeInvalidRequest, http.StatusBadRequest)
	})
}

func TestHandleSoulCommSend_StateErrors(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex

	t.Run("agent not active", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         agentID,
				Domain:          "example.com",
				Status:          models.SoulAgentStatusSelfSuspended,
				LifecycleStatus: models.SoulAgentStatusSelfSuspended,
			}
		}).Once()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel": "email",
			"agentId": agentID,
			"to":      "alice@example.com",
			"subject": "Hello",
			"body":    "hello",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, nil))
		assertCommTheoryErrorCode(t, err, "comm.agent_not_active", http.StatusConflict)
	})

	t.Run("channel unverified", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		expectCommIdentity(t, tdb.qIdentity, models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			LocalID:         "agent-alice",
			Status:          models.SoulAgentStatusActive,
			LifecycleStatus: models.SoulAgentStatusActive,
			UpdatedAt:       time.Now().UTC(),
		})
		expectCommDomain(t, tdb.qDomain, models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified})
		expectCommChannel(t, tdb.qChannel, models.SoulAgentChannel{
			AgentID:       agentID,
			ChannelType:   models.SoulChannelTypeEmail,
			Identifier:    provisionTestEmailAddress,
			Verified:      false,
			ProvisionedAt: time.Now().Add(-time.Hour).UTC(),
			Status:        models.SoulChannelStatusActive,
			UpdatedAt:     time.Now().UTC(),
		})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel": "email",
			"agentId": agentID,
			"to":      "alice@example.com",
			"subject": "Hello",
			"body":    "hello",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, strPtr("comm-msg-prev")))
		assertCommTheoryErrorCode(t, err, "comm.channel_unverified", http.StatusConflict)
	})
}

func TestHandleSoulCommSend_EmailProviderAndVoiceErrors(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex

	t.Run("email provider not configured", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		expectActiveCommRoute(t, tdb, agentID, models.SoulChannelTypeEmail)
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel":   "email",
			"agentId":   agentID,
			"to":        "alice@example.com",
			"subject":   "Hello",
			"body":      "hello",
			"inReplyTo": "comm-msg-prev",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, strPtr("comm-msg-prev")))
		assertCommTheoryErrorCode(t, err, commCodeProviderUnavailable, http.StatusServiceUnavailable)
	})

	t.Run("email provider unavailable", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		expectActiveCommRoute(t, tdb, agentID, models.SoulChannelTypeEmail)
		s := &Server{
			store: store.New(tdb.db),
			cfg:   config.Config{SoulEnabled: true},
			ssmGetParameter: func(ctx context.Context, name string) (string, error) {
				return "pw", nil
			},
			migaduSendSMTP: func(ctx context.Context, username string, password string, from string, recipients []string, data []byte) error {
				return context.DeadlineExceeded
			},
		}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel":   "email",
			"agentId":   agentID,
			"to":        "alice@example.com",
			"subject":   "Hello",
			"body":      "hello",
			"inReplyTo": "comm-msg-prev",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, strPtr("comm-msg-prev")))
		assertCommTheoryErrorCode(t, err, commCodeProviderUnavailable, http.StatusServiceUnavailable)
	})

	t.Run("email provider rejected", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		expectActiveCommRoute(t, tdb, agentID, models.SoulChannelTypeEmail)
		s := &Server{
			store: store.New(tdb.db),
			cfg:   config.Config{SoulEnabled: true},
			ssmGetParameter: func(ctx context.Context, name string) (string, error) {
				return "pw", nil
			},
			migaduSendSMTP: func(ctx context.Context, username string, password string, from string, recipients []string, data []byte) error {
				return errors.New("rejected")
			},
		}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel":   "email",
			"agentId":   agentID,
			"to":        "alice@example.com",
			"subject":   "Hello",
			"body":      "hello",
			"inReplyTo": "comm-msg-prev",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, strPtr("comm-msg-prev")))
		appErr := requireCommTheoryError(t, err)
		if appErr.Code != "comm.provider_rejected" || appErr.StatusCode != http.StatusBadGateway {
			t.Fatalf("expected provider_rejected/502, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("voice unsupported", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		expectActiveCommRoute(t, tdb, agentID, models.SoulChannelTypePhone)
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel":   "voice",
			"agentId":   agentID,
			"to":        "+15550143",
			"body":      "hello",
			"inReplyTo": "comm-msg-prev",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, strPtr("comm-msg-prev")))
		assertCommTheoryErrorCode(t, err, commCodeProviderUnavailable, http.StatusServiceUnavailable)
	})

	t.Run("phone entitlement required before channel details", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		expectCommIdentity(t, tdb.qIdentity, models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			LocalID:         "agent-alice",
			Status:          models.SoulAgentStatusActive,
			LifecycleStatus: models.SoulAgentStatusActive,
			UpdatedAt:       time.Now().UTC(),
		})
		expectCommDomain(t, tdb.qDomain, models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		body := mustMarshalCommSendBody(t, map[string]any{
			"channel": "sms",
			"agentId": agentID,
			"to":      "+15550143",
			"body":    "hello",
		})
		_, err := s.handleSoulCommSend(newCommSendCtx(body, nil))
		appErr := requireCommTheoryError(t, err)
		if appErr.Code != commCodeEntitlementRequired || appErr.StatusCode != http.StatusForbidden || appErr.Message != "channel entitlement required" {
			t.Fatalf("expected entitlement_required/403 without detail leak, got %q/%d %q", appErr.Code, appErr.StatusCode, appErr.Message)
		}
		tdb.qChannel.AssertNotCalled(t, "First", mock.Anything)
	})
}

func TestHandleSoulCommSend_IdempotencyBehavior(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	body := map[string]any{
		"channel":        "email",
		"agentId":        agentID,
		"to":             "alice@example.com",
		"subject":        "Hello",
		"body":           "hello",
		"idempotencyKey": "dup-key",
	}
	expectedHash := soulCommSendRequestHash("inst1", validatedSoulCommSendRequest{
		channel:        commChannelEmail,
		agentIDHex:     agentID,
		to:             "alice@example.com",
		subject:        "Hello",
		body:           "hello",
		idempotencyKey: "dup-key",
	})

	t.Run("returns original success without redispatch", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		expectActiveCommRoute(t, tdb, agentID, models.SoulChannelTypeEmail)
		tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		resetCommMockQueryChain(tdb.qIdem)
		resetCommMockQueryChain(tdb.qStatus)
		tdb.qIdem.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		tdb.qIdem.On("First", mock.AnythingOfType("*models.SoulCommSendIdempotency")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulCommSendIdempotency](t, args, 0)
			*dest = models.SoulCommSendIdempotency{
				InstanceSlug:   "inst1",
				AgentID:        agentID,
				IdempotencyKey: "dup-key",
				RequestHash:    expectedHash,
				MessageID:      "comm-msg-existing",
				ChannelType:    commChannelEmail,
				To:             "alice@example.com",
				Status:         models.SoulCommSendIdempotencyStatusSucceeded,
				ResponseStatus: models.SoulCommMessageStatusSent,
				CreatedAt:      time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
			}
		}).Once()
		tdb.qStatus.On("All", mock.AnythingOfType("*[]*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulCommMessageStatus](t, args, 0)
			*dest = []*models.SoulCommMessageStatus{{
				MessageID:         "comm-msg-existing",
				InstanceSlug:      "inst1",
				AgentID:           agentID,
				IdempotencyKey:    "dup-key",
				ChannelType:       commChannelEmail,
				To:                "alice@example.com",
				Provider:          commDeliveryProviderMigadu,
				ProviderMessageID: "<comm-msg-existing@lessersoul.ai>",
				Status:            models.SoulCommMessageStatusSent,
				CreatedAt:         time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
			}}
		}).Once()

		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		resp, err := s.handleSoulCommSend(newCommSendCtx(mustMarshalCommSendBody(t, body), nil))
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		assertSoulCommSendResponse(t, resp, models.SoulCommMessageStatusSent, commDeliveryProviderMigadu, commChannelEmail, "<comm-msg-existing@lessersoul.ai>")

		var out soulCommSendResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.MessageID != "comm-msg-existing" {
			t.Fatalf("expected existing message id, got %#v", out)
		}
	})

	t.Run("returns accepted while original request is still processing", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		expectActiveCommRoute(t, tdb, agentID, models.SoulChannelTypeEmail)
		tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		resetCommMockQueryChain(tdb.qIdem)
		resetCommMockQueryChain(tdb.qStatus)
		tdb.qIdem.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		tdb.qIdem.On("First", mock.AnythingOfType("*models.SoulCommSendIdempotency")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulCommSendIdempotency](t, args, 0)
			*dest = models.SoulCommSendIdempotency{
				InstanceSlug:   "inst1",
				AgentID:        agentID,
				IdempotencyKey: "dup-key",
				RequestHash:    expectedHash,
				MessageID:      "comm-msg-existing",
				ChannelType:    commChannelEmail,
				To:             "alice@example.com",
				Status:         models.SoulCommSendIdempotencyStatusProcessing,
				ResponseStatus: models.SoulCommMessageStatusAccepted,
				CreatedAt:      time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
			}
		}).Once()
		tdb.qStatus.On("All", mock.AnythingOfType("*[]*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulCommMessageStatus](t, args, 0)
			*dest = []*models.SoulCommMessageStatus{}
		}).Once()

		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		resp, err := s.handleSoulCommSend(newCommSendCtx(mustMarshalCommSendBody(t, body), nil))
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		assertSoulCommSendResponse(t, resp, models.SoulCommMessageStatusAccepted, "", commChannelEmail, "")

		var out soulCommSendResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.MessageID != "comm-msg-existing" {
			t.Fatalf("expected in-flight message id, got %#v", out)
		}
	})

	t.Run("rejects same key for different payload", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		expectActiveCommRoute(t, tdb, agentID, models.SoulChannelTypeEmail)
		tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		resetCommMockQueryChain(tdb.qIdem)
		tdb.qIdem.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		tdb.qIdem.On("First", mock.AnythingOfType("*models.SoulCommSendIdempotency")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulCommSendIdempotency](t, args, 0)
			*dest = models.SoulCommSendIdempotency{
				InstanceSlug:   "inst1",
				AgentID:        agentID,
				IdempotencyKey: "dup-key",
				RequestHash:    "different-hash",
				MessageID:      "comm-msg-existing",
				ChannelType:    commChannelEmail,
				To:             "alice@example.com",
				Status:         models.SoulCommSendIdempotencyStatusSucceeded,
				ResponseStatus: models.SoulCommMessageStatusSent,
				CreatedAt:      time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
			}
		}).Once()

		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		_, err := s.handleSoulCommSend(newCommSendCtx(mustMarshalCommSendBody(t, body), nil))
		assertCommTheoryErrorCode(t, err, commCodeIdempotencyConflict, http.StatusConflict)
	})
}

func testSoulCommSendBaseIdempotencyItem() *models.SoulCommSendIdempotency {
	return &models.SoulCommSendIdempotency{
		InstanceSlug:   "inst1",
		AgentID:        soulLifecycleTestAgentIDHex,
		IdempotencyKey: "dup-key",
		RequestHash:    "hash-1",
		MessageID:      "comm-msg-1",
		ChannelType:    commChannelEmail,
		To:             "alice@example.com",
		CreatedAt:      time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
	}
}

func TestSoulCommSendIdempotencyCompleteHelpers(t *testing.T) {
	t.Parallel()

	baseItem := testSoulCommSendBaseIdempotencyItem()

	t.Run("success updates provider fields", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		resetCommMockQueryChain(tdb.qIdem)
		tdb.qIdem.On("Update", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			got := testutil.RequireMockArg[[]string](t, args, 0)
			if strings.Join(got, ",") != "Status,ResponseStatus,Provider,ProviderMessageID,UpdatedAt" {
				t.Fatalf("unexpected update fields: %#v", got)
			}
		}).Once()

		s := &Server{store: store.New(tdb.db)}
		if err := s.completeSoulCommSendIdempotencySuccess(context.Background(), baseItem, soulCommSendDelivery{
			provider:          commDeliveryProviderMigadu,
			providerMessageID: "<comm-msg-1@lessersoul.ai>",
			initialStatus:     models.SoulCommMessageStatusSent,
		}); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("failure stores original error", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		resetCommMockQueryChain(tdb.qIdem)
		tdb.qIdem.On("Update", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			got := testutil.RequireMockArg[[]string](t, args, 0)
			if strings.Join(got, ",") != "Status,ResponseStatus,ErrorCode,ErrorMessage,ErrorStatusCode,UpdatedAt" {
				t.Fatalf("unexpected update fields: %#v", got)
			}
		}).Once()

		s := &Server{store: store.New(tdb.db)}
		appErr := apptheory.NewAppTheoryError(commCodeProviderUnavailable, "provider unavailable").WithStatusCode(http.StatusServiceUnavailable)
		if err := s.completeSoulCommSendIdempotencyFailure(context.Background(), baseItem, appErr); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

func TestFinalizeSoulCommSendIdempotency(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommSendMoreTestDB()
	resetCommMockQueryChain(tdb.qIdem)
	tdb.qIdem.On("Update", mock.Anything).Return(nil).Once()
	tdb.qIdem.On("Update", mock.Anything).Return(nil).Once()

	s := &Server{store: store.New(tdb.db)}
	baseItem := testSoulCommSendBaseIdempotencyItem()
	s.finalizeSoulCommSendIdempotency(context.Background(), baseItem, soulCommSendDelivery{
		provider:          commDeliveryProviderMigadu,
		providerMessageID: "<comm-msg-1@lessersoul.ai>",
		initialStatus:     models.SoulCommMessageStatusSent,
	}, nil)
	s.finalizeSoulCommSendIdempotency(context.Background(), baseItem, soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError))
	s.finalizeSoulCommSendIdempotency(context.Background(), nil, soulCommSendDelivery{}, nil)
}

func TestRespondFromSoulCommSendIdempotency(t *testing.T) {
	t.Parallel()

	t.Run("nil item returns internal", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		s := &Server{store: store.New(tdb.db)}
		_, appErr := s.respondFromSoulCommSendIdempotency(context.Background(), nil, newSoulCommSendMetrics("lab", "inst1"))
		if appErr == nil || appErr.Code != commCodeInternal || appErr.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected internal error, got %#v", appErr)
		}
	})

	t.Run("failed item returns original error", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		resetCommMockQueryChain(tdb.qStatus)
		tdb.qStatus.On("All", mock.AnythingOfType("*[]*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulCommMessageStatus](t, args, 0)
			*dest = []*models.SoulCommMessageStatus{}
		}).Once()

		s := &Server{store: store.New(tdb.db)}
		_, appErr := s.respondFromSoulCommSendIdempotency(context.Background(), &models.SoulCommSendIdempotency{
			InstanceSlug:    "inst1",
			AgentID:         soulLifecycleTestAgentIDHex,
			IdempotencyKey:  "dup-key",
			MessageID:       "comm-msg-1",
			ChannelType:     commChannelEmail,
			To:              "alice@example.com",
			Status:          models.SoulCommSendIdempotencyStatusFailed,
			ResponseStatus:  models.SoulCommMessageStatusFailed,
			ErrorCode:       commCodeProviderUnavailable,
			ErrorMessage:    "provider unavailable",
			ErrorStatusCode: http.StatusServiceUnavailable,
			CreatedAt:       testSoulCommSendBaseIdempotencyItem().CreatedAt,
		}, newSoulCommSendMetrics("lab", "inst1"))
		if appErr == nil || appErr.Code != commCodeProviderUnavailable || appErr.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("expected provider unavailable, got %#v", appErr)
		}
	})

	t.Run("processing item returns accepted response", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		resetCommMockQueryChain(tdb.qStatus)
		tdb.qStatus.On("All", mock.AnythingOfType("*[]*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulCommMessageStatus](t, args, 0)
			*dest = []*models.SoulCommMessageStatus{}
		}).Once()

		s := &Server{store: store.New(tdb.db)}
		resp, appErr := s.respondFromSoulCommSendIdempotency(context.Background(), &models.SoulCommSendIdempotency{
			InstanceSlug:   "inst1",
			AgentID:        soulLifecycleTestAgentIDHex,
			IdempotencyKey: "dup-key",
			MessageID:      "comm-msg-1",
			ChannelType:    commChannelEmail,
			To:             "alice@example.com",
			Status:         models.SoulCommSendIdempotencyStatusProcessing,
			ResponseStatus: models.SoulCommMessageStatusAccepted,
			CreatedAt:      testSoulCommSendBaseIdempotencyItem().CreatedAt,
		}, newSoulCommSendMetrics("lab", "inst1"))
		if appErr != nil {
			t.Fatalf("unexpected appErr: %v", appErr)
		}
		assertSoulCommSendResponse(t, resp, models.SoulCommMessageStatusAccepted, "", commChannelEmail, "")
	})
}

func TestClaimSoulCommSendIdempotency_Branches(t *testing.T) {
	t.Parallel()

	req := validatedSoulCommSendRequest{
		channel:        commChannelEmail,
		agentIDHex:     soulLifecycleTestAgentIDHex,
		to:             "alice@example.com",
		subject:        "Hello",
		body:           "hello",
		idempotencyKey: "dup-key",
	}
	createdAt := testSoulCommSendBaseIdempotencyItem().CreatedAt

	t.Run("non-condition create error returns internal", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		resetCommMockQueryChain(tdb.qIdem)
		tdb.qIdem.On("Create").Return(errors.New("boom")).Once()
		s := &Server{store: store.New(tdb.db)}

		_, _, appErr := s.claimSoulCommSendIdempotency(
			&apptheory.Context{Request: apptheory.Request{}},
			&models.InstanceKey{InstanceSlug: "inst1"},
			req,
			"comm-msg-1",
			createdAt,
			newSoulCommSendMetrics("lab", "inst1"),
		)
		if appErr == nil || appErr.Code != commCodeInternal || appErr.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected internal error, got %#v", appErr)
		}
	})

	t.Run("status fallback returns original response", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		resetCommMockQueryChain(tdb.qIdem)
		resetCommMockQueryChain(tdb.qStatus)
		tdb.qIdem.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		tdb.qIdem.On("First", mock.AnythingOfType("*models.SoulCommSendIdempotency")).Return(errors.New("boom")).Once()
		tdb.qStatus.On("All", mock.AnythingOfType("*[]*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulCommMessageStatus](t, args, 0)
			*dest = []*models.SoulCommMessageStatus{{
				MessageID:         "comm-msg-existing",
				InstanceSlug:      "inst1",
				AgentID:           soulLifecycleTestAgentIDHex,
				IdempotencyKey:    "dup-key",
				ChannelType:       commChannelEmail,
				To:                "alice@example.com",
				Provider:          commDeliveryProviderMigadu,
				ProviderMessageID: "<comm-msg-existing@lessersoul.ai>",
				Status:            models.SoulCommMessageStatusSent,
				CreatedAt:         createdAt,
			}}
		}).Once()

		s := &Server{store: store.New(tdb.db)}
		_, resp, appErr := s.claimSoulCommSendIdempotency(
			&apptheory.Context{Request: apptheory.Request{}},
			&models.InstanceKey{InstanceSlug: "inst1"},
			req,
			"comm-msg-1",
			createdAt,
			newSoulCommSendMetrics("lab", "inst1"),
		)
		if appErr != nil {
			t.Fatalf("unexpected appErr: %v", appErr)
		}
		assertSoulCommSendResponse(t, resp, models.SoulCommMessageStatusSent, commDeliveryProviderMigadu, commChannelEmail, "<comm-msg-existing@lessersoul.ai>")
	})
}

func TestHandleSoulCommStatus_ErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("invalid message id", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		_, err := s.handleSoulCommStatus(newCommStatusCtx("", "Bearer k1"))
		appErr := requireCommTheoryError(t, err)
		if appErr.Code != commCodeInvalidRequest || appErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected invalid_request/400, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("message not found", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		tdb.qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(theoryErrors.ErrItemNotFound).Once()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		_, err := s.handleSoulCommStatus(newCommStatusCtx("comm-msg-1", "Bearer k1"))
		appErr := requireCommTheoryError(t, err)
		if appErr.Code != commCodeInvalidRequest || appErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected invalid_request/400, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("identity missing", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		tdb.qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0)
			*dest = models.SoulCommMessageStatus{MessageID: "comm-msg-1", AgentID: soulLifecycleTestAgentIDHex}
		}).Once()
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		_, err := s.handleSoulCommStatus(newCommStatusCtx("comm-msg-1", "Bearer k1"))
		appErr := requireCommTheoryError(t, err)
		if appErr.Code != commCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized/401, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})
}

func TestRequireCommInstanceKey_FallbackAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("internal when store missing", func(t *testing.T) {
		s := &Server{}
		_, err := s.requireCommInstanceKey(&apptheory.Context{})
		appErr := requireCommTheoryError(t, err)
		if appErr.Code != "comm.internal" || appErr.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected internal/500, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("unauthorized without bearer", func(t *testing.T) {
		s := &Server{store: store.New(ttmocks.NewMockExtendedDB())}
		_, err := s.requireCommInstanceKey(&apptheory.Context{})
		appErr := requireCommTheoryError(t, err)
		if appErr.Code != commCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized/401, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("falls back to raw token when hash miss", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "plain-token", InstanceSlug: commWebhookTestInstanceSlug, CreatedAt: time.Now().Add(-time.Hour).UTC()})
		s := &Server{store: store.New(tdb.db)}

		key, err := s.requireCommInstanceKey(&apptheory.Context{
			Request: apptheory.Request{Headers: map[string][]string{"authorization": {"Bearer plain-token"}}},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if key == nil || key.ID != "plain-token" || strings.TrimSpace(key.InstanceSlug) != commWebhookTestInstanceSlug {
			t.Fatalf("unexpected key: %#v", key)
		}
		if key.LastUsedAt.IsZero() {
			t.Fatalf("expected LastUsedAt to be updated")
		}
	})

	t.Run("revoked key skipped", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
			*dest = models.InstanceKey{
				ID:           "plain-token",
				InstanceSlug: commWebhookTestInstanceSlug,
				CreatedAt:    time.Now().Add(-time.Hour).UTC(),
				RevokedAt:    time.Now().UTC(),
			}
		}).Once()
		s := &Server{store: store.New(tdb.db)}

		_, err := s.requireCommInstanceKey(&apptheory.Context{
			Request: apptheory.Request{Headers: map[string][]string{"authorization": {"Bearer plain-token"}}},
		})
		appErr := requireCommTheoryError(t, err)
		if appErr.Code != commCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized/401, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("query error currently degrades to unauthorized", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(errors.New("boom")).Twice()
		s := &Server{store: store.New(tdb.db)}

		_, err := s.requireCommInstanceKey(&apptheory.Context{
			Request: apptheory.Request{Headers: map[string][]string{"authorization": {"Bearer plain-token"}}},
		})
		appErr := requireCommTheoryError(t, err)
		if appErr.Code != commCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized/401, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})
}

func TestRequireCommAgentInstanceAccess_ErrorsAndSuccess(t *testing.T) {
	t.Parallel()

	key := &models.InstanceKey{ID: "k1", InstanceSlug: "inst1"}

	t.Run("blank instance slug unauthorized", func(t *testing.T) {
		s := &Server{store: store.New(ttmocks.NewMockExtendedDB())}
		appErr := s.requireCommAgentInstanceAccess(context.Background(), &models.InstanceKey{}, &models.SoulAgentIdentity{Domain: "example.com"})
		if appErr.Code != commCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized/401, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("blank domain invalid", func(t *testing.T) {
		s := &Server{store: store.New(ttmocks.NewMockExtendedDB())}
		appErr := s.requireCommAgentInstanceAccess(context.Background(), key, &models.SoulAgentIdentity{})
		if appErr.Code != commCodeInvalidRequest || appErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected invalid_request/400, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("domain not found unauthorized", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
		s := &Server{store: store.New(tdb.db)}
		appErr := s.requireCommAgentInstanceAccess(context.Background(), key, &models.SoulAgentIdentity{Domain: "example.com"})
		if appErr.Code != commCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized/401, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("domain query error internal", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(errors.New("boom")).Once()
		s := &Server{store: store.New(tdb.db)}
		appErr := s.requireCommAgentInstanceAccess(context.Background(), key, &models.SoulAgentIdentity{Domain: "example.com"})
		if appErr.Code != "comm.internal" || appErr.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected internal/500, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("instance mismatch unauthorized", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommDomain(t, tdb.qDomain, models.Domain{Domain: "example.com", InstanceSlug: "other", Status: models.DomainStatusVerified})
		s := &Server{store: store.New(tdb.db)}
		appErr := s.requireCommAgentInstanceAccess(context.Background(), key, &models.SoulAgentIdentity{Domain: "example.com"})
		if appErr.Code != "comm.unauthorized" || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized/401, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("success", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		expectCommDomain(t, tdb.qDomain, models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified})
		s := &Server{store: store.New(tdb.db)}
		if appErr := s.requireCommAgentInstanceAccess(context.Background(), key, &models.SoulAgentIdentity{Domain: "example.com"}); appErr != nil {
			t.Fatalf("expected success, got %v", appErr)
		}
	})

	t.Run("managed stage alias resolves through base domain", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
		expectCommDomain(t, tdb.qDomain, models.Domain{
			Domain:             "simulacrum.greater.website",
			InstanceSlug:       "inst1",
			Status:             models.DomainStatusVerified,
			Type:               models.DomainTypePrimary,
			VerificationMethod: "managed",
		})
		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "inst1", HostedBaseDomain: "simulacrum.greater.website"}
		}).Once()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{Stage: "lab"}}
		if appErr := s.requireCommAgentInstanceAccess(context.Background(), key, &models.SoulAgentIdentity{Domain: "dev.simulacrum.greater.website"}); appErr != nil {
			t.Fatalf("expected managed alias success, got %v", appErr)
		}
	})
}

func TestEnforceSoulCommSendGuards_RecipientFirstContactPreferences(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex

	t.Run("recipient reputation requirement blocks first contact", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
			*dest = []*models.SoulAgentCommActivity{}
		}).Twice()
		expectCommEmailIdx(t, tdb.qEmailIdx, "recipient@lessersoul.ai", "0xrecipient")
		minRep := 0.9
		expectCommPrefs(t, tdb.qPrefs, models.SoulAgentContactPreferences{
			AgentID:                       "0xrecipient",
			FirstContactRequireReputation: &minRep,
			UpdatedAt:                     time.Now().UTC(),
		})
		tdb.qReputation.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()

		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		decision, appErr := s.enforceSoulCommSendGuards(
			&apptheory.Context{RequestID: "req-pref-deny"},
			&models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com"},
			validatedSoulCommSendRequest{
				channel:    commChannelEmail,
				agentIDHex: agentID,
				to:         "recipient@lessersoul.ai",
				subject:    "Hello",
				body:       "First contact",
			},
			time.Now().UTC(),
			newSoulCommSendMetrics("lab", "inst1"),
		)
		if appErr == nil {
			t.Fatalf("expected preference violation")
			return
		}
		if appErr.Code != commCodePreferenceViolation || appErr.StatusCode != http.StatusForbidden {
			t.Fatalf("expected %s/403, got %q/%d", commCodePreferenceViolation, appErr.Code, appErr.StatusCode)
		}
		if decision.preferenceRespected == nil || *decision.preferenceRespected {
			t.Fatalf("expected preferenceRespected=false, got %#v", decision.preferenceRespected)
		}
	})

	t.Run("recipient requireSoul is satisfied for a soul sender", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
			*dest = []*models.SoulAgentCommActivity{}
		}).Twice()
		expectCommEmailIdx(t, tdb.qEmailIdx, "recipient@lessersoul.ai", "0xrecipient")
		expectCommPrefs(t, tdb.qPrefs, models.SoulAgentContactPreferences{
			AgentID:                 "0xrecipient",
			FirstContactRequireSoul: true,
			UpdatedAt:               time.Now().UTC(),
		})

		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		decision, appErr := s.enforceSoulCommSendGuards(
			&apptheory.Context{RequestID: "req-pref-allow"},
			&models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com"},
			validatedSoulCommSendRequest{
				channel:    commChannelEmail,
				agentIDHex: agentID,
				to:         "recipient@lessersoul.ai",
				subject:    "Hello",
				body:       "First contact",
			},
			time.Now().UTC(),
			newSoulCommSendMetrics("lab", "inst1"),
		)
		if appErr != nil {
			t.Fatalf("expected allowed first contact, got %v", appErr)
		}
		if decision.preferenceRespected == nil || !*decision.preferenceRespected {
			t.Fatalf("expected preferenceRespected=true, got %#v", decision.preferenceRespected)
		}
	})
}

func TestEnforceSoulCommSendGuards_EmailCCBCCFirstContactPreferences(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	tdb := newSoulCommSendMoreTestDB()
	tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
		*dest = []*models.SoulAgentCommActivity{}
	}).Twice()
	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	expectCommEmailIdx(t, tdb.qEmailIdx, "protected@lessersoul.ai", "0xprotected")
	minRep := 0.9
	expectCommPrefs(t, tdb.qPrefs, models.SoulAgentContactPreferences{
		AgentID:                       "0xprotected",
		FirstContactRequireReputation: &minRep,
		UpdatedAt:                     time.Now().UTC(),
	})
	tdb.qReputation.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	decision, appErr := s.enforceSoulCommSendGuards(
		&apptheory.Context{RequestID: "req-pref-cc-deny"},
		&models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com"},
		validatedSoulCommSendRequest{
			channel:    commChannelEmail,
			agentIDHex: agentID,
			to:         "external@example.com",
			cc:         []string{"protected@lessersoul.ai"},
			bcc:        []string{"protected@lessersoul.ai"},
			subject:    "Hello",
			body:       "First contact",
		},
		time.Now().UTC(),
		newSoulCommSendMetrics("lab", "inst1"),
	)
	if appErr == nil {
		t.Fatalf("expected preference violation")
		return
	}
	if appErr.Code != commCodePreferenceViolation || appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected %s/403, got %q/%d", commCodePreferenceViolation, appErr.Code, appErr.StatusCode)
	}
	if decision.preferenceRespected == nil || *decision.preferenceRespected {
		t.Fatalf("expected preferenceRespected=false, got %#v", decision.preferenceRespected)
	}
}

func TestEnforceSoulCommSendGuards_InReplyToRequiresRecipientThread(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex

	t.Run("arbitrary token rejected", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
			*dest = []*models.SoulAgentCommActivity{}
		}).Times(3)

		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		_, appErr := s.enforceSoulCommSendGuards(
			&apptheory.Context{RequestID: "req-reply-deny"},
			&models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com"},
			validatedSoulCommSendRequest{
				channel:    commChannelEmail,
				agentIDHex: agentID,
				to:         "alice@example.com",
				subject:    "Re",
				body:       "Reply",
				inReplyTo:  "comm-msg-prev",
			},
			time.Now().UTC(),
			newSoulCommSendMetrics("lab", "inst1"),
		)
		if appErr == nil {
			t.Fatalf("expected boundary violation")
			return
		}
		if appErr.Code != commCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden {
			t.Fatalf("expected %s/403, got %q/%d", commCodeBoundaryViolation, appErr.Code, appErr.StatusCode)
		}
		if appErr.Details["field"] != "inReplyTo" || appErr.Details["reason"] != "no_prior_conversation" || appErr.Details["boundary"] != "conversation" {
			t.Fatalf("expected structured inReplyTo boundary details, got %#v", appErr.Details)
		}
	})

	t.Run("cc recipient must be part of thread", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
			*dest = []*models.SoulAgentCommActivity{}
		}).Twice()
		tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
			*dest = []*models.SoulAgentCommActivity{
				{
					ChannelType:  commChannelEmail,
					Direction:    models.SoulCommDirectionInbound,
					Counterparty: "alice@example.com",
					MessageID:    "comm-msg-prev",
					Timestamp:    time.Now().Add(-time.Minute).UTC(),
				},
			}
		}).Once()

		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		_, appErr := s.enforceSoulCommSendGuards(
			&apptheory.Context{RequestID: "req-reply-cc-deny"},
			&models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com"},
			validatedSoulCommSendRequest{
				channel:    commChannelEmail,
				agentIDHex: agentID,
				to:         "alice@example.com",
				cc:         []string{"new@example.com"},
				subject:    "Re",
				body:       "Reply",
				inReplyTo:  "comm-msg-prev",
			},
			time.Now().UTC(),
			newSoulCommSendMetrics("lab", "inst1"),
		)
		if appErr == nil {
			t.Fatalf("expected boundary violation for cc outside thread")
			return
		}
		if appErr.Code != commCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden {
			t.Fatalf("expected %s/403, got %q/%d", commCodeBoundaryViolation, appErr.Code, appErr.StatusCode)
		}
	})

	t.Run("matching recipients pass", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
			*dest = []*models.SoulAgentCommActivity{}
		}).Twice()
		tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
			*dest = []*models.SoulAgentCommActivity{
				{
					ChannelType:  commChannelEmail,
					Direction:    models.SoulCommDirectionInbound,
					Counterparty: "alice@example.com",
					MessageID:    "comm-msg-prev",
					Timestamp:    time.Now().Add(-time.Minute).UTC(),
				},
				{
					ChannelType:  commChannelEmail,
					Direction:    models.SoulCommDirectionInbound,
					Counterparty: "bob@example.com",
					MessageID:    "comm-msg-prev",
					Timestamp:    time.Now().Add(-time.Minute).UTC(),
				},
			}
		}).Once()

		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		if _, appErr := s.enforceSoulCommSendGuards(
			&apptheory.Context{RequestID: "req-reply-pass"},
			&models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com"},
			validatedSoulCommSendRequest{
				channel:    commChannelEmail,
				agentIDHex: agentID,
				to:         "alice@example.com",
				cc:         []string{"bob@example.com"},
				subject:    "Re",
				body:       "Reply",
				inReplyTo:  "comm-msg-prev",
			},
			time.Now().UTC(),
			newSoulCommSendMetrics("lab", "inst1"),
		); appErr != nil {
			t.Fatalf("expected matching reply boundary to pass, got %v", appErr)
		}
	})
}

func TestLookupSoulCommRecipientAgentID_PhoneAndUnsupported(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommSendMoreTestDB()
	tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulPhoneAgentIndex](t, args, 0)
		*dest = models.SoulPhoneAgentIndex{Phone: "+15551234567", AgentID: "0xphone"}
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	agentID, found, err := s.lookupSoulCommRecipientAgentID(context.Background(), commChannelSMS, "+15551234567")
	if err != nil || !found || agentID != "0xphone" {
		t.Fatalf("expected phone lookup hit, got agent=%q found=%v err=%v", agentID, found, err)
	}

	agentID, found, err = s.lookupSoulCommRecipientAgentID(context.Background(), "pager", "whatever")
	if err != nil || found || agentID != "" {
		t.Fatalf("expected unsupported channel miss, got agent=%q found=%v err=%v", agentID, found, err)
	}
}

func TestRequireCommAgentInstanceAccess_ManagedStageDomain(t *testing.T) {
	t.Parallel()

	key := &models.InstanceKey{ID: "k1", InstanceSlug: "inst1"}

	t.Run("success", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
		expectCommDomain(t, tdb.qDomain, models.Domain{
			Domain:             "example.com",
			InstanceSlug:       "inst1",
			Type:               models.DomainTypePrimary,
			Status:             models.DomainStatusVerified,
			VerificationMethod: "managed",
		})
		expectCommInstance(t, tdb.qInstance, models.Instance{Slug: "inst1", HostedBaseDomain: "example.com"})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{Stage: "lab"}}
		if appErr := s.requireCommAgentInstanceAccess(context.Background(), key, &models.SoulAgentIdentity{Domain: "dev.example.com"}); appErr != nil {
			t.Fatalf("expected success, got %v", appErr)
		}
	})

	t.Run("foreign instance unauthorized", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
		expectCommDomain(t, tdb.qDomain, models.Domain{
			Domain:             "example.com",
			InstanceSlug:       "other",
			Type:               models.DomainTypePrimary,
			Status:             models.DomainStatusVerified,
			VerificationMethod: "managed",
		})
		expectCommInstance(t, tdb.qInstance, models.Instance{Slug: "other", HostedBaseDomain: "example.com"})
		s := &Server{store: store.New(tdb.db), cfg: config.Config{Stage: "lab"}}
		appErr := s.requireCommAgentInstanceAccess(context.Background(), key, &models.SoulAgentIdentity{Domain: "dev.example.com"})
		if appErr.Code != "comm.unauthorized" || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized/401, got %q/%d", appErr.Code, appErr.StatusCode)
		}
	})
}

func TestCountSoulOutboundCommSince_FiltersOutboundByChannelAndTime(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommSendMoreTestDB()
	now := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)
	tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
		*dest = []*models.SoulAgentCommActivity{
			nil,
			{ChannelType: "email", Direction: models.SoulCommDirectionOutbound, Timestamp: now.Add(-30 * time.Minute)},
			{ChannelType: "sms", Direction: models.SoulCommDirectionOutbound, Timestamp: now.Add(-20 * time.Minute)},
			{ChannelType: "email", Direction: models.SoulCommDirectionInbound, Timestamp: now.Add(-10 * time.Minute)},
			{ChannelType: "email", Direction: models.SoulCommDirectionOutbound, Timestamp: now.Add(-2 * time.Hour)},
		}
	}).Once()
	s := &Server{store: store.New(tdb.db)}

	count, err := s.countSoulOutboundCommSince(context.Background(), soulLifecycleTestAgentIDHex, "email", now.Add(-1*time.Hour), 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 matching outbound email, got %d", count)
	}

	if _, err := s.countSoulOutboundCommSince(context.Background(), "", "email", now.Add(-1*time.Hour), 10); err == nil {
		t.Fatalf("expected error for blank agent id")
	}
}

func TestBuildOutboundEmailRFC5322_NormalizesRecipientsAndHeaders(t *testing.T) {
	t.Parallel()

	sentAt := time.Date(2026, time.March, 5, 12, 34, 56, 0, time.UTC)
	raw, recipients, appErr := buildOutboundEmailRFC5322(outboundEmailRFC5322Input{
		From:               provisionTestEmailAddress,
		To:                 "alice@example.com",
		CC:                 []string{"bob@example.com", " bob@example.com ", "bad"},
		BCC:                []string{"carol@example.com", "carol@example.com"},
		Subject:            "Hello",
		Body:               "Line 1\n\n",
		MessageID:          "<comm-msg-1@lessersoul.ai>",
		InReplyToMessageID: "comm-msg-prev",
		SentAt:             sentAt,
	})
	if appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}
	if got, want := strings.Join(recipients, ","), "alice@example.com,bob@example.com,carol@example.com"; got != want {
		t.Fatalf("expected recipients %q, got %q", want, got)
	}

	out := string(raw)
	for _, want := range []string{
		"From: " + provisionTestEmailAddress,
		"To: alice@example.com",
		"Reply-To: " + provisionTestEmailAddress,
		"Subject: Hello",
		"Date: Thu, 05 Mar 2026 12:34:56 +0000",
		"Message-ID: <comm-msg-1@lessersoul.ai>",
		"Cc: bob@example.com",
		"In-Reply-To: <comm-msg-prev@lessersoul.ai>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got %q", want, out)
		}
	}
	if !strings.HasSuffix(out, "\r\n\r\nLine 1\r\n") {
		t.Fatalf("expected canonical body termination, got %q", out)
	}
}

func TestBuildOutboundEmailRFC5322_RejectsInvalidPayloads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   outboundEmailRFC5322Input
		code    string
		status  int
		message string
	}{
		{
			name:    "missing subject",
			input:   outboundEmailRFC5322Input{From: "agent@lessersoul.ai", To: "alice@example.com", Body: "hello", MessageID: "msg-1"},
			code:    commCodeInvalidRequest,
			status:  http.StatusBadRequest,
			message: "invalid email payload",
		},
		{
			name:    "invalid replyTo",
			input:   outboundEmailRFC5322Input{From: "agent@lessersoul.ai", To: "alice@example.com", ReplyTo: "bad", Subject: "Hello", Body: "hello", MessageID: "msg-1"},
			code:    commCodeInvalidRequest,
			status:  http.StatusBadRequest,
			message: "replyTo must be an email address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, appErr := buildOutboundEmailRFC5322(tt.input)
			if appErr == nil || appErr.Code != tt.code || appErr.StatusCode != tt.status || appErr.Message != tt.message {
				t.Fatalf("expected %s/%d %q, got %#v", tt.code, tt.status, tt.message, appErr)
			}
		})
	}
}

func TestNormalizeCommAgentID_ValidatesAndNormalizes(t *testing.T) {
	t.Parallel()

	if got, appErr := normalizeCommAgentID(" " + strings.ToUpper(soulLifecycleTestAgentIDHex) + " "); appErr != nil || got != soulLifecycleTestAgentIDHex {
		t.Fatalf("expected normalized agent id %q, got %q appErr=%v", soulLifecycleTestAgentIDHex, got, appErr)
	}

	for _, raw := range []string{"", "0x1234", "0xzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"} {
		if _, appErr := normalizeCommAgentID(raw); appErr == nil {
			t.Fatalf("expected validation error for %q", raw)
		}
	}
}

func TestIsCommProviderUnavailable_RecognizesExpectedErrors(t *testing.T) {
	t.Parallel()

	if isCommProviderUnavailable(nil) {
		t.Fatalf("expected nil error to be available")
	}
	if !isCommProviderUnavailable(context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded to be unavailable")
	}
	if !isCommProviderUnavailable(&net.DNSError{IsTimeout: true}) {
		t.Fatalf("expected timeout net error to be unavailable")
	}
	if isCommProviderUnavailable(errors.New("boom")) {
		t.Fatalf("expected generic error to not be treated as unavailable")
	}
}

func TestParseSoulCommSendRequest_NormalizesAllRecipients(t *testing.T) {
	t.Parallel()

	inReplyTo := " <comm-msg-prev@lessersoul.ai> "
	body, _ := json.Marshal(soulCommSendRequest{
		Channel:        " EMAIL ",
		AgentID:        strings.ToUpper(soulLifecycleTestAgentIDHex),
		To:             "Alice <ALICE@example.com>",
		CC:             []string{" Bob <bob@example.com> ", "bob@example.com", ""},
		BCC:            []string{" CAROL@example.com "},
		Subject:        " Hello ",
		Body:           " Body ",
		ReplyTo:        " reply@example.com ",
		InReplyTo:      &inReplyTo,
		IdempotencyKey: " idem-1 ",
	})
	metrics := newSoulCommSendMetrics(" ", " ")

	req, appErr := parseSoulCommSendRequest(newCommSendCtx(body, nil), metrics)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}
	if req.channel != commChannelEmail || req.agentIDHex != soulLifecycleTestAgentIDHex || req.to != "alice@example.com" {
		t.Fatalf("unexpected normalized request identity/channel/to: %#v", req)
	}
	if !reflect.DeepEqual(req.cc, []string{"bob@example.com"}) || !reflect.DeepEqual(req.bcc, []string{"carol@example.com"}) {
		t.Fatalf("unexpected normalized cc/bcc: cc=%#v bcc=%#v", req.cc, req.bcc)
	}
	if req.subject != "Hello" || req.body != "Body" || req.replyTo != "reply@example.com" || req.inReplyTo != "<comm-msg-prev@lessersoul.ai>" || req.idempotencyKey != "idem-1" {
		t.Fatalf("unexpected normalized envelope fields: %#v", req)
	}
	if metrics.stage != "lab" || metrics.instance != commMetricUnknown || metrics.channel != commChannelEmail {
		t.Fatalf("unexpected metrics after parse: %#v", metrics)
	}
}

func TestParseSoulCommSendRequest_RejectsRecipientFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request soulCommSendRequest
		message string
	}{
		{
			name: "email missing subject",
			request: soulCommSendRequest{
				Channel: commChannelEmail,
				AgentID: soulLifecycleTestAgentIDHex,
				To:      "alice@example.com",
				Body:    "body text",
			},
			message: "subject is required for email",
		},
		{
			name: "invalid cc",
			request: soulCommSendRequest{
				Channel: commChannelEmail,
				AgentID: soulLifecycleTestAgentIDHex,
				To:      "alice@example.com",
				CC:      []string{"not-an-email"},
				Subject: "Hello",
				Body:    "body text",
			},
			message: "cc must be an email address",
		},
		{
			name: "invalid sms recipient",
			request: soulCommSendRequest{
				Channel: commChannelSMS,
				AgentID: soulLifecycleTestAgentIDHex,
				To:      "not-a-phone",
				Body:    "body text",
			},
			message: "to must be an E.164 phone number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.request)
			metrics := newSoulCommSendMetrics("lab", "inst1")
			_, appErr := parseSoulCommSendRequest(newCommSendCtx(body, nil), metrics)
			if appErr == nil || appErr.Message != tt.message || appErr.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected %q bad request, got %#v", tt.message, appErr)
			}
			if metrics.status != commMetricInvalidRequest {
				t.Fatalf("expected invalid request metrics, got %#v", metrics)
			}
		})
	}
}

func TestSoulCommReplyBoundaryMatching(t *testing.T) {
	t.Parallel()

	denied := false
	items := []*models.SoulAgentCommActivity{
		nil,
		{ChannelType: commChannelSMS, Counterparty: "+15550123", MessageID: "sms-1"},
		{ChannelType: commChannelEmail, Counterparty: "Other <other@example.com>", MessageID: "email-2"},
		{ChannelType: commChannelEmail, Counterparty: "Alice <ALICE@example.com>", MessageID: "comm-msg-prev"},
		{ChannelType: commChannelEmail, Counterparty: "bob@example.com", InReplyTo: "<thread-1@lessersoul.ai>"},
		{
			ChannelType:         commChannelEmail,
			Direction:           models.SoulCommDirectionOutbound,
			Counterparty:        "charlie@example.com",
			Action:              "send",
			InReplyTo:           "denied-thread",
			BoundaryCheck:       models.SoulCommBoundaryCheckViolated,
			PreferenceRespected: &denied,
		},
	}

	if !soulCommReplyBoundaryMatchesRecipient(items, commChannelEmail, "alice@example.com", "<comm-msg-prev@lessersoul.ai>") {
		t.Fatalf("expected message-id reference to match normalized email recipient")
	}
	if !soulCommReplyBoundaryMatchesRecipient(items, commChannelEmail, "Bob <bob@example.com>", "thread-1") {
		t.Fatalf("expected prior in-reply-to token to match")
	}
	if soulCommReplyBoundaryMatchesRecipient(items, commChannelEmail, "charlie@example.com", "comm-msg-prev") {
		t.Fatalf("did not expect unmatched recipient to pass")
	}
	if soulCommReplyBoundaryMatchesRecipient(items, commChannelEmail, "charlie@example.com", "denied-thread") {
		t.Fatalf("did not expect denied activity to satisfy reply boundary")
	}
	if !soulCommReplyBoundaryMatchesRecipient(items, commChannelSMS, "+1 (555) 0123", "sms-1") {
		t.Fatalf("expected normalized phone recipient to match")
	}

	recipients := soulCommBoundaryRecipients(validatedSoulCommSendRequest{
		channel: commChannelEmail,
		to:      "Alice <ALICE@example.com>",
		cc:      []string{"bob@example.com", "BOB@example.com"},
		bcc:     []string{"carol@example.com"},
	})
	if !reflect.DeepEqual(recipients, []string{"ALICE@example.com", "BOB@example.com", "carol@example.com"}) {
		t.Fatalf("unexpected email boundary recipients: %#v", recipients)
	}
}

func TestBuildOutboundEmailRFC5322_SanitizesHeaderInjection(t *testing.T) {
	t.Parallel()

	raw, _, appErr := buildOutboundEmailRFC5322(outboundEmailRFC5322Input{
		From:               "agent@lessersoul.ai",
		To:                 "alice@example.com",
		ReplyTo:            "reply@example.com",
		Subject:            "Hello\r\nBcc: victim@example.com",
		Body:               "body",
		MessageID:          "<comm-msg-1@lessersoul.ai>\r\nX-Bad: yes",
		InReplyToMessageID: "<parent@lessersoul.ai>\r\nX-Bad: yes",
		SentAt:             time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
	})
	if appErr != nil {
		t.Fatalf("buildOutboundEmailRFC5322: %v", appErr)
	}
	text := string(raw)
	if strings.Contains(text, "\r\nBcc: victim@example.com") || strings.Contains(text, "\r\nX-Bad: yes") {
		t.Fatalf("header injection was not neutralized: %q", text)
	}
	if !strings.Contains(text, "Subject: Hello Bcc: victim@example.com\r\n") {
		t.Fatalf("expected sanitized subject, got %q", text)
	}
}

func newCommSendCtx(body []byte, inReplyTo *string) *apptheory.Context {
	ctx := &apptheory.Context{
		Request: apptheory.Request{
			Body: body,
			Headers: map[string][]string{
				"authorization": {"Bearer k1"},
			},
		},
	}
	_ = inReplyTo
	return ctx
}

func newCommStatusCtx(messageID string, auth string) *apptheory.Context {
	return &apptheory.Context{
		Params: map[string]string{"messageId": messageID},
		Request: apptheory.Request{
			Headers: map[string][]string{
				"authorization": {auth},
			},
		},
	}
}

func expectCommInstanceKey(t *testing.T, q *ttmocks.MockQuery, key models.InstanceKey) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = key
	}).Once()
}

func expectCommIdentity(t *testing.T, q *ttmocks.MockQuery, identity models.SoulAgentIdentity) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = identity
	}).Once()
}

func expectCommDomain(t *testing.T, q *ttmocks.MockQuery, domain models.Domain) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = domain
	}).Once()
}

func expectCommInstance(t *testing.T, q *ttmocks.MockQuery, inst models.Instance) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = inst
	}).Once()
}

func expectCommChannel(t *testing.T, q *ttmocks.MockQuery, ch models.SoulAgentChannel) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = ch
	}).Once()
}

func expectCommEmailIdx(t *testing.T, q *ttmocks.MockQuery, email string, agentID string) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
		*dest = models.SoulEmailAgentIndex{Email: email, AgentID: agentID}
	}).Once()
}

func expectCommPrefs(t *testing.T, q *ttmocks.MockQuery, prefs models.SoulAgentContactPreferences) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentContactPreferences](t, args, 0)
		*dest = prefs
	}).Once()
}

func expectActiveCommRoute(t *testing.T, tdb soulCommSendMoreTestDB, agentID string, channelType string) {
	t.Helper()

	identity := models.SoulAgentIdentity{
		AgentID:         agentID,
		Domain:          "example.com",
		LocalID:         "agent-alice",
		Status:          models.SoulAgentStatusActive,
		LifecycleStatus: models.SoulAgentStatusActive,
		UpdatedAt:       time.Now().UTC(),
	}
	if channelType == models.SoulChannelTypePhone {
		applyProvisionedPhonePolicy(&identity)
	}
	expectCommIdentity(t, tdb.qIdentity, identity)
	expectCommDomain(t, tdb.qDomain, models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified})

	identifier := provisionTestEmailAddress
	if channelType == models.SoulChannelTypePhone {
		identifier = "+15550142"
	}
	expectCommChannel(t, tdb.qChannel, models.SoulAgentChannel{
		AgentID:       agentID,
		ChannelType:   map[bool]string{true: models.SoulChannelTypePhone, false: models.SoulChannelTypeEmail}[channelType == models.SoulChannelTypePhone],
		Identifier:    identifier,
		Verified:      true,
		ProvisionedAt: time.Now().Add(-time.Hour).UTC(),
		Status:        models.SoulChannelStatusActive,
		UpdatedAt:     time.Now().UTC(),
	})
	tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
		*dest = []*models.SoulAgentCommActivity{}
	}).Twice()
	counterparty := "alice@example.com"
	if channelType == models.SoulChannelTypePhone {
		counterparty = "+15550143"
	}
	tdb.qCommActivity.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
		activities := []*models.SoulAgentCommActivity{
			{
				ChannelType:  map[bool]string{true: commChannelSMS, false: commChannelEmail}[channelType == models.SoulChannelTypePhone],
				Direction:    models.SoulCommDirectionInbound,
				Counterparty: counterparty,
				MessageID:    "comm-msg-prev",
				Timestamp:    time.Now().Add(-time.Minute).UTC(),
			},
		}
		if channelType == models.SoulChannelTypePhone {
			activities = append(activities, &models.SoulAgentCommActivity{
				ChannelType:  commChannelVoice,
				Direction:    models.SoulCommDirectionInbound,
				Counterparty: counterparty,
				MessageID:    "comm-msg-prev",
				Timestamp:    time.Now().Add(-time.Minute).UTC(),
			})
		}
		*dest = activities
	}).Maybe()
}

func resetCommMockQueryChain(q *ttmocks.MockQuery) {
	q.ExpectedCalls = nil
	q.Calls = nil
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("Index", mock.Anything).Return(q).Maybe()
	q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("Limit", mock.Anything).Return(q).Maybe()
	q.On("IfExists").Return(q).Maybe()
	q.On("IfNotExists").Return(q).Maybe()
	q.On("ConsistentRead").Return(q).Maybe()
}

func requireCommTheoryError(t *testing.T, err error) *apptheory.AppTheoryError {
	t.Helper()
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok {
		t.Fatalf("expected AppTheoryError, got %T (%v)", err, err)
	}
	return appErr
}

func strPtr(v string) *string { return &v }
