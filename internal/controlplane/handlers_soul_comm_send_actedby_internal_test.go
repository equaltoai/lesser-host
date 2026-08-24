package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	commSendActedByTestUser    = "shared_caller-01"
	commSendActedByExistingMsg = "comm-msg-existing"
)

func newActedByEmailSendServer(t *testing.T, tdb soulCommSendMoreTestDB, sendCalled *bool) *Server {
	t.Helper()
	return &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{SoulEnabled: true, Stage: "lab"},
		ssmGetParameter: func(ctx context.Context, name string) (string, error) {
			return "pw", nil
		},
		migaduSendSMTP: func(ctx context.Context, username string, password string, from string, recipients []string, data []byte) error {
			*sendCalled = true
			if username != provisionTestEmailAddress || password != "pw" || from != provisionTestEmailAddress {
				t.Fatalf("unexpected smtp args username=%q password=%q from=%q", username, password, from)
			}
			if len(recipients) != 1 || recipients[0] != commSendTestEmailRecipient {
				t.Fatalf("unexpected recipients: %#v", recipients)
			}
			return nil
		},
	}
}

func expectActedByEmailRoute(t *testing.T, tdb soulCommSendMoreTestDB, agentID string) {
	t.Helper()
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
	expectActiveCommRoute(t, tdb, agentID, models.SoulChannelTypeEmail)
	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
}

func actedByEmailSendBody(agentID string, actedBy string) map[string]any {
	body := map[string]any{
		"channel":        "email",
		"agentId":        agentID,
		"to":             commSendTestEmailRecipient,
		"subject":        "Hello",
		"body":           "Test",
		"idempotencyKey": "actedby-key",
	}
	if actedBy != "" {
		body["actedBy"] = actedBy
	}
	return body
}

func capturedModelArgs[T any](calls []mock.Call) []*T {
	out := []*T{}
	for _, call := range calls {
		if call.Method != "Model" || len(call.Arguments) == 0 {
			continue
		}
		if m, ok := call.Arguments[0].(*T); ok {
			out = append(out, m)
		}
	}
	return out
}

// capturedPersistedCommActivity returns the comm activity record written by the
// send path, skipping the empty Model prototypes used by read-side guard queries
// (rate-limit counts and reply-boundary scans).
func capturedPersistedCommActivity(calls []mock.Call) *models.SoulAgentCommActivity {
	for _, activity := range capturedModelArgs[models.SoulAgentCommActivity](calls) {
		if activity != nil && strings.TrimSpace(activity.MessageID) != "" {
			return activity
		}
	}
	return nil
}

func TestHandleSoulCommSend_ActedByAcceptedPersistedEchoed(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommSendMoreTestDB()
	agentID := soulLifecycleTestAgentIDHex
	expectActedByEmailRoute(t, tdb, agentID)

	var sendCalled bool
	s := newActedByEmailSendServer(t, tdb, &sendCalled)

	resp, err := s.handleSoulCommSend(newCommSendCtx(mustMarshalCommSendBody(t, actedByEmailSendBody(agentID, commSendActedByTestUser)), nil))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !sendCalled {
		t.Fatalf("expected smtp send called")
	}
	assertSoulCommSendResponse(t, resp, models.SoulCommMessageStatusSent, commDeliveryProviderMigadu, commChannelEmail, "")

	var out soulCommSendResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ActedBy != commSendActedByTestUser {
		t.Fatalf("expected actedBy echo %q, got %#v", commSendActedByTestUser, out)
	}

	statuses := capturedModelArgs[models.SoulCommMessageStatus](tdb.db.Calls)
	if len(statuses) == 0 {
		t.Fatalf("expected a persisted SoulCommMessageStatus model")
	}
	if statuses[0].ActedBy != commSendActedByTestUser {
		t.Fatalf("expected persisted status actedBy %q, got %#v", commSendActedByTestUser, statuses[0].ActedBy)
	}

	idems := capturedModelArgs[models.SoulCommSendIdempotency](tdb.db.Calls)
	if len(idems) == 0 {
		t.Fatalf("expected a persisted SoulCommSendIdempotency model")
	}
	if idems[0].ActedBy != commSendActedByTestUser {
		t.Fatalf("expected persisted idempotency actedBy %q, got %#v", commSendActedByTestUser, idems[0].ActedBy)
	}

	activity := capturedPersistedCommActivity(tdb.db.Calls)
	if activity == nil {
		t.Fatalf("expected a persisted SoulAgentCommActivity model")
	}
	if activity.ActedBy != commSendActedByTestUser {
		t.Fatalf("expected persisted activity actedBy %q, got %#v", commSendActedByTestUser, activity.ActedBy)
	}

	audits := capturedModelArgs[models.AuditLogEntry](tdb.db.Calls)
	if len(audits) == 0 {
		t.Fatalf("expected a persisted AuditLogEntry model")
	}
	if audits[0].ActedBy != commSendActedByTestUser {
		t.Fatalf("expected persisted audit actedBy %q, got %#v", commSendActedByTestUser, audits[0].ActedBy)
	}
}

func TestHandleSoulCommSend_AbsentActedByUnchanged(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommSendMoreTestDB()
	agentID := soulLifecycleTestAgentIDHex
	expectActedByEmailRoute(t, tdb, agentID)

	var sendCalled bool
	s := newActedByEmailSendServer(t, tdb, &sendCalled)

	resp, err := s.handleSoulCommSend(newCommSendCtx(mustMarshalCommSendBody(t, actedByEmailSendBody(agentID, "")), nil))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !sendCalled {
		t.Fatalf("expected smtp send called")
	}
	assertSoulCommSendResponse(t, resp, models.SoulCommMessageStatusSent, commDeliveryProviderMigadu, commChannelEmail, "")

	var raw map[string]any
	if err := json.Unmarshal(resp.Body, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["actedBy"]; ok {
		t.Fatalf("expected no actedBy key when absent, got %v", raw["actedBy"])
	}

	statuses := capturedModelArgs[models.SoulCommMessageStatus](tdb.db.Calls)
	if len(statuses) == 0 {
		t.Fatalf("expected a persisted SoulCommMessageStatus model")
	}
	if statuses[0].ActedBy != "" {
		t.Fatalf("expected empty persisted actedBy, got %#v", statuses[0].ActedBy)
	}

	activity := capturedPersistedCommActivity(tdb.db.Calls)
	if activity == nil {
		t.Fatalf("expected a persisted SoulAgentCommActivity model")
	}
	if activity.ActedBy != "" {
		t.Fatalf("expected empty persisted activity actedBy, got %#v", activity.ActedBy)
	}

	audits := capturedModelArgs[models.AuditLogEntry](tdb.db.Calls)
	if len(audits) == 0 {
		t.Fatalf("expected a persisted AuditLogEntry model")
	}
	if audits[0].ActedBy != "" {
		t.Fatalf("expected empty persisted audit actedBy, got %#v", audits[0].ActedBy)
	}
}

func TestParseSoulCommSendRequest_ActedByShape(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	valid := []string{"a", "0", commSendActedByTestUser, "user-name_2", strings.Repeat("z", 30)}
	for _, actedBy := range valid {
		body := actedByEmailSendBody(agentID, actedBy)
		delete(body, "idempotencyKey")
		ctx := &apptheory.Context{Request: apptheory.Request{Body: mustMarshalCommSendBody(t, body)}}
		req, appErr := parseSoulCommSendRequest(ctx, newSoulCommSendMetrics("lab", "inst1"))
		if appErr != nil {
			t.Fatalf("expected actedBy %q to parse, got %v", actedBy, appErr)
		}
		if req.actedBy != actedBy {
			t.Fatalf("expected actedBy %q, got %q", actedBy, req.actedBy)
		}
	}

	invalid := []string{"Alice", "has space", "bad!char", "UPPER", strings.Repeat("z", 31), "evil@example.com", "../x"}
	for _, actedBy := range invalid {
		body := actedByEmailSendBody(agentID, actedBy)
		delete(body, "idempotencyKey")
		ctx := &apptheory.Context{Request: apptheory.Request{Body: mustMarshalCommSendBody(t, body)}}
		_, appErr := parseSoulCommSendRequest(ctx, newSoulCommSendMetrics("lab", "inst1"))
		if appErr == nil {
			t.Fatalf("expected actedBy %q to fail closed", actedBy)
		}
		if appErr.Code != commCodeInvalidRequest || appErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected %s/400 for %q, got %q/%d", commCodeInvalidRequest, actedBy, appErr.Code, appErr.StatusCode)
		}
		if appErr.Details["field"] != "actedBy" {
			t.Fatalf("expected error.details.field=actedBy for %q, got %#v", actedBy, appErr.Details)
		}
	}

	// Whitespace-only actedBy is treated as absent (current behavior).
	body := actedByEmailSendBody(agentID, " ")
	delete(body, "idempotencyKey")
	ctx := &apptheory.Context{Request: apptheory.Request{Body: mustMarshalCommSendBody(t, body)}}
	req, appErr := parseSoulCommSendRequest(ctx, newSoulCommSendMetrics("lab", "inst1"))
	if appErr != nil {
		t.Fatalf("unexpected err: %v", appErr)
	}
	if req.actedBy != "" {
		t.Fatalf("expected whitespace-only actedBy to normalize to absent, got %q", req.actedBy)
	}
}

func TestHandleSoulCommSend_MalformedActedByFailsClosed(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommSendMoreTestDB()
	agentID := soulLifecycleTestAgentIDHex
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})

	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	_, err := s.handleSoulCommSend(newCommSendCtx(mustMarshalCommSendBody(t, actedByEmailSendBody(agentID, "Not A User!")), nil))
	appErr := requireCommTheoryError(t, err)
	if appErr.Code != commCodeInvalidRequest || appErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected %s/400, got %q/%d", commCodeInvalidRequest, appErr.Code, appErr.StatusCode)
	}
	if appErr.Details["field"] != "actedBy" {
		t.Fatalf("expected error.details.field=actedBy, got %#v", appErr.Details)
	}
}

func TestHandleSoulCommSend_ActedByNeverAltersAuthz(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex

	t.Run("no bearer still 401 with actedBy", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		ctx := &apptheory.Context{
			RequestID: "r-comm-actedby-unauth",
			Request:   apptheory.Request{Body: mustMarshalCommSendBody(t, actedByEmailSendBody(agentID, commSendActedByTestUser))},
		}
		_, err := s.handleSoulCommSend(ctx)
		assertCommTheoryErrorCode(t, err, commCodeUnauthorized, http.StatusUnauthorized)
	})

	t.Run("unknown instance key still 401 with actedBy", func(t *testing.T) {
		tdb := newSoulCommSendMoreTestDB()
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Maybe()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

		ctx := &apptheory.Context{
			RequestID: "r-comm-actedby-bad-key",
			Request: apptheory.Request{
				Body:    mustMarshalCommSendBody(t, actedByEmailSendBody(agentID, commSendActedByTestUser)),
				Headers: map[string][]string{"authorization": {"Bearer unknown-key"}},
			},
		}
		_, err := s.handleSoulCommSend(ctx)
		assertCommTheoryErrorCode(t, err, commCodeUnauthorized, http.StatusUnauthorized)
	})
}

func TestSoulCommSendRequestHash_ActedByParticipates(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	base := validatedSoulCommSendRequest{
		channel:        commChannelEmail,
		agentIDHex:     agentID,
		to:             commSendTestEmailRecipient,
		subject:        "Hello",
		body:           "Test",
		idempotencyKey: "dup-key",
	}
	without := soulCommSendRequestHash("inst1", base)

	withActedBy := base
	withActedBy.actedBy = commSendActedByTestUser
	with := soulCommSendRequestHash("inst1", withActedBy)

	if without == with {
		t.Fatalf("expected actedBy to change the idempotency request hash")
	}
	if with != soulCommSendRequestHash("inst1", withActedBy) {
		t.Fatalf("expected deterministic hash for identical actedBy")
	}

	other := base
	other.actedBy = "another_user"
	if soulCommSendRequestHash("inst1", other) == with {
		t.Fatalf("expected different actedBy values to produce different hashes")
	}
}

func TestHandleSoulCommSend_IdempotencyConflictOnDifferentActedBy(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommSendMoreTestDB()
	agentID := soulLifecycleTestAgentIDHex
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
	expectActiveCommRoute(t, tdb, agentID, models.SoulChannelTypeEmail)
	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	// Stored claim: identical payload WITHOUT actedBy (owner send). A same-key resend
	// with actedBy must conflict, proving actedBy participates in idempotency.
	storedHash := soulCommSendRequestHash("inst1", validatedSoulCommSendRequest{
		channel:        commChannelEmail,
		agentIDHex:     agentID,
		to:             commSendTestEmailRecipient,
		subject:        "Hello",
		body:           "Test",
		idempotencyKey: "actedby-key",
	})

	resetCommMockQueryChain(tdb.qIdem)
	tdb.qIdem.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	tdb.qIdem.On("First", mock.AnythingOfType("*models.SoulCommSendIdempotency")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommSendIdempotency](t, args, 0)
		*dest = models.SoulCommSendIdempotency{
			InstanceSlug:   "inst1",
			AgentID:        agentID,
			IdempotencyKey: "actedby-key",
			RequestHash:    storedHash,
			MessageID:      commSendActedByExistingMsg,
			ChannelType:    commChannelEmail,
			To:             commSendTestEmailRecipient,
			Status:         models.SoulCommSendIdempotencyStatusSucceeded,
			ResponseStatus: models.SoulCommMessageStatusSent,
			CreatedAt:      time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
		}
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	_, err := s.handleSoulCommSend(newCommSendCtx(mustMarshalCommSendBody(t, actedByEmailSendBody(agentID, commSendActedByTestUser)), nil))
	assertCommTheoryErrorCode(t, err, commCodeIdempotencyConflict, http.StatusConflict)
}

func TestHandleSoulCommSend_IdempotentReplayEchoesActedBy(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommSendMoreTestDB()
	agentID := soulLifecycleTestAgentIDHex
	expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
	expectActiveCommRoute(t, tdb, agentID, models.SoulChannelTypeEmail)
	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	storedHash := soulCommSendRequestHash("inst1", validatedSoulCommSendRequest{
		channel:        commChannelEmail,
		agentIDHex:     agentID,
		to:             commSendTestEmailRecipient,
		subject:        "Hello",
		body:           "Test",
		idempotencyKey: "actedby-key",
		actedBy:        commSendActedByTestUser,
	})

	resetCommMockQueryChain(tdb.qIdem)
	resetCommMockQueryChain(tdb.qStatus)
	tdb.qIdem.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	tdb.qIdem.On("First", mock.AnythingOfType("*models.SoulCommSendIdempotency")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommSendIdempotency](t, args, 0)
		*dest = models.SoulCommSendIdempotency{
			InstanceSlug:   "inst1",
			AgentID:        agentID,
			IdempotencyKey: "actedby-key",
			RequestHash:    storedHash,
			MessageID:      commSendActedByExistingMsg,
			ChannelType:    commChannelEmail,
			To:             commSendTestEmailRecipient,
			ActedBy:        commSendActedByTestUser,
			Status:         models.SoulCommSendIdempotencyStatusSucceeded,
			ResponseStatus: models.SoulCommMessageStatusSent,
			CreatedAt:      time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
		}
	}).Once()
	tdb.qStatus.On("All", mock.AnythingOfType("*[]*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulCommMessageStatus](t, args, 0)
		*dest = []*models.SoulCommMessageStatus{{
			MessageID:      commSendActedByExistingMsg,
			InstanceSlug:   "inst1",
			AgentID:        agentID,
			IdempotencyKey: "actedby-key",
			ChannelType:    commChannelEmail,
			To:             commSendTestEmailRecipient,
			ActedBy:        commSendActedByTestUser,
			Provider:       commDeliveryProviderMigadu,
			Status:         models.SoulCommMessageStatusSent,
			CreatedAt:      time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
		}}
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	resp, err := s.handleSoulCommSend(newCommSendCtx(mustMarshalCommSendBody(t, actedByEmailSendBody(agentID, commSendActedByTestUser)), nil))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var out soulCommSendResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.MessageID != commSendActedByExistingMsg || out.ActedBy != commSendActedByTestUser {
		t.Fatalf("expected replay to echo original message and actedBy, got %#v", out)
	}
}

func TestHandleSoulCommStatus_EchoesActedBy(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	newStatusServer := func(t *testing.T, actedBy string) *Server {
		t.Helper()
		tdb := newSoulCommSendMoreTestDB()
		expectCommInstanceKey(t, tdb.qKey, models.InstanceKey{ID: "k1", InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour).UTC()})
		tdb.qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0)
			*dest = models.SoulCommMessageStatus{
				MessageID:    "comm-msg-1",
				InstanceSlug: "inst1",
				AgentID:      agentID,
				ChannelType:  commChannelEmail,
				To:           commSendTestEmailRecipient,
				Status:       models.SoulCommMessageStatusSent,
				ActedBy:      actedBy,
				CreatedAt:    time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC),
			}
		}).Once()
		expectCommIdentity(t, tdb.qIdentity, models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			LocalID:         "agent-alice",
			Status:          models.SoulAgentStatusActive,
			LifecycleStatus: models.SoulAgentStatusActive,
			UpdatedAt:       time.Now().UTC(),
		})
		expectCommDomain(t, tdb.qDomain, models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified})
		return &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
	}

	t.Run("echoes actedBy when present", func(t *testing.T) {
		s := newStatusServer(t, commSendActedByTestUser)
		resp, err := s.handleSoulCommStatus(newCommStatusCtx("comm-msg-1", "Bearer k1"))
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		var out soulCommStatusResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.ActedBy != commSendActedByTestUser {
			t.Fatalf("expected actedBy echo %q, got %#v", commSendActedByTestUser, out)
		}
	})

	t.Run("omits actedBy when absent", func(t *testing.T) {
		s := newStatusServer(t, "")
		resp, err := s.handleSoulCommStatus(newCommStatusCtx("comm-msg-1", "Bearer k1"))
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(resp.Body, &raw); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := raw["actedBy"]; ok {
			t.Fatalf("expected no actedBy key when absent, got %v", raw["actedBy"])
		}
	})
}
