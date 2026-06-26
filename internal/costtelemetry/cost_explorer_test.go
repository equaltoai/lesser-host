package costtelemetry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

// ---------------------------------------------------------------------------
// test constants for Cost Explorer tests
// ---------------------------------------------------------------------------

const (
	testCEServiceLambda   = "AWS Lambda"
	testCEServiceDynamoDB = "Amazon DynamoDB"
	testCEServiceAPIGW    = "Amazon API Gateway"
	testCEServiceS3       = "Amazon Simple Storage Service"
	testCEServiceSQS      = "Amazon Simple Queue Service"
	testCEServiceCloudF   = "Amazon CloudFront"
	testCECurrencyUSD     = "USD"
	testCEDate1           = "2026-05-25"
	testCEDate2           = "2026-05-26"
	testCENormLambda      = "Lambda"
	testCENormDynamoDB    = "DynamoDB"
	testCENormAPIGW       = "APIGateway"
)

// ---------------------------------------------------------------------------
// fake Cost Explorer implementations for tests
// ---------------------------------------------------------------------------

// fakeCostExplorerAPI implements costExplorerAPI for tests.
type fakeCostExplorerAPI struct {
	results   []cetypes.ResultByTime
	lastInput *costexplorer.GetCostAndUsageInput
	err       error
}

func (f *fakeCostExplorerAPI) GetCostAndUsage(_ context.Context, params *costexplorer.GetCostAndUsageInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error) {
	f.lastInput = params
	if f.err != nil {
		return nil, f.err
	}
	return &costexplorer.GetCostAndUsageOutput{
		ResultsByTime: f.results,
	}, nil
}

// fakeCostExplorerClientFactory implements CostExplorerClientFactory for tests.
type fakeCostExplorerClientFactory struct {
	api costExplorerAPI
}

func (f *fakeCostExplorerClientFactory) Client(_ context.Context, _, _ string) (costExplorerAPI, error) {
	if f.api == nil {
		return nil, fmt.Errorf("no Cost Explorer client configured for test")
	}
	return f.api, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func makeCECollector(api costExplorerAPI) CostExplorerCollector {
	factory := &fakeCostExplorerClientFactory{api: api}
	return NewCostExplorerCollector(factory)
}

// ceServiceGroup creates a single Group with the given service name and cost.
func ceServiceGroup(service string, amount string) cetypes.Group {
	return cetypes.Group{
		Keys: []string{service},
		Metrics: map[string]cetypes.MetricValue{
			costBreakdownMetric: {
				Amount: aws.String(amount),
				Unit:   aws.String(testCECurrencyUSD),
			},
		},
	}
}

// ceResultByTime creates a ResultByTime for the given date with the provided groups.
func ceResultByTime(startDate string, groups ...cetypes.Group) cetypes.ResultByTime {
	return cetypes.ResultByTime{
		TimePeriod: &cetypes.DateInterval{
			Start: aws.String(startDate),
			End:   aws.String(nextDate(startDate)),
		},
		Groups: groups,
	}
}

// nextDate returns the next day as YYYY-MM-DD given a YYYY-MM-DD input.
func nextDate(date string) string {
	t, _ := time.Parse("2006-01-02", date)
	return t.Add(24 * time.Hour).Format("2006-01-02")
}

// cwReportWithAttributions creates an InstanceCostReport for a given slug
// with the specified attributions.
func cwReportWithAttributions(slug, accountID string, attributions ...ServiceAttribution) *InstanceCostReport {
	start := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	end := start.Add(48 * time.Hour)
	return &InstanceCostReport{
		Slug:         slug,
		AccountID:    accountID,
		WindowStart:  start,
		WindowEnd:    end,
		Attributions: attributions,
	}
}

// costKeys returns sorted keys for assertion diagnostics.
func costKeys(costs []CostBreakdown) string {
	keys := make([]string, len(costs))
	for i, c := range costs {
		keys[i] = c.Service
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// entryKeys returns sorted service keys from reconciled entries for diagnostics.
func entryKeys(entries []ReconciledCostEntry) string {
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.Service
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// findCostBreakdown locates a CostBreakdown by service name.
func findCostBreakdown(costs []CostBreakdown, service string) (CostBreakdown, bool) {
	for _, c := range costs {
		if c.Service == service {
			return c, true
		}
	}
	return CostBreakdown{}, false
}

// ---------------------------------------------------------------------------
// validation tests
// ---------------------------------------------------------------------------

func TestCECollectCosts_BlankSlug(t *testing.T) {
	t.Parallel()
	cc := makeCECollector(&fakeCostExplorerAPI{})
	scope := validScope()
	scope.Slug = ""
	start, end := validWindow()

	_, err := cc.CollectCosts(context.Background(), scope, start, end)
	if err == nil {
		t.Fatal("expected error for blank slug")
	}
	if !strings.Contains(err.Error(), "Slug") {
		t.Fatalf("error should mention Slug: %v", err)
	}
}

func TestCECollectCosts_BlankAccountID(t *testing.T) {
	t.Parallel()
	cc := makeCECollector(&fakeCostExplorerAPI{})
	scope := validScope()
	scope.AccountID = ""
	start, end := validWindow()

	_, err := cc.CollectCosts(context.Background(), scope, start, end)
	if err == nil {
		t.Fatal("expected error for blank AccountID")
	}
	if !strings.Contains(err.Error(), "AccountID") {
		t.Fatalf("error should mention AccountID: %v", err)
	}
}

func TestCECollectCosts_BlankRegion(t *testing.T) {
	t.Parallel()
	cc := makeCECollector(&fakeCostExplorerAPI{})
	scope := validScope()
	scope.Region = ""
	start, end := validWindow()

	_, err := cc.CollectCosts(context.Background(), scope, start, end)
	if err == nil {
		t.Fatal("expected error for blank Region")
	}
	if !strings.Contains(err.Error(), "Region") {
		t.Fatalf("error should mention Region: %v", err)
	}
}

func TestCECollectCosts_InvertedWindow(t *testing.T) {
	t.Parallel()
	cc := makeCECollector(&fakeCostExplorerAPI{})
	start := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)

	_, err := cc.CollectCosts(context.Background(), validScope(), start, end)
	if err == nil {
		t.Fatal("expected error for inverted window")
	}
	if !strings.Contains(err.Error(), "windowStart must be before windowEnd") {
		t.Fatalf("error should mention window ordering: %v", err)
	}
}

func TestCECollectCosts_EqualWindow(t *testing.T) {
	t.Parallel()
	cc := makeCECollector(&fakeCostExplorerAPI{})
	ts := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	_, err := cc.CollectCosts(context.Background(), validScope(), ts, ts)
	if err == nil {
		t.Fatal("expected error for equal start/end")
	}
}

func TestCECollectCosts_FactoryError(t *testing.T) {
	t.Parallel()
	collector := NewCostExplorerCollector(&fakeCostExplorerClientFactory{api: nil})
	start, end := validWindow()

	_, err := collector.CollectCosts(context.Background(), validScope(), start, end)
	if err == nil {
		t.Fatal("expected error from factory")
	}
}

// ---------------------------------------------------------------------------
// CollectCosts happy-path tests
// ---------------------------------------------------------------------------

func TestCECollectCosts_HappyPath(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	api := &fakeCostExplorerAPI{
		results: []cetypes.ResultByTime{
			ceResultByTime(testCEDate1,
				ceServiceGroup(testCEServiceLambda, "0.0532947123"),
				ceServiceGroup(testCEServiceDynamoDB, "0.0042189000"),
			),
		},
	}

	cc := makeCECollector(api)
	result, err := cc.CollectCosts(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Slug != testSlug {
		t.Fatalf("expected slug 'demo', got %q", result.Slug)
	}
	if result.AccountID != testAccountID {
		t.Fatalf("expected AccountID %q, got %q", testAccountID, result.AccountID)
	}
	if len(result.Costs) != 2 {
		t.Fatalf("expected 2 costs, got %d: %s", len(result.Costs), costKeys(result.Costs))
	}

	// Verify each entry carries the daily date from TimePeriod.Start.
	for _, c := range result.Costs {
		if c.Date != testCEDate1 {
			t.Fatalf("expected date %q, got %q for service %q", testCEDate1, c.Date, c.Service)
		}
	}

	// Verify rounding: 0.0532947123 → 0.053295 after rounding to 1e6.
	lambdaCost, ok := findCostBreakdown(result.Costs, testCEServiceLambda)
	if !ok {
		t.Fatalf("missing cost for %s", testCEServiceLambda)
	}
	expected := 0.053295
	if lambdaCost.Amount != expected {
		t.Fatalf("Lambda cost = %v, want %v (rounded to 6 decimal places)", lambdaCost.Amount, expected)
	}
	if lambdaCost.Currency != testCECurrencyUSD {
		t.Fatalf("currency = %q, want USD", lambdaCost.Currency)
	}

	dynamoCost, ok := findCostBreakdown(result.Costs, testCEServiceDynamoDB)
	if !ok {
		t.Fatalf("missing cost for %s", testCEServiceDynamoDB)
	}
	expected = 0.004219
	if dynamoCost.Amount != expected {
		t.Fatalf("DynamoDB cost = %v, want %v", dynamoCost.Amount, expected)
	}

	// Total should be sum of both.
	expectedTotal := 0.057514 // 0.053295 + 0.004219
	if result.TotalCost != expectedTotal {
		t.Fatalf("TotalCost = %v, want %v", result.TotalCost, expectedTotal)
	}
	if result.Currency != testCECurrencyUSD {
		t.Fatalf("result currency = %q, want USD", result.Currency)
	}
}

func TestCECollectCosts_QueryInputFiltersLinkedAccount(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	api := &fakeCostExplorerAPI{
		results: []cetypes.ResultByTime{
			ceResultByTime(testCEDate1, ceServiceGroup(testCEServiceLambda, "0.05")),
		},
	}

	cc := makeCECollector(api)
	if _, err := cc.CollectCosts(context.Background(), validScope(), start, end); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if api.lastInput == nil {
		t.Fatal("expected GetCostAndUsage input to be captured")
	}
	if api.lastInput.Filter == nil {
		t.Fatal("expected Cost Explorer input to include tenant filter")
	}
	if api.lastInput.Filter.Dimensions == nil {
		t.Fatalf("expected Cost Explorer filter to use dimensions, got %#v", api.lastInput.Filter)
	}
	if api.lastInput.Filter.Dimensions.Key != cetypes.DimensionLinkedAccount {
		t.Fatalf("filter dimension key = %q, want %q",
			api.lastInput.Filter.Dimensions.Key, cetypes.DimensionLinkedAccount)
	}
	values := api.lastInput.Filter.Dimensions.Values
	if len(values) != 1 || values[0] != testAccountID {
		t.Fatalf("filter values = %#v, want [%q]", values, testAccountID)
	}
}

func TestBuildCostExplorerResultRejectsUnscopedResults(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	_, err := buildCostExplorerResult(validScope(), start, end, tenantScopedCostExplorerResults{
		results: []cetypes.ResultByTime{
			ceResultByTime(testCEDate1, ceServiceGroup(testCEServiceLambda, "0.05")),
		},
	})
	if err == nil {
		t.Fatal("expected unscoped Cost Explorer results to be rejected")
	}
	if !strings.Contains(err.Error(), "unscoped Cost Explorer results") {
		t.Fatalf("error should mention unscoped results: %v", err)
	}
}

func TestBuildCostExplorerResultRejectsAccountScopeMismatch(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	_, err := buildCostExplorerResult(validScope(), start, end, tenantScopedCostExplorerResults{
		accountID: "999999999999",
		results: []cetypes.ResultByTime{
			ceResultByTime(testCEDate1, ceServiceGroup(testCEServiceLambda, "0.05")),
		},
	})
	if err == nil {
		t.Fatal("expected mismatched Cost Explorer account scope to be rejected")
	}
	if !strings.Contains(err.Error(), "account scope mismatch") {
		t.Fatalf("error should mention account scope mismatch: %v", err)
	}
}

func TestCECollectCosts_MultipleDays(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	api := &fakeCostExplorerAPI{
		results: []cetypes.ResultByTime{
			ceResultByTime(testCEDate1,
				ceServiceGroup(testCEServiceLambda, "0.05"),
			),
			ceResultByTime(testCEDate2,
				ceServiceGroup(testCEServiceLambda, "0.03"),
			),
		},
	}

	cc := makeCECollector(api)
	result, err := cc.CollectCosts(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// M3.9: same service across two days must NOT collapse into one entry.
	// Each day must produce its own CostBreakdown with the correct date
	// and per-day amount.
	if len(result.Costs) != 2 {
		t.Fatalf("expected 2 cost entries (one per day), got %d: %s",
			len(result.Costs), costKeys(result.Costs))
	}

	// Find entries by date.
	var day1Cost, day2Cost float64
	var day1Found, day2Found bool
	for _, c := range result.Costs {
		if c.Service != testCEServiceLambda {
			t.Fatalf("unexpected service %q", c.Service)
		}
		switch c.Date {
		case testCEDate1:
			day1Cost = c.Amount
			day1Found = true
		case testCEDate2:
			day2Cost = c.Amount
			day2Found = true
		default:
			t.Fatalf("unexpected date %q in cost entry", c.Date)
		}
	}
	if !day1Found {
		t.Fatal("missing cost entry for date 2026-05-25")
	}
	if !day2Found {
		t.Fatal("missing cost entry for date 2026-05-26")
	}
	if day1Cost != 0.05 {
		t.Fatalf("2026-05-25 Lambda cost = %v, want 0.05", day1Cost)
	}
	if day2Cost != 0.03 {
		t.Fatalf("2026-05-26 Lambda cost = %v, want 0.03", day2Cost)
	}
	if result.TotalCost != 0.08 {
		t.Fatalf("TotalCost = %v, want 0.08", result.TotalCost)
	}
}

func TestCECollectCosts_MultipleServices(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	api := &fakeCostExplorerAPI{
		results: []cetypes.ResultByTime{
			ceResultByTime(testCEDate1,
				ceServiceGroup(testCEServiceLambda, "1.00"),
				ceServiceGroup(testCEServiceDynamoDB, "0.50"),
				ceServiceGroup(testCEServiceAPIGW, "0.25"),
				ceServiceGroup(testCEServiceS3, "0.10"),
				ceServiceGroup(testCEServiceSQS, "0.05"),
				ceServiceGroup(testCEServiceCloudF, "0.15"),
			),
		},
	}

	cc := makeCECollector(api)
	result, err := cc.CollectCosts(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Costs) != 6 {
		t.Fatalf("expected 6 costs, got %d: %s", len(result.Costs), costKeys(result.Costs))
	}
	// Total should be 1.00 + 0.50 + 0.25 + 0.10 + 0.05 + 0.15 = 2.05
	if result.TotalCost != 2.05 {
		t.Fatalf("TotalCost = %v, want 2.05", result.TotalCost)
	}

	// M3.9: every entry must carry the date from TimePeriod.Start.
	for _, c := range result.Costs {
		if c.Date != testCEDate1 {
			t.Fatalf("expected date %q, got %q for service %q", testCEDate1, c.Date, c.Service)
		}
	}

	// Verify deterministic sort order (date, then lexicographic by CE
	// service name). With a single date the primary key is the same for
	// all entries, so the secondary service-name sort dominates.
	// "AWS" < "Amazon" because 'W' (87) < 'm' (109) in ASCII.
	expectedOrder := []string{
		testCEServiceLambda,   // "AWS Lambda"
		testCEServiceAPIGW,    // "Amazon API Gateway"
		testCEServiceCloudF,   // "Amazon CloudFront"
		testCEServiceDynamoDB, // "Amazon DynamoDB"
		testCEServiceSQS,      // "Amazon Simple Queue Service"
		testCEServiceS3,       // "Amazon Simple Storage Service"
	}
	for i, exp := range expectedOrder {
		if result.Costs[i].Service != exp {
			t.Fatalf("costs[%d] = %q, want %q (deterministic sort)", i, result.Costs[i].Service, exp)
		}
	}
}

func TestCECollectCosts_EmptyResults(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	api := &fakeCostExplorerAPI{
		results: nil,
	}

	cc := makeCECollector(api)
	result, err := cc.CollectCosts(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Slug != testSlug {
		t.Fatalf("expected slug in report even with empty results")
	}
	if len(result.Costs) != 0 {
		t.Fatalf("expected 0 costs, got %d", len(result.Costs))
	}
	if result.TotalCost != 0 {
		t.Fatalf("TotalCost should be 0 for empty results, got %v", result.TotalCost)
	}
}

func TestCECollectCosts_EmptyGroups(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	api := &fakeCostExplorerAPI{
		results: []cetypes.ResultByTime{
			{
				TimePeriod: &cetypes.DateInterval{
					Start: aws.String(testCEDate1),
					End:   aws.String(nextDate(testCEDate1)),
				},
				Groups: nil,
			},
		},
	}

	cc := makeCECollector(api)
	result, err := cc.CollectCosts(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Costs) != 0 {
		t.Fatalf("expected 0 costs for empty groups, got %d", len(result.Costs))
	}
}

func TestCECollectCosts_GroupWithEmptyKeys(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	api := &fakeCostExplorerAPI{
		results: []cetypes.ResultByTime{
			ceResultByTime(testCEDate1,
				cetypes.Group{
					Keys: []string{}, // empty key — should be skipped
					Metrics: map[string]cetypes.MetricValue{
						costBreakdownMetric: {
							Amount: aws.String("1.00"),
							Unit:   aws.String(testCECurrencyUSD),
						},
					},
				},
				ceServiceGroup(testCEServiceLambda, "0.50"),
			),
		},
	}

	cc := makeCECollector(api)
	result, err := cc.CollectCosts(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Costs) != 1 {
		t.Fatalf("expected 1 cost (empty-key group skipped), got %d", len(result.Costs))
	}
	if result.TotalCost != 0.5 {
		t.Fatalf("TotalCost = %v, want 0.5 (empty-key group excluded)", result.TotalCost)
	}
}

func TestCECollectCosts_NilAmount(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	api := &fakeCostExplorerAPI{
		results: []cetypes.ResultByTime{
			{
				TimePeriod: &cetypes.DateInterval{
					Start: aws.String(testCEDate1),
					End:   aws.String(nextDate(testCEDate1)),
				},
				Groups: []cetypes.Group{
					{
						Keys: []string{testCEServiceLambda},
						Metrics: map[string]cetypes.MetricValue{
							costBreakdownMetric: {
								Amount: nil, // nil amount → treated as 0
								Unit:   aws.String(testCECurrencyUSD),
							},
						},
					},
				},
			},
		},
	}

	cc := makeCECollector(api)
	result, err := cc.CollectCosts(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Costs) != 0 {
		t.Fatalf("expected 0 costs (nil amount treated as 0, zero-cost entry may be omitted), got %d", len(result.Costs))
	}
	if result.TotalCost != 0 {
		t.Fatalf("TotalCost = %v, want 0", result.TotalCost)
	}
}

func TestCECollectCosts_APIError(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	api := &fakeCostExplorerAPI{err: fmt.Errorf("access denied")}
	cc := makeCECollector(api)

	_, err := cc.CollectCosts(context.Background(), validScope(), start, end)
	if err == nil {
		t.Fatal("expected error from GetCostAndUsage")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("error should propagate: %v", err)
	}
}

func TestCECollectCosts_OutputIsDeterministic(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	makeAPI := func() *fakeCostExplorerAPI {
		return &fakeCostExplorerAPI{
			results: []cetypes.ResultByTime{
				ceResultByTime(testCEDate1,
					ceServiceGroup(testCEServiceAPIGW, "0.25"),
					ceServiceGroup(testCEServiceLambda, "1.00"),
				),
			},
		}
	}

	cc1 := makeCECollector(makeAPI())
	result1, err := cc1.CollectCosts(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cc2 := makeCECollector(makeAPI())
	result2, err := cc2.CollectCosts(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result1.Costs) != len(result2.Costs) {
		t.Fatalf("cost count differs: %d vs %d", len(result1.Costs), len(result2.Costs))
	}
	for i := range result1.Costs {
		if result1.Costs[i].Service != result2.Costs[i].Service ||
			result1.Costs[i].Amount != result2.Costs[i].Amount ||
			result1.Costs[i].Currency != result2.Costs[i].Currency {
			t.Fatalf("mismatch at index %d: %+v vs %+v", i, result1.Costs[i], result2.Costs[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Reconcile tests
// ---------------------------------------------------------------------------

func TestReconcile_HappyPath(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0.08,
		Currency:    testCECurrencyUSD,
		Costs: []CostBreakdown{
			{Date: testCEDate1, Service: testCEServiceLambda, Amount: 0.053295, Currency: testCECurrencyUSD},
			{Date: testCEDate1, Service: testCEServiceDynamoDB, Amount: 0.004219, Currency: testCECurrencyUSD},
		},
	}

	cwReport := cwReportWithAttributions(testSlug, testAccountID,
		ServiceAttribution{Service: testCENormLambda, MetricName: "Invocations", Stat: "Sum", Unit: "Count", Value: 42},
		ServiceAttribution{Service: testCENormLambda, MetricName: "Duration", Stat: "Average", Unit: "Milliseconds", Value: 150.5},
		ServiceAttribution{Service: testCENormDynamoDB, MetricName: "ConsumedReadCapacityUnits", Stat: "Sum", Unit: "Count", Value: 1000},
	)

	report, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.Slug != testSlug {
		t.Fatalf("Slug = %q, want %q", report.Slug, testSlug)
	}
	if report.AccountID != testAccountID {
		t.Fatalf("AccountID = %q, want %q", report.AccountID, testAccountID)
	}
	if report.TotalCost != 0.08 {
		t.Fatalf("TotalCost = %v, want 0.08", report.TotalCost)
	}
	if len(report.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %s", len(report.Entries), entryKeys(report.Entries))
	}

	assertReconciledEntryDates(t, report.Entries)
	assertReconciledServiceEntries(t, report.Entries)
}

// assertReconciledEntryDates fails if any reconciled entry has an empty Date.
func assertReconciledEntryDates(t *testing.T, entries []ReconciledCostEntry) {
	t.Helper()
	for _, entry := range entries {
		if entry.Date == "" {
			t.Fatalf("reconciled entry for service %q has empty date", entry.Service)
		}
	}
}

// assertReconciledServiceEntries validates the HappyPath fixture's per-service
// cost and metric counts for Lambda and DynamoDB entries.
func assertReconciledServiceEntries(t *testing.T, entries []ReconciledCostEntry) {
	t.Helper()
	for _, entry := range entries {
		switch entry.Service {
		case testCENormLambda:
			if entry.Cost != 0.053295 {
				t.Fatalf("Lambda cost = %v, want 0.053295", entry.Cost)
			}
			if len(entry.Metrics) != 2 {
				t.Fatalf("Lambda should have 2 metrics, got %d", len(entry.Metrics))
			}
		case testCENormDynamoDB:
			if entry.Cost != 0.004219 {
				t.Fatalf("DynamoDB cost = %v, want 0.004219", entry.Cost)
			}
			if len(entry.Metrics) != 1 {
				t.Fatalf("DynamoDB should have 1 metric, got %d", len(entry.Metrics))
			}
		default:
			t.Fatalf("unexpected service %q", entry.Service)
		}
	}
}

func TestReconcile_NilCEResult(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	cwReport := cwReportWithAttributions(testSlug, testAccountID)

	_, err := cc.Reconcile(nil, cwReport)
	if err == nil {
		t.Fatal("expected error for nil CE result")
	}
	if !strings.Contains(err.Error(), "ceResult is nil") {
		t.Fatalf("error should mention nil ceResult: %v", err)
	}
}

func TestReconcile_NilCWReport(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{Slug: testSlug, AccountID: testAccountID}

	_, err := cc.Reconcile(ceResult, nil)
	if err == nil {
		t.Fatal("expected error for nil CW report")
	}
	if !strings.Contains(err.Error(), "cwReport is nil") {
		t.Fatalf("error should mention nil cwReport: %v", err)
	}
}

func TestReconcile_SlugMismatch(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{Slug: "slug-a", AccountID: testAccountID}
	cwReport := cwReportWithAttributions("slug-b", testAccountID)

	_, err := cc.Reconcile(ceResult, cwReport)
	if err == nil {
		t.Fatal("expected error for slug mismatch")
	}
	if !strings.Contains(err.Error(), "slug mismatch") {
		t.Fatalf("error should mention slug mismatch: %v", err)
	}
}

func TestReconcile_AccountMismatch(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{Slug: testSlug, AccountID: "111111111111"}
	cwReport := cwReportWithAttributions(testSlug, "999999999999")

	_, err := cc.Reconcile(ceResult, cwReport)
	if err == nil {
		t.Fatal("expected error for account mismatch")
	}
	if !strings.Contains(err.Error(), "account mismatch") {
		t.Fatalf("error should mention account mismatch: %v", err)
	}
}

func TestReconcile_EmptyCWAttributions(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0.05,
		Currency:    testCECurrencyUSD,
		Costs: []CostBreakdown{
			{Date: testCEDate1, Service: testCEServiceLambda, Amount: 0.05, Currency: testCECurrencyUSD},
		},
	}

	cwReport := cwReportWithAttributions(testSlug, testAccountID) // empty attributions

	report, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(report.Entries))
	}
	if len(report.Entries[0].Metrics) != 0 {
		t.Fatalf("expected 0 metrics (empty CW), got %d", len(report.Entries[0].Metrics))
	}
	if report.Entries[0].Date == "" {
		t.Fatal("reconciled entry has empty date")
	}
}

func TestReconcile_ServiceMappingApplied(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0.10,
		Currency:    testCECurrencyUSD,
		Costs: []CostBreakdown{
			{Date: testCEDate1, Service: testCEServiceAPIGW, Amount: 0.10, Currency: testCECurrencyUSD},
		},
	}

	// CW attribution uses the normalized name "APIGateway".
	cwReport := cwReportWithAttributions(testSlug, testAccountID,
		ServiceAttribution{Service: testCENormAPIGW, MetricName: "Count", Stat: "Sum", Unit: "Count", Value: 5000},
	)

	report, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(report.Entries))
	}
	entry := report.Entries[0]
	if entry.Service != testCENormAPIGW {
		t.Fatalf("expected normalized service %q, got %q", testCENormAPIGW, entry.Service)
	}
	if len(entry.Metrics) != 1 {
		t.Fatalf("expected 1 metric (mapped via CE→CW), got %d", len(entry.Metrics))
	}
}

func TestReconcile_UnknownServicePassthrough(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0.01,
		Currency:    testCECurrencyUSD,
		Costs: []CostBreakdown{
			{Date: testCEDate1, Service: "AWS NoSuchService", Amount: 0.01, Currency: testCECurrencyUSD},
		},
	}

	cwReport := cwReportWithAttributions(testSlug, testAccountID)

	report, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(report.Entries))
	}
	// Unknown service should pass through unchanged.
	if report.Entries[0].Service != "AWS NoSuchService" {
		t.Fatalf("unknown service should pass through: got %q", report.Entries[0].Service)
	}
}

func TestReconcile_DeterministicOutput(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0.15,
		Currency:    testCECurrencyUSD,
		Costs: []CostBreakdown{
			{Date: testCEDate1, Service: testCEServiceS3, Amount: 0.05, Currency: testCECurrencyUSD},
			{Date: testCEDate1, Service: testCEServiceLambda, Amount: 0.10, Currency: testCECurrencyUSD},
		},
	}

	cwReport := cwReportWithAttributions(testSlug, testAccountID)

	report1, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	report2, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report1.Entries) != len(report2.Entries) {
		t.Fatalf("entry count differs: %d vs %d", len(report1.Entries), len(report2.Entries))
	}
	for i := range report1.Entries {
		e1 := report1.Entries[i]
		e2 := report2.Entries[i]
		if e1.Service != e2.Service || e1.Cost != e2.Cost {
			t.Fatalf("mismatch at index %d: %+v vs %+v", i, e1, e2)
		}
	}
}

func TestReconcile_WindowIntersection(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0.05,
		Currency:    testCECurrencyUSD,
		Costs: []CostBreakdown{
			{Date: testCEDate1, Service: testCEServiceLambda, Amount: 0.05, Currency: testCECurrencyUSD},
		},
	}

	// CW window is tighter — starts later, ends earlier.
	cwReport := cwReportWithAttributions(testSlug, testAccountID)
	cwReport.WindowStart = time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	cwReport.WindowEnd = time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	report, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !report.WindowStart.Equal(cwReport.WindowStart) {
		t.Fatalf("WindowStart = %v, want %v (tighter CW start)", report.WindowStart, cwReport.WindowStart)
	}
	if !report.WindowEnd.Equal(cwReport.WindowEnd) {
		t.Fatalf("WindowEnd = %v, want %v (tighter CW end)", report.WindowEnd, cwReport.WindowEnd)
	}
}

func TestReconcile_NoCosts(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0,
		Currency:    testCECurrencyUSD,
		Costs:       nil,
	}

	cwReport := cwReportWithAttributions(testSlug, testAccountID,
		ServiceAttribution{Service: testCENormLambda, MetricName: "Invocations", Stat: "Sum", Unit: "Count", Value: 42},
	)

	report, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Entries) != 0 {
		t.Fatalf("expected 0 entries for no costs, got %d", len(report.Entries))
	}
	if report.TotalCost != 0 {
		t.Fatalf("TotalCost = %v, want 0", report.TotalCost)
	}
}

// ---------------------------------------------------------------------------
// unit tests for helpers
// ---------------------------------------------------------------------------

func TestCeilingDay_AfterMidnight(t *testing.T) {
	t.Parallel()

	// 2026-05-26 12:00:00 UTC → should return 2026-05-27 00:00:00 UTC.
	input := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	got := ceilingDay(input)
	expected := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	if !got.Equal(expected) {
		t.Fatalf("ceilingDay(%v) = %v, want %v", input, got, expected)
	}
}

func TestCeilingDay_AtMidnight(t *testing.T) {
	t.Parallel()

	// Already at midnight — should stay at midnight.
	input := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	got := ceilingDay(input)
	if !got.Equal(input) {
		t.Fatalf("ceilingDay at midnight should return same time; got %v, want %v", got, input)
	}
}

func TestCeilingDay_EndOfDay(t *testing.T) {
	t.Parallel()

	// 2026-05-26 23:59:59 UTC → should return 2026-05-27 00:00:00 UTC.
	input := time.Date(2026, 5, 26, 23, 59, 59, 0, time.UTC)
	got := ceilingDay(input)
	expected := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	if !got.Equal(expected) {
		t.Fatalf("ceilingDay(%v) = %v, want %v", input, got, expected)
	}
}

func TestCeilingDay_StartOfDay(t *testing.T) {
	t.Parallel()

	// 2026-05-26 00:00:01 UTC → should return 2026-05-27 00:00:00 UTC.
	input := time.Date(2026, 5, 26, 0, 0, 1, 0, time.UTC)
	got := ceilingDay(input)
	expected := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	if !got.Equal(expected) {
		t.Fatalf("ceilingDay(%v) = %v, want %v", input, got, expected)
	}
}

func TestExtractMetricValueCE_HappyPath(t *testing.T) {
	t.Parallel()

	mv := cetypes.MetricValue{
		Amount: aws.String("0.0532947123"),
		Unit:   aws.String(testCECurrencyUSD),
	}
	amount, currency := extractMetricValueCE(mv)
	if amount != 0.0532947123 {
		t.Fatalf("amount = %v, want 0.0532947123", amount)
	}
	if currency != testCECurrencyUSD {
		t.Fatalf("currency = %q, want USD", currency)
	}
}

func TestExtractMetricValueCE_NilAmount(t *testing.T) {
	t.Parallel()

	mv := cetypes.MetricValue{
		Amount: nil,
		Unit:   aws.String(testCECurrencyUSD),
	}
	amount, currency := extractMetricValueCE(mv)
	if amount != 0 {
		t.Fatalf("amount = %v, want 0", amount)
	}
	if currency != testCECurrencyUSD {
		t.Fatalf("currency = %q, want USD", currency)
	}
}

func TestExtractMetricValueCE_NilUnit(t *testing.T) {
	t.Parallel()

	mv := cetypes.MetricValue{
		Amount: aws.String("1.50"),
		Unit:   nil,
	}
	amount, currency := extractMetricValueCE(mv)
	if amount != 1.50 {
		t.Fatalf("amount = %v, want 1.50", amount)
	}
	if currency != "" {
		t.Fatalf("currency = %q, want empty string", currency)
	}
}

func TestExtractMetricValueCE_BadFormat(t *testing.T) {
	t.Parallel()

	mv := cetypes.MetricValue{
		Amount: aws.String("not-a-number"),
		Unit:   aws.String(testCECurrencyUSD),
	}
	amount, currency := extractMetricValueCE(mv)
	if amount != 0 {
		t.Fatalf("amount = %v, want 0 for unparseable value", amount)
	}
	if currency != testCECurrencyUSD {
		t.Fatalf("currency = %q, want USD", currency)
	}
}

func TestMapCEToCWService_KnownMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ceName string
		want   string
	}{
		{"AWS Lambda", testCENormLambda},
		{"Amazon DynamoDB", testCENormDynamoDB},
		{"Amazon API Gateway", testCENormAPIGW},
		{"Amazon Simple Storage Service", "S3"},
		{"Amazon Simple Queue Service", "SQS"},
		{"Amazon CloudFront", "CloudFront"},
		{"AWS CloudFront", "CloudFront"},
		{"Amazon Route 53", "Route53"},
		{"AWS Route 53", "Route53"},
		{"AWS Key Management Service", "KMS"},
		{"AWS CodeBuild", "CodeBuild"},
		{"AWS Secrets Manager", "SecretsManager"},
		{"AWS Step Functions", "StepFunctions"},
		{"Amazon CloudWatch", "CloudWatch"},
		{"Amazon Cognito", "Cognito"},
		{"Amazon EC2", "EC2"},
		{"Amazon Elastic Compute Cloud", "EC2"},
		{"EC2 - Other", "EC2"},
		{"AWS Certificate Manager", "ACM"},
		{"AWS Data Transfer", "DataTransfer"},
		{"AWS Elastic Load Balancing", "ELB"},
	}

	for _, tt := range tests {
		got := mapCEToCWService(tt.ceName)
		if got != tt.want {
			t.Fatalf("mapCEToCWService(%q) = %q, want %q", tt.ceName, got, tt.want)
		}
	}
}

func TestMapCEToCWService_UnknownPassthrough(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"AWS NoSuchService",
		"",
		"Amazon SomethingUnknown",
		"Tax",
		"AWS Support",
	}
	for _, in := range inputs {
		got := mapCEToCWService(in)
		if got != in {
			t.Fatalf("mapCEToCWService(%q) = %q, want %q (passthrough)", in, got, in)
		}
	}
}

func TestBuildCWAttributionLookup_Empty(t *testing.T) {
	t.Parallel()

	got := buildCWAttributionLookup(nil)
	if got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}

	got = buildCWAttributionLookup([]ServiceAttribution{})
	if got != nil {
		t.Fatalf("expected nil for empty slice, got %v", got)
	}
}

func TestBuildCWAttributionLookup_HappyPath(t *testing.T) {
	t.Parallel()

	attrs := []ServiceAttribution{
		{Service: testCENormLambda, MetricName: "Invocations", Stat: "Sum", Unit: "Count", Value: 42},
		{Service: testCENormLambda, MetricName: "Duration", Stat: "Average", Unit: "Milliseconds", Value: 150.5},
		{Service: testCENormDynamoDB, MetricName: "ConsumedReadCapacityUnits", Stat: "Sum", Unit: "Count", Value: 1000},
	}

	lookup := buildCWAttributionLookup(attrs)
	if len(lookup) != 2 {
		t.Fatalf("expected 2 service keys, got %d", len(lookup))
	}
	if len(lookup[testCENormLambda]) != 2 {
		t.Fatalf("Lambda should have 2 attributions, got %d", len(lookup[testCENormLambda]))
	}
	if len(lookup[testCENormDynamoDB]) != 1 {
		t.Fatalf("DynamoDB should have 1 attribution, got %d", len(lookup[testCENormDynamoDB]))
	}
}

// ---------------------------------------------------------------------------
// integration-style: NewCostExplorerCollector returns a usable interface
// ---------------------------------------------------------------------------

func TestNewCostExplorerCollector_ReturnsInterface(t *testing.T) {
	api := &fakeCostExplorerAPI{
		results: []cetypes.ResultByTime{
			ceResultByTime(testCEDate1,
				ceServiceGroup(testCEServiceLambda, "1.00"),
			),
		},
	}

	collector := NewCostExplorerCollector(&fakeCostExplorerClientFactory{api: api})
	if collector == nil {
		t.Fatal("NewCostExplorerCollector returned nil")
	}

	start, end := validWindow()
	result, err := collector.CollectCosts(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
		return
	}
	if result.Slug != testSlug {
		t.Fatalf("expected slug 'demo', got %q", result.Slug)
	}
	if len(result.Costs) != 1 {
		t.Fatalf("expected 1 cost, got %d", len(result.Costs))
	}
}

// ---------------------------------------------------------------------------
// reconciliation edge cases
// ---------------------------------------------------------------------------

func TestReconcile_MultipleMetricsSameService(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0.05,
		Currency:    testCECurrencyUSD,
		Costs: []CostBreakdown{
			{Date: testCEDate1, Service: testCEServiceLambda, Amount: 0.05, Currency: testCECurrencyUSD},
		},
	}

	cwReport := cwReportWithAttributions(testSlug, testAccountID,
		ServiceAttribution{Service: testCENormLambda, MetricName: "Invocations", Stat: "Sum", Unit: "Count", Value: 42},
		ServiceAttribution{Service: testCENormLambda, MetricName: "Duration", Stat: "Average", Unit: "Milliseconds", Value: 150.5},
		ServiceAttribution{Service: testCENormLambda, MetricName: "Errors", Stat: "Sum", Unit: "Count", Value: 0},
	)

	report, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(report.Entries))
	}
	if len(report.Entries[0].Metrics) != 3 {
		t.Fatalf("expected 3 metrics for Lambda, got %d", len(report.Entries[0].Metrics))
	}
}

func TestReconcile_CWServiceNotInCE(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0.05,
		Currency:    testCECurrencyUSD,
		Costs: []CostBreakdown{
			{Date: testCEDate1, Service: testCEServiceLambda, Amount: 0.05, Currency: testCECurrencyUSD},
		},
	}

	// CW has metrics for DynamoDB but CE has no DynamoDB cost — that's fine.
	// The DynamoDB metrics simply don't attach to any CE entry.
	cwReport := cwReportWithAttributions(testSlug, testAccountID,
		ServiceAttribution{Service: testCENormDynamoDB, MetricName: "ConsumedReadCapacityUnits", Stat: "Sum", Unit: "Count", Value: 500},
	)

	report, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Lambda entry should have no metrics (CW has no Lambda data).
	if len(report.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(report.Entries))
	}
	if len(report.Entries[0].Metrics) != 0 {
		t.Fatalf("Lambda entry should have 0 metrics (CW has DynamoDB only), got %d",
			len(report.Entries[0].Metrics))
	}
}

func TestReconcile_SortOrder(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0.35,
		Currency:    testCECurrencyUSD,
		Costs: []CostBreakdown{
			{Date: testCEDate1, Service: testCEServiceS3, Amount: 0.10, Currency: testCECurrencyUSD},
			{Date: testCEDate1, Service: testCEServiceSQS, Amount: 0.05, Currency: testCECurrencyUSD},
			{Date: testCEDate1, Service: testCEServiceLambda, Amount: 0.20, Currency: testCECurrencyUSD},
		},
	}

	cwReport := cwReportWithAttributions(testSlug, testAccountID)

	report, err := cc.Reconcile(ceResult, cwReport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be sorted by service, then by cost.
	expectedOrder := []string{testCENormLambda, "S3", "SQS"}
	for i, exp := range expectedOrder {
		if report.Entries[i].Service != exp {
			t.Fatalf("entries[%d] = %q, want %q (deterministic sort)", i, report.Entries[i].Service, exp)
		}
	}
}

// TestReconcile_EmptyDate verifies that Reconcile returns an error when
// any CostBreakdown has an empty Date. M3.9 acceptance requires per-day
// attribution; entries without dates are unusable.
func TestReconcile_EmptyDate(t *testing.T) {
	t.Parallel()

	cc := makeCECollector(nil)
	ceResult := &CostExplorerResult{
		Slug:        testSlug,
		AccountID:   testAccountID,
		WindowStart: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		TotalCost:   0.10,
		Currency:    testCECurrencyUSD,
		Costs: []CostBreakdown{
			{Date: testCEDate1, Service: testCEServiceLambda, Amount: 0.05, Currency: testCECurrencyUSD},
			{Date: "", Service: testCEServiceDynamoDB, Amount: 0.05, Currency: testCECurrencyUSD},
		},
	}

	cwReport := cwReportWithAttributions(testSlug, testAccountID)

	_, err := cc.Reconcile(ceResult, cwReport)
	if err == nil {
		t.Fatal("expected error for empty Date in CostBreakdown")
	}
	if !strings.Contains(err.Error(), "empty date") {
		t.Fatalf("error should mention empty date: %v", err)
	}
}
