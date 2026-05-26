package costtelemetry

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

const (
	testSlug          = "demo"
	testAccountID     = "123456789012"
	testRegion        = "us-east-1"
	testServiceLambda = "Lambda"
)

// ---------------------------------------------------------------------------
// fake CloudWatch implementations for tests
// ---------------------------------------------------------------------------

// fakeCloudWatchAPI implements cloudWatchAPI for tests.
type fakeCloudWatchAPI struct {
	// metricResults maps query ID → MetricDataResult.
	metricResults map[string]cwtypes.MetricDataResult
	// err is returned from GetMetricData if non-nil.
	err error
}

func (f *fakeCloudWatchAPI) GetMetricData(_ context.Context, params *cloudwatch.GetMetricDataInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	results := make([]cwtypes.MetricDataResult, len(params.MetricDataQueries))
	for i, q := range params.MetricDataQueries {
		if r, ok := f.metricResults[*q.Id]; ok {
			results[i] = r
		} else {
			results[i] = cwtypes.MetricDataResult{
				Id:     q.Id,
				Values: []float64{},
			}
		}
	}
	return &cloudwatch.GetMetricDataOutput{MetricDataResults: results}, nil
}

// fakeCloudWatchClientFactory implements CloudWatchClientFactory for tests.
type fakeCloudWatchClientFactory struct {
	api cloudWatchAPI
}

func (f *fakeCloudWatchClientFactory) Client(_ context.Context, _, _ string) (cloudWatchAPI, error) {
	if f.api == nil {
		return nil, fmt.Errorf("no CloudWatch client configured for test")
	}
	return f.api, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func validScope() TenantScope {
	return TenantScope{
		Slug:      testSlug,
		AccountID: testAccountID,
		Region:    testRegion,
	}
}

func validWindow() (time.Time, time.Time) {
	start := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Hour)
	return start, end
}

func makeCollector(api cloudWatchAPI) CloudWatchCollector {
	factory := &fakeCloudWatchClientFactory{api: api}
	return NewCloudWatchCollector(factory)
}

func metricResultsFromValues(values map[string][]float64) map[string]cwtypes.MetricDataResult {
	out := make(map[string]cwtypes.MetricDataResult, len(values))
	for id, vals := range values {
		out[id] = cwtypes.MetricDataResult{Id: aws.String(id), Values: vals}
	}
	return out
}

// attributionKeys returns a sorted, joined string of "service/metric" keys
// for assertion messages.
func attributionKeys(attributions []ServiceAttribution) string {
	keys := make([]string, len(attributions))
	for i, a := range attributions {
		keys[i] = a.Service + "/" + a.MetricName
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// findAttribution locates a matching ServiceAttribution by service and name.
func findAttribution(attributions []ServiceAttribution, service, metricName string) (ServiceAttribution, bool) {
	for _, a := range attributions {
		if a.Service == service && a.MetricName == metricName {
			return a, true
		}
	}
	return ServiceAttribution{}, false
}

// ---------------------------------------------------------------------------
// validation tests
// ---------------------------------------------------------------------------

func TestValidateScope_BlankSlug(t *testing.T) {
	t.Parallel()
	c := makeCollector(&fakeCloudWatchAPI{})
	scope := validScope()
	scope.Slug = ""
	start, end := validWindow()

	_, err := c.CollectMetrics(context.Background(), scope, start, end)
	if err == nil {
		t.Fatal("expected error for blank slug")
	}
	if !strings.Contains(err.Error(), "Slug") {
		t.Fatalf("error should mention Slug: %v", err)
	}
}

func TestValidateScope_BlankAccountID(t *testing.T) {
	t.Parallel()
	c := makeCollector(&fakeCloudWatchAPI{})
	scope := validScope()
	scope.AccountID = ""
	start, end := validWindow()

	_, err := c.CollectMetrics(context.Background(), scope, start, end)
	if err == nil {
		t.Fatal("expected error for blank AccountID")
	}
	if !strings.Contains(err.Error(), "AccountID") {
		t.Fatalf("error should mention AccountID: %v", err)
	}
}

func TestValidateScope_BlankRegion(t *testing.T) {
	t.Parallel()
	c := makeCollector(&fakeCloudWatchAPI{})
	scope := validScope()
	scope.Region = ""
	start, end := validWindow()

	_, err := c.CollectMetrics(context.Background(), scope, start, end)
	if err == nil {
		t.Fatal("expected error for blank Region")
	}
	if !strings.Contains(err.Error(), "Region") {
		t.Fatalf("error should mention Region: %v", err)
	}
}

func TestValidateWindow_Inverted(t *testing.T) {
	t.Parallel()
	c := makeCollector(&fakeCloudWatchAPI{})
	scope := validScope()
	end := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	start := end.Add(1 * time.Hour) // start after end

	_, err := c.CollectMetrics(context.Background(), scope, start, end)
	if err == nil {
		t.Fatal("expected error for inverted window")
	}
	if !strings.Contains(err.Error(), "windowStart must be before windowEnd") {
		t.Fatalf("error should mention window ordering: %v", err)
	}
}

func TestValidateWindow_Equal(t *testing.T) {
	t.Parallel()
	c := makeCollector(&fakeCloudWatchAPI{})
	scope := validScope()
	ts := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	_, err := c.CollectMetrics(context.Background(), scope, ts, ts)
	if err == nil {
		t.Fatal("expected error for equal start/end")
	}
}

func TestCollectMetrics_FactoryError(t *testing.T) {
	t.Parallel()
	// Factory that always returns an error.
	collector := NewCloudWatchCollector(&fakeCloudWatchClientFactory{api: nil})
	scope := validScope()
	start, end := validWindow()

	_, err := collector.CollectMetrics(context.Background(), scope, start, end)
	if err == nil {
		t.Fatal("expected error from factory")
	}
}

// ---------------------------------------------------------------------------
// happy-path tests
// ---------------------------------------------------------------------------

func TestCollectMetrics_HappyPath(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	results := metricResultsFromValues(map[string][]float64{
		"m0":  {42},      // Lambda/Invocations
		"m1":  {150.5},   // Lambda/Duration
		"m2":  {0},       // Lambda/Errors
		"m3":  {0},       // Lambda/Throttles
		"m4":  {1000},    // DynamoDB/ConsumedReadCapacityUnits
		"m5":  {500},     // DynamoDB/ConsumedWriteCapacityUnits
		"m6":  {10000},   // APIGateway/Count
		"m7":  {45.2},    // APIGateway/Latency
		"m8":  {12},      // APIGateway/4XXError
		"m9":  {2},       // APIGateway/5XXError
		"m10": {300},     // SQS/NumberOfMessagesSent
		"m11": {280},     // SQS/NumberOfMessagesReceived
		"m12": {5},       // S3/NumberOfObjects
		"m13": {1048576}, // S3/BucketSizeBytes
		"m14": {5000},    // CloudFront/Requests
		"m15": {5242880}, // CloudFront/BytesDownloaded
	})

	collector := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	report, err := collector.CollectMetrics(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.Slug != testSlug {
		t.Fatalf("expected slug 'demo', got %q", report.Slug)
	}
	if report.AccountID != testAccountID {
		t.Fatalf("expected AccountID '123456789012', got %q", report.AccountID)
	}
	if !report.WindowStart.Equal(start) {
		t.Fatalf("expected WindowStart %v, got %v", start, report.WindowStart)
	}
	if !report.WindowEnd.Equal(end) {
		t.Fatalf("expected WindowEnd %v, got %v", end, report.WindowEnd)
	}

	expectedCount := len(defaultMetrics) // 16
	if len(report.Attributions) != expectedCount {
		t.Fatalf("expected %d attributions, got %d: %s",
			expectedCount, len(report.Attributions), attributionKeys(report.Attributions))
	}

	// Spot-check a few attributions.
	checks := []struct {
		service, metric string
		want            float64
	}{
		{testServiceLambda, "Invocations", 42},
		{testServiceLambda, "Duration", 150.5},
		{"DynamoDB", "ConsumedReadCapacityUnits", 1000},
		{"APIGateway", "Count", 10000},
		{"CloudFront", "Requests", 5000},
		{"S3", "BucketSizeBytes", 1048576},
	}
	for _, ch := range checks {
		a, ok := findAttribution(report.Attributions, ch.service, ch.metric)
		if !ok {
			t.Fatalf("missing attribution for %s/%s", ch.service, ch.metric)
		}
		if a.Value != ch.want {
			t.Fatalf("%s/%s value = %v, want %v", ch.service, ch.metric, a.Value, ch.want)
		}
	}
}

func TestCollectMetrics_OutputIsDeterministic(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	// Two different providers with shuffled metric order — output should
	// be identical (sorted by service, then metric name).
	results := metricResultsFromValues(map[string][]float64{
		"m0": {1}, "m1": {2}, "m2": {3}, "m3": {4},
		"m4": {5}, "m5": {6}, "m6": {7}, "m7": {8},
		"m8": {9}, "m9": {10}, "m10": {11}, "m11": {12},
		"m12": {13}, "m13": {14}, "m14": {15}, "m15": {16},
	})

	collector := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	report1, err := collector.CollectMetrics(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	collector2 := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	report2, err := collector2.CollectMetrics(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report1.Attributions) != len(report2.Attributions) {
		t.Fatalf("attribution count differs: %d vs %d",
			len(report1.Attributions), len(report2.Attributions))
	}
	for i := range report1.Attributions {
		a1 := report1.Attributions[i]
		a2 := report2.Attributions[i]
		if a1.Service != a2.Service || a1.MetricName != a2.MetricName || a1.Value != a2.Value {
			t.Fatalf("mismatch at index %d: (%s/%s=%v) vs (%s/%s=%v)",
				i, a1.Service, a1.MetricName, a1.Value, a2.Service, a2.MetricName, a2.Value)
		}
	}
}

func TestCollectMetrics_ServiceFilter(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	results := metricResultsFromValues(map[string][]float64{
		"m0": {10}, // Lambda/Invocations
		"m1": {20}, // Lambda/Duration
		"m2": {0},  // Lambda/Errors
		"m3": {0},  // Lambda/Throttles
	})

	scope := validScope()
	scope.Services = []string{testServiceLambda}

	collector := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	report, err := collector.CollectMetrics(context.Background(), scope, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only have Lambda metrics (4 of them).
	if len(report.Attributions) != 4 {
		t.Fatalf("expected 4 Lambda attributions, got %d: %s",
			len(report.Attributions), attributionKeys(report.Attributions))
	}
	for _, a := range report.Attributions {
		if a.Service != testServiceLambda {
			t.Fatalf("unexpected service %q in filtered results", a.Service)
		}
	}
}

func TestCollectMetrics_EmptyResults(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	// All metrics return empty values.
	results := metricResultsFromValues(map[string][]float64{})

	collector := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	report, err := collector.CollectMetrics(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.Slug != testSlug {
		t.Fatalf("expected slug in report even with empty results")
	}
	// Empty values → no attributions (extractMetricValue returns 0, but 0
	// is a valid metric value so it IS included for Sum stats; for empty
	// slices extractMetricValue returns 0 which is valid).
	if len(report.Attributions) == 0 {
		t.Fatalf("expected attributions even with empty results (zero is a valid metric value)")
	}
}

func TestCollectMetrics_GetMetricDataError(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	api := &fakeCloudWatchAPI{err: fmt.Errorf("throttled")}
	collector := makeCollector(api)

	_, err := collector.CollectMetrics(context.Background(), validScope(), start, end)
	if err == nil {
		t.Fatal("expected error from GetMetricData")
	}
	if !strings.Contains(err.Error(), "throttled") {
		t.Fatalf("error should propagate the underlying error: %v", err)
	}
}

func TestCollectMetrics_NaNValuesFiltered(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	// Some metrics return NaN — these should be excluded from output.
	results := metricResultsFromValues(map[string][]float64{
		"m0": {math.NaN()}, // Lambda/Invocations (filtered)
		"m1": {100},        // Lambda/Duration (kept)
		"m2": {math.NaN()}, // Lambda/Errors (filtered)
	})

	scope := validScope()
	scope.Services = []string{testServiceLambda}

	collector := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	report, err := collector.CollectMetrics(context.Background(), scope, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if att, ok := findAttribution(report.Attributions, testServiceLambda, "Duration"); !ok || att.Value != 100 {
		t.Fatalf("expected Duration=100 to be present, got %v", att)
	}
	if _, ok := findAttribution(report.Attributions, testServiceLambda, "Invocations"); ok {
		t.Fatal("NaN Invocations value should have been filtered")
	}
}

func TestCollectMetrics_InfValuesFiltered(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	results := metricResultsFromValues(map[string][]float64{
		"m0": {math.Inf(1)}, // Lambda/Invocations (filtered)
		"m1": {100},         // Lambda/Duration (kept)
	})

	scope := validScope()
	scope.Services = []string{testServiceLambda}

	collector := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	report, err := collector.CollectMetrics(context.Background(), scope, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := findAttribution(report.Attributions, testServiceLambda, "Invocations"); ok {
		t.Fatal("Inf Invocations value should have been filtered")
	}
	if att, ok := findAttribution(report.Attributions, testServiceLambda, "Duration"); !ok || att.Value != 100 {
		t.Fatalf("expected Duration=100 to be present")
	}
}

func TestCollectMetrics_SumStatAggregatesValues(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	// For Sum stats, multiple values should be summed.
	results := metricResultsFromValues(map[string][]float64{
		"m0": {10, 20, 30}, // Lambda/Invocations (Sum) → 60
	})

	scope := validScope()
	scope.Services = []string{testServiceLambda}

	collector := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	report, err := collector.CollectMetrics(context.Background(), scope, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	att, ok := findAttribution(report.Attributions, testServiceLambda, "Invocations")
	if !ok {
		t.Fatal("missing Lambda/Invocations")
	}
	if att.Value != 60 {
		t.Fatalf("expected summed value 60, got %v", att.Value)
	}
}

func TestCollectMetrics_AverageStatTakesLastValue(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	// For Average stats, the last valid value is taken.
	results := metricResultsFromValues(map[string][]float64{
		"m1": {10, 20, math.NaN(), 25}, // Lambda/Duration (Average) → 25
	})

	scope := validScope()
	scope.Services = []string{testServiceLambda}

	collector := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	report, err := collector.CollectMetrics(context.Background(), scope, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	att, ok := findAttribution(report.Attributions, testServiceLambda, "Duration")
	if !ok {
		t.Fatal("missing Lambda/Duration")
	}
	if att.Value != 25 {
		t.Fatalf("expected last valid value 25, got %v", att.Value)
	}
}

func TestCollectMetrics_NoServicesReturnsAllDefaults(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	results := metricResultsFromValues(map[string][]float64{})
	for i := range defaultMetrics {
		id := fmt.Sprintf("m%d", i)
		results[id] = cwtypes.MetricDataResult{Id: aws.String(id), Values: []float64{float64(i)}}
	}

	collector := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	scope := validScope()
	scope.Services = nil // should default to all

	report, err := collector.CollectMetrics(context.Background(), scope, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Attributions) != len(defaultMetrics) {
		t.Fatalf("expected %d attributions, got %d", len(defaultMetrics), len(report.Attributions))
	}
}

func TestCollectMetrics_AttributionFields(t *testing.T) {
	t.Parallel()

	start, end := validWindow()
	results := metricResultsFromValues(map[string][]float64{
		"m0": {42}, // Lambda/Invocations
	})

	scope := validScope()
	scope.Services = []string{testServiceLambda}

	collector := makeCollector(&fakeCloudWatchAPI{metricResults: results})
	report, err := collector.CollectMetrics(context.Background(), scope, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	att, ok := findAttribution(report.Attributions, testServiceLambda, "Invocations")
	if !ok {
		t.Fatal("missing Lambda/Invocations")
	}

	if att.Service != testServiceLambda {
		t.Fatalf("Service = %q, want Lambda", att.Service)
	}
	if att.MetricName != "Invocations" {
		t.Fatalf("MetricName = %q, want Invocations", att.MetricName)
	}
	if att.Stat != "Sum" {
		t.Fatalf("Stat = %q, want Sum", att.Stat)
	}
	if att.Unit != "Count" {
		t.Fatalf("Unit = %q, want Count", att.Unit)
	}
	if att.Value != 42 {
		t.Fatalf("Value = %v, want 42", att.Value)
	}
}

// ---------------------------------------------------------------------------
// unit tests for helpers
// ---------------------------------------------------------------------------

func TestExtractMetricValue_Empty(t *testing.T) {
	if v := extractMetricValue(nil, "Sum"); v != 0 {
		t.Fatalf("expected 0 for nil, got %v", v)
	}
	if v := extractMetricValue([]float64{}, "Average"); v != 0 {
		t.Fatalf("expected 0 for empty, got %v", v)
	}
}

func TestExtractMetricValue_SumSkipsNaN(t *testing.T) {
	v := extractMetricValue([]float64{1, math.NaN(), 2, math.Inf(1), 3}, "Sum")
	if v != 6 {
		t.Fatalf("expected 6 (1+2+3, skipping NaN and Inf), got %v", v)
	}
}

func TestExtractMetricValue_NonSumSkipsNaN(t *testing.T) {
	// With all NaN except the last, should return the last valid.
	v := extractMetricValue([]float64{math.NaN(), math.NaN(), 42}, "Average")
	if v != 42 {
		t.Fatalf("expected 42, got %v", v)
	}
}

func TestFilterMetricsByService_Empty(t *testing.T) {
	out := filterMetricsByService(defaultMetrics, nil)
	if len(out) != len(defaultMetrics) {
		t.Fatalf("expected all %d metrics, got %d", len(defaultMetrics), len(out))
	}
}

func TestFilterMetricsByService_Match(t *testing.T) {
	out := filterMetricsByService(defaultMetrics, []string{testServiceLambda, "SQS"})
	for _, m := range out {
		if m.Service != testServiceLambda && m.Service != "SQS" {
			t.Fatalf("unexpected service %q in filtered output", m.Service)
		}
	}
	// Lambda has 4 metrics, SQS has 2.
	if len(out) != 6 {
		t.Fatalf("expected 6 metrics (4 Lambda + 2 SQS), got %d", len(out))
	}
}

func TestFilterMetricsByService_NoMatch(t *testing.T) {
	out := filterMetricsByService(defaultMetrics, []string{"NoSuchService"})
	if len(out) != 0 {
		t.Fatalf("expected 0 metrics for unmatched service, got %d", len(out))
	}
}

func TestMetricUnitFromString(t *testing.T) {
	tests := []struct {
		input string
		want  cwtypes.StandardUnit
	}{
		{"Count", cwtypes.StandardUnitCount},
		{"Milliseconds", cwtypes.StandardUnitMilliseconds},
		{"Bytes", cwtypes.StandardUnitBytes},
		{"Seconds", cwtypes.StandardUnitSeconds},
		{"Percent", cwtypes.StandardUnitPercent},
		{"Microseconds", cwtypes.StandardUnitMicroseconds},
		{"Bytes/Second", cwtypes.StandardUnitBytesSecond},
		{"Count/Second", cwtypes.StandardUnitCountSecond},
		{"UnknownUnit", cwtypes.StandardUnitNone},
		{"", cwtypes.StandardUnitNone},
	}
	for _, tt := range tests {
		got := metricUnitFromString(tt.input)
		if got != tt.want {
			t.Fatalf("metricUnitFromString(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestDefaultMetrics_AllHaveValidUnits(t *testing.T) {
	for _, m := range defaultMetrics {
		u := metricUnitFromString(m.Unit)
		if u == cwtypes.StandardUnitNone && m.Unit != "" {
			t.Fatalf("defaultMetric %s/%s has unhandled unit %q",
				m.Service, m.MetricName, m.Unit)
		}
	}
}

// ---------------------------------------------------------------------------
// integration-style: NewCloudWatchCollector returns a usable interface
// ---------------------------------------------------------------------------

func TestNewCloudWatchCollector_ReturnsInterface(t *testing.T) {
	factory := &fakeCloudWatchClientFactory{
		api: &fakeCloudWatchAPI{metricResults: map[string]cwtypes.MetricDataResult{
			"m0":  {Id: aws.String("m0"), Values: []float64{1}},
			"m1":  {Id: aws.String("m1"), Values: []float64{2}},
			"m2":  {Id: aws.String("m2"), Values: []float64{0}},
			"m3":  {Id: aws.String("m3"), Values: []float64{0}},
			"m4":  {Id: aws.String("m4"), Values: []float64{0}},
			"m5":  {Id: aws.String("m5"), Values: []float64{0}},
			"m6":  {Id: aws.String("m6"), Values: []float64{0}},
			"m7":  {Id: aws.String("m7"), Values: []float64{0}},
			"m8":  {Id: aws.String("m8"), Values: []float64{0}},
			"m9":  {Id: aws.String("m9"), Values: []float64{0}},
			"m10": {Id: aws.String("m10"), Values: []float64{0}},
			"m11": {Id: aws.String("m11"), Values: []float64{0}},
			"m12": {Id: aws.String("m12"), Values: []float64{0}},
			"m13": {Id: aws.String("m13"), Values: []float64{0}},
			"m14": {Id: aws.String("m14"), Values: []float64{0}},
			"m15": {Id: aws.String("m15"), Values: []float64{0}},
		}},
	}

	collector := NewCloudWatchCollector(factory)
	if collector == nil {
		t.Fatal("NewCloudWatchCollector returned nil")
	}

	// Should satisfy the interface and return useful data.
	start, end := validWindow()
	report, err := collector.CollectMetrics(context.Background(), validScope(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.Slug != testSlug {
		t.Fatalf("expected slug 'demo', got %q", report.Slug)
	}
}
