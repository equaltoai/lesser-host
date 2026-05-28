package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	testFleetOwner = "alice"
	testFleetKey   = "test-key"
)

// managedMetricsJSON returns a JSON response body for the managed Lesser metrics
// endpoint with daily rows spanning the last fleetMetricsWindowDays days.
// uniqueUsersPerDay controls how many unique users each daily row reports.
func managedMetricsJSON(now time.Time, uniqueUsersPerDay int64, requestsPerDay int64, costPerDay float64) string {
	type daily struct {
		Date           string  `json:"date"`
		TotalRequests  int64   `json:"total_requests"`
		UniqueUsers    int64   `json:"unique_users"`
		DynamoDBReads  int64   `json:"dynamodb_reads"`
		DynamoDBWrites int64   `json:"dynamodb_writes"`
		CostCents      int64   `json:"cost_cents"`
		CostDollars    float64 `json:"cost_dollars"`
		Currency       string  `json:"currency"`
	}
	rows := make([]daily, fleetMetricsWindowDays)
	for i := 0; i < fleetMetricsWindowDays; i++ {
		date := now.AddDate(0, 0, -(fleetMetricsWindowDays - 1 - i)).Format("2006-01-02")
		rows[i] = daily{
			Date:          date,
			TotalRequests: requestsPerDay,
			UniqueUsers:   uniqueUsersPerDay,
			CostDollars:   costPerDay,
			Currency:      "USD",
		}
	}
	b, _ := json.Marshal(map[string]any{
		"period": map[string]any{
			"start":    rows[0].Date,
			"end":      rows[len(rows)-1].Date,
			"days":     fleetMetricsWindowDays,
			"timezone": "UTC",
		},
		"daily": rows,
	})
	return string(b)
}

// TestFleetDataDTOBackwardCompatibility verifies that existing consumers
// (which decode instanceResponse without Fleet fields) continue to work.
// New fields are additive and omitempty — a decoder that ignores unknown
// fields (standard JSON behavior) is unaffected.
func TestFleetDataDTOBackwardCompatibility(t *testing.T) {
	t.Parallel()

	orig := instanceResponse{
		Slug:  "demo",
		Owner: testFleetOwner,
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Fields that must NOT appear because they are zero/omitted.
	for _, forbidden := range []string{
		"active_users_30d", "posts_24h", "sig_fails_24h",
		"spark_activity", "spark_cost", "peers", "severed",
	} {
		if _, ok := decoded[forbidden]; ok {
			t.Errorf("field %q must be omitted when zero (omitempty)", forbidden)
		}
	}

	// Fields that MUST appear (existing contract).
	for _, required := range []string{"slug", "owner"} {
		if _, ok := decoded[required]; !ok {
			t.Errorf("field %q must be present", required)
		}
	}
}

// TestFleetDataDTORedactionProof verifies that the Fleet-enriched
// instanceResponse never leaks internal storage fields, raw keys, secrets,
// or cross-tenant identifiers.
func TestFleetDataDTORedactionProof(t *testing.T) {
	t.Parallel()

	resp := instanceResponse{
		Slug:           "demo",
		Owner:          testFleetOwner,
		Status:         "active",
		SparkActivity:  []int64{1, 2, 3, 4, 5, 6, 7},
		SparkCost:      []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7},
		ActiveUsers30d: 42,
		Posts24h:       10,
		Peers:          3,
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(b)

	// Never leak internal storage fields.
	for _, forbidden := range []string{
		"PK", "SK", "TTL", "ttl",
		"account_id", "AccountID",
		"EntriesJSON", "entries_json", "entriesJson",
		"SecretString", "SecretBinary",
		"raw_key", "plaintext",
		"PAN", "CVV",
		"gsipk", "gsi1pk", "gsi1PK",
	} {
		if strings.Contains(strings.ToLower(raw), strings.ToLower(forbidden)) {
			t.Errorf("response must not contain %q: %s", forbidden, raw)
		}
	}

	// Never leak cross-tenant identifiers.
	if strings.Contains(raw, "OWNER#") {
		t.Errorf("response must not contain cross-tenant PK pattern: %s", raw)
	}

	// Fleet fields must be present when non-zero.
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["spark_activity"]; !ok {
		t.Error("spark_activity must be present when non-zero")
	}
	if _, ok := decoded["spark_cost"]; !ok {
		t.Error("spark_cost must be present when non-zero")
	}
	if v, ok := decoded["active_users_30d"].(float64); !ok || v != 42 {
		t.Errorf("active_users_30d must be 42, got %v", decoded["active_users_30d"])
	}
}

// TestFleetDataEmptyMetricsReturnsZeroValues verifies that when no Fleet
// data is available, the response returns zero/empty values (not HTTP 500).
func TestFleetDataEmptyMetricsReturnsZeroValues(t *testing.T) {
	t.Parallel()

	resp := instanceResponse{Slug: "demo", Owner: testFleetOwner}

	if resp.ActiveUsers30d != 0 {
		t.Error("ActiveUsers30d must be zero when no data")
	}
	if resp.Posts24h != 0 {
		t.Error("Posts24h must be zero when no data")
	}
	if resp.SigFails24h != 0 {
		t.Error("SigFails24h must be zero when no data")
	}
	if resp.SparkActivity != nil {
		t.Error("SparkActivity must be nil when no data")
	}
	if resp.SparkCost != nil {
		t.Error("SparkCost must be nil when no data")
	}
	if resp.Peers != 0 {
		t.Error("Peers must be zero when no data")
	}
	if resp.Severed != 0 {
		t.Error("Severed must be zero when no data")
	}
}

// TestFleetEnrichFromManagedMetricsPopulatesFields verifies that
// fleetEnrichFromManagedMetrics populates active_users_30d, spark_activity,
// and spark_cost from managed Lesser daily metrics.
func TestFleetEnrichFromManagedMetricsPopulatesFields(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(managedMetricsJSON(now, 42, 100, 0.25)))
	}))
	defer ts.Close()

	s := &Server{
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return testFleetKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
	}

	inst := &models.Instance{
		Slug:                           "demo",
		HostedBaseDomain:               "demo.greater.website",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:demo/instance-key",
	}
	resp := &instanceResponse{Slug: "demo"}

	s.fleetEnrichFromManagedMetrics(t.Context(), inst, resp)

	// Verify auth header was sent.
	if gotAuth != "Bearer test-key" {
		t.Errorf("expected Authorization: Bearer test-key, got %q", gotAuth)
	}

	// active_users_30d should be the max daily unique users (42).
	if resp.ActiveUsers30d != 42 {
		t.Errorf("ActiveUsers30d = %d, want 42", resp.ActiveUsers30d)
	}

	// spark_activity should have 7 entries, with the last day populated.
	if resp.SparkActivity == nil {
		t.Fatal("SparkActivity must not be nil")
	}
	if len(resp.SparkActivity) != fleetSparkDays {
		t.Fatalf("SparkActivity must have %d entries, got %d", fleetSparkDays, len(resp.SparkActivity))
	}
	// Every day in the 30-day window has requests, so all 7 sparkline days should be populated.
	// The 7-day window is a subset of the 30-day fetch. Verify each entry is 100.
	for i, v := range resp.SparkActivity {
		if v != 100 {
			t.Errorf("SparkActivity[%d] = %d, want 100", i, v)
		}
	}

	// spark_cost should have 7 entries.
	if resp.SparkCost == nil {
		t.Fatal("SparkCost must not be nil")
	}
	if len(resp.SparkCost) != fleetSparkDays {
		t.Fatalf("SparkCost must have %d entries, got %d", fleetSparkDays, len(resp.SparkCost))
	}
	for i, v := range resp.SparkCost {
		if v != 0.25 {
			t.Errorf("SparkCost[%d] = %f, want 0.25", i, v)
		}
	}
}

// TestFleetEnrichFromManagedMetricsMaxUsersSemantics verifies that
// active_users_30d uses max daily unique users (not sum), which avoids
// double-counting users active on multiple days.
func TestFleetEnrichFromManagedMetricsMaxUsersSemantics(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	// Only 2 days: one with 5 users, one with 15 users. Max should be 15.
	body := `{"period":{"start":"` + now.AddDate(0, 0, -1).Format("2006-01-02") + `","end":"` + now.Format("2006-01-02") + `","days":2,"timezone":"UTC"},"daily":[
		{"date":"` + now.AddDate(0, 0, -1).Format("2006-01-02") + `","total_requests":10,"unique_users":5,"cost_dollars":0.10,"currency":"USD"},
		{"date":"` + now.Format("2006-01-02") + `","total_requests":30,"unique_users":15,"cost_dollars":0.30,"currency":"USD"}
	]}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	s := &Server{
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return testFleetKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
	}

	inst := &models.Instance{
		Slug:                           "demo",
		HostedBaseDomain:               "demo.greater.website",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:demo/instance-key",
	}
	resp := &instanceResponse{Slug: "demo"}

	s.fleetEnrichFromManagedMetrics(t.Context(), inst, resp)

	// Max should be 15, not sum(5+15=20).
	if resp.ActiveUsers30d != 15 {
		t.Errorf("ActiveUsers30d = %d, want 15 (max, not sum)", resp.ActiveUsers30d)
	}
}

// TestFleetEnrichFromManagedMetricsCostCentsFallback verifies that CostCents
// is used as a fallback when CostDollars is zero.
func TestFleetEnrichFromManagedMetricsCostCentsFallback(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	// Today has CostCents=34 but CostDollars=0.
	body := `{"period":{"start":"` + today + `","end":"` + today + `","days":1,"timezone":"UTC"},"daily":[
		{"date":"` + today + `","total_requests":100,"unique_users":7,"cost_cents":34,"cost_dollars":0,"currency":"USD"}
	]}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	s := &Server{
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return testFleetKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
	}

	inst := &models.Instance{
		Slug:                           "demo",
		HostedBaseDomain:               "demo.greater.website",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:demo/instance-key",
	}
	resp := &instanceResponse{Slug: "demo"}

	s.fleetEnrichFromManagedMetrics(t.Context(), inst, resp)

	// Today is the last sparkline entry (newest). Cost should be 0.34 from cents.
	if resp.SparkCost == nil {
		t.Fatal("SparkCost must not be nil")
	}
	lastIdx := fleetSparkDays - 1
	if resp.SparkCost[lastIdx] != 0.34 {
		t.Errorf("SparkCost[%d] = %f, want 0.34 (34 cents converted to dollars)", lastIdx, resp.SparkCost[lastIdx])
	}
}

// TestFleetEnrichFromManagedMetricsKeyResolutionFailure verifies that Fleet
// fields stay zero when instance key resolution fails (no 500, no panic).
func TestFleetEnrichFromManagedMetricsKeyResolutionFailure(t *testing.T) {
	t.Parallel()

	s := &Server{
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return "", context.DeadlineExceeded
		},
	}

	inst := &models.Instance{Slug: "demo"}
	resp := &instanceResponse{Slug: "demo"}

	s.fleetEnrichFromManagedMetrics(t.Context(), inst, resp)

	if resp.ActiveUsers30d != 0 {
		t.Error("ActiveUsers30d must be zero on key resolution failure")
	}
	if resp.SparkActivity != nil {
		t.Error("SparkActivity must be nil on key resolution failure")
	}
	if resp.SparkCost != nil {
		t.Error("SparkCost must be nil on key resolution failure")
	}
}

// TestFleetEnrichFromManagedMetricsUpstreamFailure verifies that Fleet fields
// stay zero when the managed metrics HTTP endpoint returns non-2xx.
func TestFleetEnrichFromManagedMetricsUpstreamFailure(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	s := &Server{
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return testFleetKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
	}

	inst := &models.Instance{
		Slug:                           "demo",
		HostedBaseDomain:               "demo.greater.website",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:demo/instance-key",
	}
	resp := &instanceResponse{Slug: "demo"}

	s.fleetEnrichFromManagedMetrics(t.Context(), inst, resp)

	if resp.ActiveUsers30d != 0 {
		t.Error("ActiveUsers30d must be zero on upstream failure")
	}
	if resp.SparkActivity != nil {
		t.Error("SparkActivity must be nil on upstream failure")
	}
	if resp.SparkCost != nil {
		t.Error("SparkCost must be nil on upstream failure")
	}
}

// TestFleetEnrichFromManagedMetricsNilServer verifies safety when server,
// instance, or response is nil.
func TestFleetEnrichFromManagedMetricsNilServer(t *testing.T) {
	t.Parallel()

	// Nil server: must not panic.
	(*Server)(nil).fleetEnrichFromManagedMetrics(t.Context(), &models.Instance{Slug: "demo"}, &instanceResponse{Slug: "demo"})

	// Server with non-nil but nil instance: must not panic.
	s := &Server{}
	s.fleetEnrichFromManagedMetrics(t.Context(), nil, &instanceResponse{Slug: "demo"})

	// Nil response: must not panic.
	s.fleetEnrichFromManagedMetrics(t.Context(), &models.Instance{Slug: "demo"}, nil)

	// Empty slug: must not panic.
	s.fleetEnrichFromManagedMetrics(t.Context(), &models.Instance{Slug: "demo"}, &instanceResponse{})
}

// TestHandlePortalListInstancesFleetFieldsPresent verifies that the list
// endpoint returns Fleet fields populated from managed metrics.
func TestHandlePortalListInstancesFleetFieldsPresent(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(managedMetricsJSON(now, 42, 100, 0.25)))
	}))
	defer ts.Close()

	tdb := newPortalTestDB()
	tdb.qInstance.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:                           "a",
				Owner:                          testFleetOwner,
				Status:                         models.InstanceStatusActive,
				HostedBaseDomain:               "a.greater.website",
				LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:a/instance-key",
			},
		}
	}).Once()

	s := &Server{
		store:                store.New(tdb.db),
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return testFleetKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
	}

	ctx := &apptheory.Context{AuthIdentity: testFleetOwner}
	resp, err := s.handlePortalListInstances(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var parsed listInstancesResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Count != 1 {
		t.Fatalf("expected 1 instance, got %d", parsed.Count)
	}

	inst := parsed.Instances[0]

	// active_users_30d must be populated from managed metrics (42).
	if inst.ActiveUsers30d != 42 {
		t.Errorf("ActiveUsers30d = %d, want 42", inst.ActiveUsers30d)
	}

	// Spark activity must be populated.
	if inst.SparkActivity == nil {
		t.Error("SparkActivity must not be nil when managed metrics are available")
	} else if len(inst.SparkActivity) != fleetSparkDays {
		t.Errorf("SparkActivity must have %d entries, got %d", fleetSparkDays, len(inst.SparkActivity))
	}

	// Spark cost must be populated.
	if inst.SparkCost == nil {
		t.Error("SparkCost must not be nil when managed metrics are available")
	} else if len(inst.SparkCost) != fleetSparkDays {
		t.Errorf("SparkCost must have %d entries, got %d", fleetSparkDays, len(inst.SparkCost))
	}

	// Essential existing fields must be present.
	b, _ := json.Marshal(inst)
	raw := string(b)
	for _, required := range []string{`"slug":"a"`, `"owner":"alice"`} {
		if !strings.Contains(raw, required) {
			t.Errorf("existing field missing: %s in %s", required, raw)
		}
	}
}

// TestHandlePortalListInstancesMetricsFailureIsSilent verifies that when
// managed metrics are unavailable for one instance, the list endpoint still
// returns successfully with Fleet fields at zero for that instance.
func TestHandlePortalListInstancesMetricsFailureIsSilent(t *testing.T) {
	t.Parallel()

	// Return a 503 for the managed metrics endpoint.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	tdb := newPortalTestDB()
	tdb.qInstance.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:                           "a",
				Owner:                          testFleetOwner,
				Status:                         models.InstanceStatusActive,
				HostedBaseDomain:               "a.greater.website",
				LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:a/instance-key",
			},
		}
	}).Once()

	s := &Server{
		store:                store.New(tdb.db),
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return testFleetKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
	}

	ctx := &apptheory.Context{AuthIdentity: testFleetOwner}
	resp, err := s.handlePortalListInstances(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var parsed listInstancesResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Count != 1 {
		t.Fatalf("expected 1 instance, got %d", parsed.Count)
	}

	inst := parsed.Instances[0]

	// Fleet fields must be zero when metrics are unavailable.
	if inst.ActiveUsers30d != 0 {
		t.Errorf("ActiveUsers30d must be 0 on metrics failure, got %d", inst.ActiveUsers30d)
	}
	if inst.SparkActivity != nil {
		t.Error("SparkActivity must be nil on metrics failure")
	}
	if inst.SparkCost != nil {
		t.Error("SparkCost must be nil on metrics failure")
	}
}

// TestHandlePortalListInstancesCrossTenantIsolation verifies that instances
// from different owners are never exposed to a customer.
func TestHandlePortalListInstancesCrossTenantIsolation(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(managedMetricsJSON(now, 5, 10, 0.05)))
	}))
	defer ts.Close()

	tdb := newPortalTestDB()
	// GSI1 query scopes to owner — enrichment happens after this.
	tdb.qInstance.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:                           "a",
				Owner:                          testFleetOwner,
				Status:                         models.InstanceStatusActive,
				HostedBaseDomain:               "a.greater.website",
				LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:a/instance-key",
			},
			{
				Slug:                           "b",
				Owner:                          testFleetOwner,
				Status:                         models.InstanceStatusActive,
				HostedBaseDomain:               "b.greater.website",
				LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:b/instance-key",
			},
		}
	}).Once()

	s := &Server{
		store:                store.New(tdb.db),
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return testFleetKey, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
	}

	ctx := &apptheory.Context{AuthIdentity: testFleetOwner}
	resp, err := s.handlePortalListInstances(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var parsed listInstancesResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify all returned instances are owned by the customer.
	for _, inst := range parsed.Instances {
		if inst.Owner != testFleetOwner {
			t.Errorf("cross-tenant leak: instance %q has owner %q", inst.Slug, inst.Owner)
		}
	}

	// Response must not contain another owner's slug pattern.
	raw := string(resp.Body)
	if strings.Contains(raw, "OWNER#bob") {
		t.Error("response must not contain cross-tenant PK pattern")
	}
}

// TestHandlePortalListInstancesUnauthenticated verifies the list endpoint
// requires authentication.
func TestHandlePortalListInstancesUnauthenticated(t *testing.T) {
	t.Parallel()

	s := &Server{}
	ctx := &apptheory.Context{}
	if _, err := s.handlePortalListInstances(ctx); err == nil {
		t.Fatal("expected error for unauthenticated request")
	}
}

// TestHandlePortalListInstancesStoreNotInitialized verifies the list endpoint
// returns internal error when the store is not initialized.
func TestHandlePortalListInstancesStoreNotInitialized(t *testing.T) {
	t.Parallel()

	s := &Server{}
	ctx := &apptheory.Context{AuthIdentity: testFleetOwner}
	if _, err := s.handlePortalListInstances(ctx); err == nil {
		t.Fatal("expected internal error when store is nil")
	}

	t.Run("nil db", func(t *testing.T) {
		s := &Server{store: store.New(nil)}
		if _, err := s.handlePortalListInstances(ctx); err == nil {
			t.Fatal("expected internal error when db is nil")
		}
	})
}

// TestHandlePortalListInstancesQueryFailure verifies the list endpoint
// returns internal error when the DB query fails.
func TestHandlePortalListInstancesQueryFailure(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("All", mock.Anything).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: testFleetOwner}
	if _, err := s.handlePortalListInstances(ctx); err == nil {
		t.Fatal("expected internal error on query failure")
	}
}
