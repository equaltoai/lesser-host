package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// currentMonthTS returns a Unix timestamp (as a string) for a point within
// the current UTC month, offset by daysFromFirst from the 1st.
// The caller must ensure daysFromFirst keeps the result within the month
// (max 27 for February in non-leap years).
func currentMonthTS(daysFromFirst int) string {
	now := time.Now().UTC()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	ts := startOfMonth.Add(time.Duration(daysFromFirst) * 24 * time.Hour)
	return strconv.FormatInt(ts.Unix(), 10)
}

// pastMonthTS returns a Unix timestamp (as a string) for a point outside
// the current UTC month (two months prior to the 1st).
func pastMonthTS() string {
	now := time.Now().UTC()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	outOfMonth := startOfMonth.AddDate(0, -2, 0)
	return strconv.FormatInt(outOfMonth.Unix(), 10)
}

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

	week := currentMonthTS(0)
	var gotAuth string
	var gotPath string
	s, _ := newActivityTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		// Return a single week in the current month with 42 statuses.
		_, _ = w.Write([]byte(fmt.Sprintf(`[
			{"week":"%s","statuses":"42","logins":"10","registrations":"3"}
		]`, week)))
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

	// Dynamically compute three week timestamps within the current UTC month.
	w1 := currentMonthTS(0)  // 1st
	w2 := currentMonthTS(7)  // 8th
	w3 := currentMonthTS(14) // 15th

	s, _ := newActivityTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`[
			{"week":"%s","statuses":"100","logins":"20","registrations":"5"},
			{"week":"%s","statuses":"200","logins":"30","registrations":"7"},
			{"week":"%s","statuses":"50","logins":"15","registrations":"2"}
		]`, w1, w2, w3)))
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

	// One week from a past month (old), one in the current month.
	old := pastMonthTS()
	current := currentMonthTS(14)

	s, _ := newActivityTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`[
			{"week":"%s","statuses":"999","logins":"50","registrations":"10"},
			{"week":"%s","statuses":"75","logins":"12","registrations":"3"}
		]`, old, current)))
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
	// Only the current-month week should be counted: 75
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

	week := currentMonthTS(14)

	s, _ := newActivityTestServer(t, baseCostInstance("bob"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`[{"week":"%s","statuses":"10","logins":"1","registrations":"0"}]`, week)))
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

	// Activity entries exist but none for the current month.
	old := pastMonthTS()

	s, _ := newActivityTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// All weeks from a past month — none in the current month.
		_, _ = w.Write([]byte(fmt.Sprintf(`[
			{"week":"%s","statuses":"50","logins":"10","registrations":"2"}
		]`, old)))
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
