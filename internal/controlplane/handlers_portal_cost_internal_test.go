package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/costtelemetry"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	testCostSlug1 = "demo"
	testCostDate1 = "2026-05-25"
	testCostDate2 = "2026-05-26"
	testRawKey    = "lhk_test_secret"
)

func newCostTestDB() (*ttmocks.MockExtendedDB, *ttmocks.MockQuery) {
	db, qs := newTestDBWithModelQueries("*models.Instance")
	return db, qs[0]
}

func stubCostInstanceFirst(t *testing.T, q *ttmocks.MockQuery, inst models.Instance) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = inst
	}).Maybe()
}

func newCostHandlerCtx(slug string, query map[string][]string) *apptheory.Context {
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": slug},
	}
	if query != nil {
		ctx.Request.Query = query
	}
	return ctx
}

func baseCostInstance(owner string) models.Instance {
	return models.Instance{
		Slug:                           testCostSlug1,
		Owner:                          owner,
		HostedAccountID:                "123456789012",
		HostedRegion:                   "us-east-1",
		HostedBaseDomain:               "simulacrum.greater.website",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:demo/instance-key",
	}
}

func newCostTestServer(t *testing.T, inst models.Instance, handler http.HandlerFunc) (*Server, *httptest.Server) {
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

func TestHandlePortalGetInstanceCost_SendsBearerAuthAndMapsDailyRows(t *testing.T) {
	t.Parallel()

	var gotAuth string
	var gotPath string
	var gotFrom string
	var gotTo string
	s, _ := newCostTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotFrom = r.URL.Query().Get("from")
		gotTo = r.URL.Query().Get("to")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"period":{"start":"2026-05-25","end":"2026-05-26","days":2,"timezone":"UTC"},
			"daily":[
				{"date":"2026-05-25","total_requests":100,"unique_users":4,"dynamodb_reads":50,"dynamodb_writes":10,"lambda_duration_ms":250,"cost_cents":12,"cost_dollars":0.12,"currency":"USD"},
				{"date":"2026-05-26","total_requests":200,"unique_users":7,"dynamodb_reads":70,"dynamodb_writes":20,"lambda_duration_ms":450,"cost_cents":34,"cost_dollars":0.34,"currency":"USD"}
			]
		}`))
	})

	ctx := newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate2},
	})
	resp, err := s.handlePortalGetInstanceCost(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)
	require.Equal(t, "Bearer "+testRawKey, gotAuth)
	require.Equal(t, "/api/v1/instance/metrics/daily", gotPath)
	require.Equal(t, testCostDate1, gotFrom)
	require.Equal(t, testCostDate2, gotTo)

	var body portalCostResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, testCostSlug1, body.InstanceSlug)
	require.Equal(t, testCostDate1, body.FromDate)
	require.Equal(t, testCostDate2, body.ToDate)
	require.Equal(t, 2, body.Count)
	require.InDelta(t, 0.46, body.TotalCost, 0.000001)
	require.Equal(t, "USD", body.Currency)
	require.Len(t, body.Days, 2)
	require.Equal(t, testCostDate1, body.Days[0].Date)
	require.InDelta(t, 0.12, body.Days[0].DayCost, 0.000001)
	require.Len(t, body.Days[0].Entries, 1)
	require.Equal(t, "Managed Lesser", body.Days[0].Entries[0].Service)
	require.InDelta(t, 0.12, body.Days[0].Entries[0].Cost, 0.000001)
	require.NotEmpty(t, body.Days[0].Entries[0].Metrics)
	require.NotContains(t, string(resp.Body), testRawKey)
	for _, forbidden := range []string{`"account_id"`, `"pk"`, `"PK"`, `"sk"`, `"SK"`, `"ttl"`, `"entries_json"`, `"EntriesJSON"`, `"instance_key"`, `"raw_key"`} {
		require.NotContains(t, string(resp.Body), forbidden)
	}
}

func TestPortalCostResponseJSONOmitsCostTelemetrySensitiveFields(t *testing.T) {
	t.Parallel()

	body := portalCostResponse{
		InstanceSlug: testCostSlug1,
		FromDate:     testCostDate1,
		ToDate:       testCostDate2,
		Days: []portalCostDayEntry{
			{
				Date:     testCostDate1,
				DayCost:  0.12,
				Currency: "USD",
				Entries: []costtelemetry.ReconciledCostEntry{
					{
						Date:     testCostDate1,
						Service:  "Managed Lesser",
						Cost:     0.12,
						Currency: "USD",
						Metrics: []costtelemetry.ServiceAttribution{
							{Service: "Lambda", MetricName: "Invocations", Stat: "Sum", Unit: "Count", Value: 42},
						},
					},
				},
			},
		},
		Count:     1,
		TotalCost: 0.12,
		Currency:  "USD",
	}

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	payload := string(raw)
	require.Contains(t, payload, `"entries"`)
	require.Contains(t, payload, `"metrics"`)
	for _, forbidden := range []string{`"account_id"`, `"pk"`, `"PK"`, `"sk"`, `"SK"`, `"ttl"`, `"entries_json"`, `"EntriesJSON"`, `"instance_key"`, `"raw_key"`} {
		require.NotContains(t, payload, forbidden)
	}
}

func TestHandlePortalGetInstanceCost_EmptyLesserData(t *testing.T) {
	t.Parallel()

	s, _ := newCostTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+testRawKey, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"period":{"start":"2026-05-25","end":"2026-05-25","days":1,"timezone":"UTC"},"daily":[]}`))
	})

	resp, err := s.handlePortalGetInstanceCost(newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate1},
	}))
	require.NoError(t, err)
	var body portalCostResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 0, body.Count)
	require.Empty(t, body.Days)
	require.Equal(t, 0.0, body.TotalCost)
}

func TestHandlePortalGetInstanceCost_DateRangeTooWideRejectedBeforeUpstream(t *testing.T) {
	t.Parallel()

	tdb, qInst := newCostTestDB()
	stubCostInstanceFirst(t, qInst, baseCostInstance("alice"))

	secretReads := 0
	upstreamCalls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	s := &Server{
		cfg:                  config.Config{Stage: "lab"},
		store:                store.New(tdb),
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			secretReads++
			return testRawKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
	}

	_, err := s.handlePortalGetInstanceCost(newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {"2025-01-01"},
		"to":   {"2026-01-03"},
	}))
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.bad_request", appErr.Code)
	require.Zero(t, secretReads)
	require.Zero(t, upstreamCalls)
}

func TestHandlePortalGetInstanceCost_MaxDateRangeSucceeds(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotFrom string
	var gotTo string
	s, _ := newCostTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotFrom = r.URL.Query().Get("from")
		gotTo = r.URL.Query().Get("to")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"period":{"start":"2025-01-01","end":"2026-01-02","days":366,"timezone":"UTC"},"daily":[]}`))
	})

	resp, err := s.handlePortalGetInstanceCost(newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {"2025-01-01"},
		"to":   {"2026-01-02"},
	}))
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)
	require.Equal(t, "/api/v1/instance/metrics/daily", gotPath)
	require.Equal(t, "2025-01-01", gotFrom)
	require.Equal(t, "2026-01-02", gotTo)
}

func TestHandlePortalGetInstanceCost_WrongOwnerForbiddenBeforeSecretOrHTTP(t *testing.T) {
	t.Parallel()

	tdb, qInst := newCostTestDB()
	stubCostInstanceFirst(t, qInst, baseCostInstance("bob"))

	secretReads := 0
	httpCalls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	s := &Server{
		cfg:                  config.Config{Stage: "lab"},
		store:                store.New(tdb),
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			secretReads++
			return testRawKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) { return ts.URL, nil },
	}

	_, err := s.handlePortalGetInstanceCost(newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate1},
	}))
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, appErrCodeForbidden, appErr.Code)
	require.Zero(t, secretReads)
	require.Zero(t, httpCalls)
}

func TestHandlePortalGetInstanceCost_Upstream5xxDoesNotLeakKeyOrBody(t *testing.T) {
	t.Parallel()

	s, _ := newCostTestServer(t, baseCostInstance("alice"), func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+testRawKey, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream body contains " + testRawKey))
	})

	_, err := s.handlePortalGetInstanceCost(newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate1},
	}))
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.upstream_error", appErr.Code)
	require.NotContains(t, appErr.Message, testRawKey)
	require.NotContains(t, appErr.Message, "upstream body")
}

func TestHandlePortalGetInstanceCost_UpstreamUnavailable(t *testing.T) {
	t.Parallel()

	tdb, qInst := newCostTestDB()
	stubCostInstanceFirst(t, qInst, baseCostInstance("alice"))

	s := &Server{
		cfg:                  config.Config{Stage: "lab"},
		store:                store.New(tdb),
		portalCostHTTPClient: http.DefaultClient,
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return testRawKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return "http://127.0.0.1:1", nil
		},
	}

	_, err := s.handlePortalGetInstanceCost(newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate1},
	}))
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.upstream_unavailable", appErr.Code)
	require.NotContains(t, appErr.Message, testRawKey)
}

func TestHandlePortalGetInstanceCost_KeyResolverFailureDoesNotLeak(t *testing.T) {
	t.Parallel()

	tdb, qInst := newCostTestDB()
	stubCostInstanceFirst(t, qInst, baseCostInstance("alice"))

	s := &Server{
		cfg:   config.Config{Stage: "lab"},
		store: store.New(tdb),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return "", errors.New("secret failure includes " + testRawKey)
		},
	}

	_, err := s.handlePortalGetInstanceCost(newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate1},
	}))
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.internal", appErr.Code)
	require.NotContains(t, appErr.Message, testRawKey)
}

func TestHandlePortalGetInstanceCost_DefaultManagedMetricsURL(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{Stage: "lab"}}
	baseURL, err := s.defaultInstanceMetricsBaseURL(&models.Instance{HostedBaseDomain: "example.greater.website"})
	require.NoError(t, err)
	require.Equal(t, "https://api.dev.example.greater.website", baseURL)
}

func TestHandlePortalGetInstanceCost_InvalidDates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from string
		to   string
		code string
	}{
		{"from only", "2026-05-01", "", "app.bad_request"},
		{"to only", "", "2026-05-26", "app.bad_request"},
		{"bad from", "not-a-date", "2026-05-26", "app.bad_request"},
		{"bad to", "2026-05-01", "nope", "app.bad_request"},
		{"from after to", "2026-05-26", "2026-05-01", "app.bad_request"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tdb, qInst := newCostTestDB()
			s := &Server{store: store.New(tdb)}
			stubCostInstanceFirst(t, qInst, baseCostInstance("alice"))

			_, err := s.handlePortalGetInstanceCost(newCostHandlerCtx(testCostSlug1, map[string][]string{
				"from": {tt.from},
				"to":   {tt.to},
			}))
			require.Error(t, err)
			appErr, ok := err.(*apptheory.AppTheoryError)
			require.True(t, ok)
			require.Equal(t, tt.code, appErr.Code)
		})
	}
}

func TestHandlePortalGetInstanceCost_InstanceNotFound(t *testing.T) {
	t.Parallel()

	tdb, qInst := newCostTestDB()
	s := &Server{store: store.New(tdb)}
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()

	_, err := s.handlePortalGetInstanceCost(newCostHandlerCtx(testCostSlug1, map[string][]string{
		"from": {testCostDate1},
		"to":   {testCostDate1},
	}))
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, soulMintAppErrCodeNotFound, appErr.Code)
}

func TestBuildManagedInstanceMetricsURL(t *testing.T) {
	t.Parallel()

	got, err := buildManagedInstanceMetricsURL("https://api.dev.example.greater.website/", testCostDate1, testCostDate2)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got, "https://api.dev.example.greater.website/api/v1/instance/metrics/daily?"))
	require.Contains(t, got, "from=2026-05-25")
	require.Contains(t, got, "to=2026-05-26")
}
