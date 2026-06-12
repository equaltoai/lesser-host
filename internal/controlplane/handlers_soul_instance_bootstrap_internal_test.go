package controlplane

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestSoulInstanceBootstrapScaffold_RequiresStrictInstanceKey(t *testing.T) {
	t.Parallel()

	t.Run("missing bearer fails closed", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)

		_, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(nil, []byte(`{"domain":"example.com"}`), nil))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulInstanceBootstrapCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized 401, got %#v", appErr)
		}
	})

	t.Run("unknown key hash fails closed", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()

		_, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer raw-key"}, []byte(`{"domain":"example.com"}`), nil))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulInstanceBootstrapCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized 401, got %#v", appErr)
		}
		tdb.qKey.AssertCalled(t, "ConsistentRead")
		tdb.qKey.AssertNumberOfCalls(t, "First", 1)
	})

	t.Run("revoked key fails closed", func(t *testing.T) {
		t.Parallel()

		tdb := newMintConversationTestDB()
		s := newMintConversationServer(tdb)
		tdb.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
			*dest = models.InstanceKey{
				ID:           sha256HexTrimmed("raw-key"),
				InstanceSlug: "inst1",
				CreatedAt:    time.Now().Add(-time.Hour).UTC(),
				RevokedAt:    time.Now().Add(-time.Minute).UTC(),
			}
		}).Once()

		_, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer raw-key"}, []byte(`{"domain":"example.com"}`), nil))
		appErr := requireAppTheoryError(t, err)
		if appErr.Code != soulInstanceBootstrapCodeUnauthorized || appErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized 401, got %#v", appErr)
		}
		tdb.qKey.AssertCalled(t, "ConsistentRead")
	})
}

func TestSoulInstanceBootstrapScaffold_RejectsCrossInstanceDomainBeforeBusinessHandler(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationInstanceDomain(t, tdb, "example.com", "other-inst")

	_, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey}, []byte(`{"domain":"example.com"}`), nil))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected boundary violation 403, got %#v", appErr)
	}
	if appErr.Details["field"] != "domain" || appErr.Details["reason"] != "tenant_domain_mismatch" {
		t.Fatalf("expected tenant-domain mismatch details, got %#v", appErr.Details)
	}
}

func TestSoulInstanceBootstrapScaffold_ValidDomainReachesScaffold(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, "example.com", "inst1")

	_, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey}, []byte(`{"domain":"example.com"}`), nil))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeNotImplemented || appErr.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected scaffold 501, got %#v", appErr)
	}
	if appErr.Details["route"] != soulInstanceBootstrapRouteRegisterBegin {
		t.Fatalf("expected route detail, got %#v", appErr.Details)
	}
}

func TestSoulInstanceBootstrapScaffold_RejectsCrossInstanceRegistration(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationRegistration(t, tdb, reg)
	stubMintConversationInstanceDomain(t, tdb, reg.DomainNormalized, "other-inst")

	_, err := s.handleSoulInstanceAgentRegistrationPrincipalDeclarationPreflight(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		[]byte(`{"principal_address":"0x0000000000000000000000000000000000000001","principal_declaration":"I declare authority.","declared_at":"2026-03-05T12:00:00Z"}`),
		map[string]string{"id": reg.ID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeBoundaryViolation || appErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected boundary violation 403, got %#v", appErr)
	}
	if appErr.Details["reason"] != "tenant_domain_mismatch" {
		t.Fatalf("expected tenant-domain mismatch details, got %#v", appErr.Details)
	}
}

func TestSoulInstanceBootstrapScaffold_ConversationIdsCannotCrossRegistration(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, "inst1")
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()

	_, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": "conv-from-other-registration"},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeNotFound || appErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected conversation not found within registration boundary, got %#v", appErr)
	}
}

func TestSoulInstanceBootstrapScaffold_ConversationRouteReachesScaffold(t *testing.T) {
	t.Parallel()

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, "inst1")
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         models.SoulMintConversationStatusCompleted,
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})

	_, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeNotImplemented || appErr.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected scaffold 501, got %#v", appErr)
	}
	if appErr.Details["route"] != soulInstanceBootstrapRouteConversationComplete {
		t.Fatalf("expected route detail, got %#v", appErr.Details)
	}
}

func TestSoulInstanceBootstrapScaffold_AllRegistrationRoutesReachScaffold(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		route     string
		needsConv bool
		call      func(*Server, *apptheory.Context) (*apptheory.Response, error)
	}{
		{
			name:  "verify",
			route: soulInstanceBootstrapRouteRegisterVerify,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceAgentRegistrationVerify(ctx)
			},
		},
		{
			name:  "mint conversation",
			route: soulInstanceBootstrapRouteConversation,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceMintConversation(ctx)
			},
		},
		{
			name:      "get conversation",
			route:     soulInstanceBootstrapRouteConversationGet,
			needsConv: true,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceGetRegistrationMintConversation(ctx)
			},
		},
		{
			name:      "finalize preflight",
			route:     soulInstanceBootstrapRouteFinalizePreflight,
			needsConv: true,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceFinalizeMintConversationPreflight(ctx)
			},
		},
		{
			name:      "finalize begin",
			route:     soulInstanceBootstrapRouteFinalizeBegin,
			needsConv: true,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceBeginFinalizeMintConversation(ctx)
			},
		},
		{
			name:      "finalize",
			route:     soulInstanceBootstrapRouteFinalize,
			needsConv: true,
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handleSoulInstanceFinalizeMintConversation(ctx)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tdb := newMintConversationTestDB()
			s := newMintConversationServer(tdb)
			reg := mintConversationHandleReg()
			expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, "inst1")
			stubMintConversationRegistration(t, tdb, reg)
			stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, "inst1")

			params := map[string]string{"id": reg.ID}
			if tc.needsConv {
				params["conversationId"] = mintConversationTestConversationID
				stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
					AgentID:        reg.AgentID,
					ConversationID: mintConversationTestConversationID,
					Status:         models.SoulMintConversationStatusCompleted,
					CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
				})
			}

			_, err := tc.call(s, newSoulInstanceBootstrapContext(
				map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
				nil,
				params,
			))
			appErr := requireAppTheoryError(t, err)
			if appErr.Code != soulInstanceBootstrapCodeNotImplemented || appErr.StatusCode != http.StatusNotImplemented {
				t.Fatalf("expected scaffold 501, got %#v", appErr)
			}
			if appErr.Details["route"] != tc.route {
				t.Fatalf("expected route %q detail, got %#v", tc.route, appErr.Details)
			}
		})
	}
}

func TestSoulInstanceBootstrapHelperErrors(t *testing.T) {
	t.Parallel()

	if _, appErr := normalizeSoulInstanceBootstrapDomain("bad domain"); appErr == nil || appErr.Code != soulInstanceBootstrapCodeInvalidRequest {
		t.Fatalf("expected invalid domain error, got %#v", appErr)
	}

	if got := soulInstanceBootstrapErrorFromAppError(nil); got != nil {
		t.Fatalf("expected nil app error mapping, got %#v", got)
	}
	cases := []struct {
		name       string
		in         *apptheory.AppError
		wantCode   string
		wantStatus int
	}{
		{"unauthorized", &apptheory.AppError{Code: appErrCodeUnauthorized, Message: "nope"}, soulInstanceBootstrapCodeUnauthorized, http.StatusUnauthorized},
		{"bad request", &apptheory.AppError{Code: appErrCodeBadRequest, Message: "bad"}, soulInstanceBootstrapCodeInvalidRequest, http.StatusBadRequest},
		{"conflict", &apptheory.AppError{Code: soulMintAppErrCodeConflict, Message: "conflict"}, soulInstanceBootstrapCodeBoundaryViolation, http.StatusForbidden},
		{"internal default", &apptheory.AppError{Code: soulMintAppErrCodeInternal, Message: "boom"}, soulInstanceBootstrapCodeInternal, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			appErr := soulInstanceBootstrapErrorFromAppError(tc.in)
			if appErr.Code != tc.wantCode || appErr.StatusCode != tc.wantStatus {
				t.Fatalf("expected %s/%d, got %#v", tc.wantCode, tc.wantStatus, appErr)
			}
		})
	}
}

func newSoulInstanceBootstrapContext(headers map[string]string, body []byte, params map[string]string) *apptheory.Context {
	h := map[string][]string{}
	for k, v := range headers {
		h[strings.ToLower(strings.TrimSpace(k))] = []string{v}
	}
	return &apptheory.Context{
		RequestID: "req-instance-bootstrap",
		Params:    params,
		Request: apptheory.Request{
			Headers: h,
			Body:    body,
		},
	}
}

func stubSoulInstanceBootstrapDomainAndInstance(t *testing.T, tdb *mintConversationTestDB, domain string, instanceSlug string) {
	t.Helper()
	stubMintConversationInstanceDomain(t, tdb, domain, instanceSlug)
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: instanceSlug, HostedBaseDomain: domain}
	}).Once()
}
