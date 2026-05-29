package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func newActivityTestServer(t *testing.T, inst models.Instance, handler http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()
	tdb, qInst := newCostTestDB()
	stubCostInstanceFirst(t, qInst, inst)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	s := &Server{
		cfg:                  config.Config{Stage: "lab", ManagedInstanceRoleName: "OrganizationAccountAccessRole", ManagedDefaultRegion: "us-east-1"},
		store:                store.New(tdb),
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return testRawKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
	}
	return s, ts
}

func TestHandlePortalGetInstanceActivity_SendsBearerAuth(t *testing.T) {
	t.Parallel()

	var gotAuth string
	var gotPath string
	s, _ := newActivityTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		// Return a single week in the current month with 42 statuses.
		_, _ = w.Write([]byte(`[
			{"week":"1777593600","statuses":"42","logins":"10","registrations":"3"}
		]`))
	})

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testCostSlug1},
	}
	resp, err := s.handlePortalGetInstanceActivity(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)
	require.Equal(t, "Bearer "+testRawKey, gotAuth)
	require.Equal(t, "/api/v1/instance/activity", gotPath)
}

func TestHandlePortalGetInstanceActivity_MapsStatuses(t *testing.T) {
	t.Parallel()

	s, _ := newActivityTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Current month (May 2026): week timestamps are within May 2026
		// May 1 2026 00:00:00 UTC = 1777593600
		// May 8 2026 00:00:00 UTC = 1778198400
		// May 15 2026 00:00:00 UTC = 1778803200
		_, _ = w.Write([]byte(`[
			{"week":"1777593600","statuses":"100","logins":"20","registrations":"5"},
			{"week":"1778198400","statuses":"200","logins":"30","registrations":"7"},
			{"week":"1778803200","statuses":"50","logins":"15","registrations":"2"}
		]`))
	})

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testCostSlug1},
	}
	resp, err := s.handlePortalGetInstanceActivity(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var body portalInstanceActivityResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, testCostSlug1, body.InstanceSlug)
	// 100 + 200 + 50 = 350
	require.Equal(t, int64(350), body.Statuses)
	require.Equal(t, 3, body.Weeks)
}

func TestHandlePortalGetInstanceActivity_FiltersOutOfMonthWeeks(t *testing.T) {
	t.Parallel()

	s, _ := newActivityTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// One week in Feb 2026 (old), one in May 2026 (current)
		// Feb 1 2026 00:00:00 UTC = 1769904000
		// May 15 2026 00:00:00 UTC = 1778803200
		_, _ = w.Write([]byte(`[
			{"week":"1769904000","statuses":"999","logins":"50","registrations":"10"},
			{"week":"1778803200","statuses":"75","logins":"12","registrations":"3"}
		]`))
	})

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testCostSlug1},
	}
	resp, err := s.handlePortalGetInstanceActivity(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	var body portalInstanceActivityResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	// Only the May 2026 week should be counted: 75
	require.Equal(t, int64(75), body.Statuses)
	require.Equal(t, 1, body.Weeks)
}

func TestHandlePortalGetInstanceActivity_RequireInstanceAccessEnforced(t *testing.T) {
	t.Parallel()

	// Instance owned by "bob", caller is "alice" — should get forbidden.
	s, _ := newActivityTestServer(t, baseCostInstance("bob"), func(w http.ResponseWriter, r *http.Request) {
		// Should not be reached
		t.Error("handler should not be called when ownership check fails")
	})

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testCostSlug1},
	}
	_, err := s.handlePortalGetInstanceActivity(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.forbidden", appErr.Code)
}

func TestHandlePortalGetInstanceActivity_OperatorBypassesOwnership(t *testing.T) {
	t.Parallel()

	s, _ := newActivityTestServer(t, baseCostInstance("bob"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"week":"1778803200","statuses":"10","logins":"1","registrations":"0"}]`))
	})

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testCostSlug1},
	}
	// Mark context as operator (operator can access any instance).
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handlePortalGetInstanceActivity(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var body portalInstanceActivityResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, int64(10), body.Statuses)
}

func TestHandlePortalGetInstanceActivity_ZeroStatusesOnEmptyActivity(t *testing.T) {
	t.Parallel()

	s, _ := newActivityTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testCostSlug1},
	}
	resp, err := s.handlePortalGetInstanceActivity(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	var body portalInstanceActivityResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, int64(0), body.Statuses)
	require.Equal(t, 0, body.Weeks)
}

func TestHandlePortalGetInstanceActivity_NoActivityDataYet(t *testing.T) {
	t.Parallel()

	// Activity entries exist but none for current month
	s, _ := newActivityTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// All weeks from 2025 — none in current month (May 2026)
		_, _ = w.Write([]byte(`[
			{"week":"1746057600","statuses":"50","logins":"10","registrations":"2"}
		]`))
	})

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testCostSlug1},
	}
	resp, err := s.handlePortalGetInstanceActivity(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	var body portalInstanceActivityResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, int64(0), body.Statuses)
	require.Equal(t, 0, body.Weeks)
}

func TestHandlePortalGetInstanceActivity_UpstreamErrorReturnsError(t *testing.T) {
	t.Parallel()

	s, _ := newActivityTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testCostSlug1},
	}
	_, err := s.handlePortalGetInstanceActivity(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.upstream_error", appErr.Code)
}
