package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulUpdateAgentChannelPreferences_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentID := soulLifecycleTestAgentIDHex
	identityUpdatedAt := time.Date(2026, 3, 6, 15, 4, 5, 0, time.UTC)

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			LocalID:         "agent-alice",
			LifecycleStatus: models.SoulAgentStatusActive,
			UpdatedAt:       identityUpdatedAt,
		}
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()
	tdb.qPrefs.On("CreateOrUpdate").Return(nil).Once()

	ctx := &apptheory.Context{
		AuthIdentity: "operator@example.com",
		RequestID:    "req-contact-prefs",
		Params:       map[string]string{"agentId": agentID},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	ctx.Request.Body = []byte(`{
		"contactPreferences": {
			"preferred": " Email ",
			"fallback": " SMS ",
			"availability": {
				"schedule": " Custom ",
				"timezone": " America/New_York ",
				"windows": [
					{"days": [" Mon ", "TUE"], "startTime": "09:00", "endTime": "17:00"}
				]
			},
			"responseExpectation": {"target": "PT4H", "guarantee": " Best-Effort "},
			"rateLimits": {"email": {"maxInboundPerHour": 2}},
			"languages": [" EN ", "fr"],
			"contentTypes": [" text/plain ", "application/json "],
			"firstContact": {"requireSoul": true, "requireReputation": 0.5, "introductionExpected": true}
		}
	}`)

	resp, err := s.handleSoulUpdateAgentChannelPreferences(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.Status, "body=%q", string(resp.Body))

	var out soulPublicAgentContactPreferencesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, agentID, out.AgentID)
	require.NotNil(t, out.ContactPreferences)
	require.Equal(t, commChannelEmail, out.ContactPreferences.Preferred)
	require.Equal(t, "sms", out.ContactPreferences.Fallback)
	require.Equal(t, "custom", out.ContactPreferences.Availability.Schedule)
	require.Equal(t, "America/New_York", out.ContactPreferences.Availability.Timezone)
	require.Len(t, out.ContactPreferences.Availability.Windows, 1)
	require.Len(t, out.ContactPreferences.Availability.Windows[0].Days, 2)
	require.Equal(t, "mon", out.ContactPreferences.Availability.Windows[0].Days[0])
	require.Equal(t, "tue", out.ContactPreferences.Availability.Windows[0].Days[1])
	require.Equal(t, "best-effort", out.ContactPreferences.ResponseExpectation.Guarantee)
	require.Len(t, out.ContactPreferences.Languages, 2)
	require.Equal(t, "en", out.ContactPreferences.Languages[0])
	require.NotEmpty(t, out.ContactPreferences.ContentTypes)
	require.Equal(t, "text/plain", out.ContactPreferences.ContentTypes[0])
	require.NotNil(t, out.ContactPreferences.FirstContact)
	require.NotNil(t, out.ContactPreferences.FirstContact.RequireReputation)
	require.Equal(t, 0.5, *out.ContactPreferences.FirstContact.RequireReputation)

	updatedAt, parseErr := time.Parse(time.RFC3339Nano, out.UpdatedAt)
	require.NoError(t, parseErr, "expected RFC3339 updatedAt, got %q", out.UpdatedAt)
	require.False(t, updatedAt.Before(identityUpdatedAt), "expected updatedAt to move forward from identity timestamp, got %q", out.UpdatedAt)

	tdb.qPrefs.AssertNumberOfCalls(t, "CreateOrUpdate", 1)
	tdb.qAudit.AssertNumberOfCalls(t, "Create", 1)
}

func TestHandleSoulUpdateAgentChannelPreferences_InvalidPreferences(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentID := soulLifecycleTestAgentIDHex

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			LocalID:         "agent-alice",
			LifecycleStatus: models.SoulAgentStatusActive,
		}
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	ctx := &apptheory.Context{
		AuthIdentity: "operator@example.com",
		Params:       map[string]string{"agentId": agentID},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	ctx.Request.Body = []byte(`{
		"contactPreferences": {
			"preferred": "pager",
			"availability": {"schedule": "always"},
			"responseExpectation": {"target": "PT4H", "guarantee": "best-effort"},
			"languages": ["en"]
		}
	}`)

	resp, err := s.handleSoulUpdateAgentChannelPreferences(ctx)
	if resp != nil {
		t.Fatalf("expected nil response on validation error, got %#v", resp)
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected app error, got %v", err)
	}
	if appErr.Code != appErrCodeBadRequest || !strings.Contains(appErr.Message, "invalid contactPreferences") {
		t.Fatalf("expected invalid contactPreferences bad_request, got %v", appErr)
	}

	tdb.qPrefs.AssertNumberOfCalls(t, "CreateOrUpdate", 0)
	tdb.qAudit.AssertNumberOfCalls(t, "Create", 0)
}
