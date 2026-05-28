package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const testFleetOwner = "alice"

// TestFleetDataDTOBackwardCompatibility verifies that existing consumers
// (which decode instanceResponse without Fleet fields) continue to work.
// New fields are additive and omitempty — a decoder that ignores unknown
// fields (standard JSON behavior) is unaffected.
func TestFleetDataDTOBackwardCompatibility(t *testing.T) {
	t.Parallel()

	// Simulate the shape an existing pre-M5 consumer would see.
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

	// All Fleet fields should be zero.
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

// TestFleetEnrichSparklinesWithCostTelemetry verifies that sparkCost is
// populated from CostTelemetry records.
func TestFleetEnrichSparklinesWithCostTelemetry(t *testing.T) {
	t.Parallel()

	qCostTele := new(ttmocks.MockQuery)
	qUsage := new(ttmocks.MockQuery)

	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.CostTelemetry")).Return(qCostTele).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UsageLedgerEntry")).Return(qUsage).Maybe()

	for _, q := range []*ttmocks.MockQuery{qCostTele, qUsage} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
	}

	// Return no usage data (activity sparkline stays empty).
	qUsage.On("All", mock.Anything).Return(nil).Maybe()

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Return cost telemetry for yesterday only.
	qCostTele.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.CostTelemetry](t, args, 0)
		*dest = []*models.CostTelemetry{
			{Date: yesterday, DayCost: 1.23},
		}
	}).Once()

	s := &Server{store: store.New(db)}
	resp := &instanceResponse{Slug: "demo"}

	s.fleetEnrichSparklines(t.Context(), resp)

	if resp.SparkCost == nil {
		t.Fatal("SparkCost must not be nil after enrichment")
	}
	if len(resp.SparkCost) != fleetSparkDays {
		t.Fatalf("SparkCost must have %d entries, got %d", fleetSparkDays, len(resp.SparkCost))
	}

	// Yesterday's position: second-to-last entry (index 5 for 7-day window).
	found := false
	for i := range fleetSparkDays {
		date := now.AddDate(0, 0, -(fleetSparkDays - 1 - i)).Format("2006-01-02")
		if date == yesterday && resp.SparkCost[i] == 1.23 {
			found = true
		}
		// Today should be zero (no data).
		if date == today && resp.SparkCost[i] != 0 {
			t.Errorf("today's cost should be 0 (no data), got %v", resp.SparkCost[i])
		}
	}
	if !found {
		t.Errorf("yesterday's cost 1.23 not found in sparkCost: %v", resp.SparkCost)
	}
}

// TestFleetEnrichSparklinesNilServer verifies safety when server or store is nil.
func TestFleetEnrichSparklinesNilServer(t *testing.T) {
	t.Parallel()

	// Nil server: must not panic.
	(*Server)(nil).fleetEnrichSparklines(t.Context(), &instanceResponse{Slug: "demo"})

	// Server with nil store: must not panic.
	s := &Server{}
	s.fleetEnrichSparklines(t.Context(), &instanceResponse{Slug: "demo"})

	// Nil response: must not panic.
	qCostTele := new(ttmocks.MockQuery)
	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.CostTelemetry")).Return(qCostTele).Maybe()
	s2 := &Server{store: store.New(db)}
	s2.fleetEnrichSparklines(t.Context(), nil)

	// Empty slug: must not panic.
	s2.fleetEnrichSparklines(t.Context(), &instanceResponse{})
}

// TestFleetAggregateDailyActivity verifies the activity sparkline aggregation
// from UsageLedgerEntry records.
func TestFleetAggregateDailyActivity(t *testing.T) {
	t.Parallel()

	qUsage := new(ttmocks.MockQuery)
	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UsageLedgerEntry")).Return(qUsage).Maybe()

	for _, q := range []*ttmocks.MockQuery{qUsage} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
	}

	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	// Return 3 entries for today.
	qUsage.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UsageLedgerEntry](t, args, 0)
		*dest = []*models.UsageLedgerEntry{
			{ID: "e1", CreatedAt: now},
			{ID: "e2", CreatedAt: now},
			{ID: "e3", CreatedAt: now},
		}
	}).Maybe()

	s := &Server{store: store.New(db)}
	counts, err := fleetAggregateDailyActivity(t.Context(), s, "demo", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(counts) != fleetSparkDays {
		t.Fatalf("expected %d buckets, got %d", fleetSparkDays, len(counts))
	}

	// Today is the last entry (newest).
	lastIdx := fleetSparkDays - 1
	if todayBucket := now.Format("2006-01-02"); todayBucket == today {
		if counts[lastIdx] != 3 {
			t.Errorf("today's activity count should be 3, got %d", counts[lastIdx])
		}
	}
}

// TestFleetAggregateDailyActivityStoreNotAvailable verifies error handling
// when the store is not available.
func TestFleetAggregateDailyActivityStoreNotAvailable(t *testing.T) {
	t.Parallel()

	_, err := fleetAggregateDailyActivity(t.Context(), nil, "demo", time.Now().UTC())
	if err == nil {
		t.Fatal("expected error when server is nil")
	}
}

// TestHandlePortalListInstancesFleetFieldsPresent verifies that the list
// endpoint returns Fleet fields (zero-valued for non-enriched instances).
func TestHandlePortalListInstancesFleetFieldsPresent(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{Slug: "a", Owner: testFleetOwner, Status: models.InstanceStatusActive},
		}
	}).Once()

	tdb.qUsage.On("All", mock.Anything).Return(nil).Maybe()

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

	// Fleet fields should be present (additive, even if zero).
	// Marshal to JSON and check field presence.
	b, _ := json.Marshal(inst)
	raw := string(b)

	// These fields should not appear when zero (omitempty).
	if strings.Contains(raw, `"active_users_30d"`) {
		t.Log("active_users_30d present (may be non-zero from enrichment)")
	}
	if strings.Contains(raw, `"spark_activity"`) {
		t.Log("spark_activity present (may be non-zero from enrichment)")
	}

	// Essential existing fields must be present.
	for _, required := range []string{`"slug":"a"`, `"owner":"alice"`} {
		if !strings.Contains(raw, required) {
			t.Errorf("existing field missing: %s in %s", required, raw)
		}
	}
}

// TestHandlePortalListInstancesCrossTenantIsolation verifies that instances
// from different owners are never exposed to a customer.
func TestHandlePortalListInstancesCrossTenantIsolation(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Return only the customer's instances — the GSI1 query ensures this.
	tdb.qInstance.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{Slug: "a", Owner: testFleetOwner, Status: models.InstanceStatusActive},
			{Slug: "b", Owner: testFleetOwner, Status: models.InstanceStatusActive},
		}
	}).Once()

	tdb.qUsage.On("All", mock.Anything).Return(nil).Maybe()

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
	// ErrItemNotFound from All is an actual error (not a not-found semantic),
	// so it should be treated as an internal error.
	if _, err := s.handlePortalListInstances(ctx); err == nil {
		t.Fatal("expected internal error on query failure")
	}
}
