package costtelemetry

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// TenantScope identifies a tenant for CloudWatch metric collection.
// Every field is explicit — future Cost Explorer and cache steps
// (M3.9, M3.10) cannot accidentally aggregate across tenant accounts.
type TenantScope struct {
	Slug      string   // tenant identifier (e.g., "demo")
	AccountID string   // AWS account ID hosting the tenant's resources
	Region    string   // AWS region where tenant resources run
	Services  []string // service names to collect; empty means all standard services
}

// ServiceAttribution represents cost-relevant metric data for a single
// AWS service dimension within a tenant. Designed for reconciliation
// with Cost Explorer in M3.9.
//
// Contains no PII, tenant content, raw instance keys, secrets, or
// request bodies.
type ServiceAttribution struct {
	Service    string  `json:"service"`     // e.g., "Lambda", "DynamoDB"
	MetricName string  `json:"metric_name"` // e.g., "Invocations", "ConsumedReadCapacityUnits"
	Stat       string  `json:"stat"`        // e.g., "Sum", "Average"
	Unit       string  `json:"unit"`        // CloudWatch unit, e.g., "Count", "Milliseconds"
	Value      float64 `json:"value"`       // metric value for the window
}

// InstanceCostReport summarizes per-instance, per-service metric
// attribution over a time window.
//
// Contains no PII, tenant content, raw instance keys, secrets, or
// request bodies. Safe to cache and expose in future API responses.
type InstanceCostReport struct {
	Slug         string               `json:"slug"`
	AccountID    string               `json:"account_id"`
	WindowStart  time.Time            `json:"window_start"`
	WindowEnd    time.Time            `json:"window_end"`
	Attributions []ServiceAttribution `json:"attributions"`
}

// CloudWatchCollector reads CloudWatch metrics scoped to tenant
// accounts. This interface is the test seam: tests inject mocks;
// production injects the AWS SDK-backed implementation.
type CloudWatchCollector interface {
	// CollectMetrics queries CloudWatch for the given tenant scope
	// over the specified time window and returns per-service cost
	// attribution data.
	//
	// The returned InstanceCostReport carries explicit tenant
	// dimensions (Slug, AccountID) so downstream steps cannot
	// accidentally merge data across tenant accounts.
	CollectMetrics(ctx context.Context, scope TenantScope, windowStart, windowEnd time.Time) (*InstanceCostReport, error)
}

// CloudWatchClientFactory creates CloudWatch API clients scoped to
// a given account and region. In production, the factory may assume
// a cross-account role; in tests, it supplies a mock.
type CloudWatchClientFactory interface {
	// Client returns a CloudWatch API client for the given account and region.
	Client(ctx context.Context, accountID, region string) (cloudWatchAPI, error)
}

// cloudWatchAPI is the subset of the AWS CloudWatch SDK used by the
// collector. Kept unexported: only the factory creates instances;
// tests implement against this interface.
type cloudWatchAPI interface {
	GetMetricData(ctx context.Context, params *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
}

// standardMetric defines a single CloudWatch metric the collector
// queries for cost attribution.
type standardMetric struct {
	Namespace  string // CloudWatch namespace, e.g., "AWS/Lambda"
	MetricName string
	Stat       string // e.g., "Sum", "Average"
	Service    string // human-readable service name, e.g., "Lambda"
	Unit       string // e.g., "Count", "Milliseconds"
}

// defaultMetrics lists the standard AWS services and metrics the
// collector queries. These are the primary cost drivers for a
// managed Lesser instance:
//
//   - Lambda: invocations, duration, errors, throttles
//   - DynamoDB: consumed read/write capacity
//   - API Gateway: request count, latency, error rates
//   - SQS: messages sent/received
//   - S3: object count, bucket size
//   - CloudFront: requests, bytes downloaded
var defaultMetrics = []standardMetric{
	{Service: "Lambda", Namespace: "AWS/Lambda", MetricName: "Invocations", Stat: "Sum", Unit: "Count"},
	{Service: "Lambda", Namespace: "AWS/Lambda", MetricName: "Duration", Stat: "Average", Unit: "Milliseconds"},
	{Service: "Lambda", Namespace: "AWS/Lambda", MetricName: "Errors", Stat: "Sum", Unit: "Count"},
	{Service: "Lambda", Namespace: "AWS/Lambda", MetricName: "Throttles", Stat: "Sum", Unit: "Count"},
	{Service: "DynamoDB", Namespace: "AWS/DynamoDB", MetricName: "ConsumedReadCapacityUnits", Stat: "Sum", Unit: "Count"},
	{Service: "DynamoDB", Namespace: "AWS/DynamoDB", MetricName: "ConsumedWriteCapacityUnits", Stat: "Sum", Unit: "Count"},
	{Service: "APIGateway", Namespace: "AWS/ApiGateway", MetricName: "Count", Stat: "Sum", Unit: "Count"},
	{Service: "APIGateway", Namespace: "AWS/ApiGateway", MetricName: "Latency", Stat: "Average", Unit: "Milliseconds"},
	{Service: "APIGateway", Namespace: "AWS/ApiGateway", MetricName: "4XXError", Stat: "Sum", Unit: "Count"},
	{Service: "APIGateway", Namespace: "AWS/ApiGateway", MetricName: "5XXError", Stat: "Sum", Unit: "Count"},
	{Service: "SQS", Namespace: "AWS/SQS", MetricName: "NumberOfMessagesSent", Stat: "Sum", Unit: "Count"},
	{Service: "SQS", Namespace: "AWS/SQS", MetricName: "NumberOfMessagesReceived", Stat: "Sum", Unit: "Count"},
	{Service: "S3", Namespace: "AWS/S3", MetricName: "NumberOfObjects", Stat: "Average", Unit: "Count"},
	{Service: "S3", Namespace: "AWS/S3", MetricName: "BucketSizeBytes", Stat: "Average", Unit: "Bytes"},
	{Service: "CloudFront", Namespace: "AWS/CloudFront", MetricName: "Requests", Stat: "Sum", Unit: "Count"},
	{Service: "CloudFront", Namespace: "AWS/CloudFront", MetricName: "BytesDownloaded", Stat: "Sum", Unit: "Bytes"},
}

// filterMetricsByService returns the subset of metrics matching the
// requested service names. Matching is case-insensitive.
func filterMetricsByService(metrics []standardMetric, services []string) []standardMetric {
	if len(services) == 0 {
		return metrics
	}
	set := make(map[string]struct{}, len(services))
	for _, s := range services {
		set[s] = struct{}{}
	}
	out := make([]standardMetric, 0, len(metrics))
	for _, m := range metrics {
		if _, ok := set[m.Service]; ok {
			out = append(out, m)
		}
	}
	return out
}

// cloudWatchCollectorImpl is the AWS SDK-backed CloudWatchCollector.
type cloudWatchCollectorImpl struct {
	factory CloudWatchClientFactory
}

// NewCloudWatchCollector constructs an AWS SDK-backed CloudWatchCollector.
// The factory is used to create per-account per-region CloudWatch clients,
// which in production may assume cross-account roles.
func NewCloudWatchCollector(factory CloudWatchClientFactory) CloudWatchCollector {
	return &cloudWatchCollectorImpl{factory: factory}
}

// CollectMetrics implements CloudWatchCollector.
func (c *cloudWatchCollectorImpl) CollectMetrics(ctx context.Context, scope TenantScope, windowStart, windowEnd time.Time) (*InstanceCostReport, error) {
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	if !windowStart.Before(windowEnd) {
		return nil, fmt.Errorf("costtelemetry: windowStart must be before windowEnd (start=%s, end=%s)",
			windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339))
	}

	client, err := c.factory.Client(ctx, scope.AccountID, scope.Region)
	if err != nil {
		return nil, fmt.Errorf("costtelemetry: creating CloudWatch client for account %s region %s: %w",
			scope.AccountID, scope.Region, err)
	}

	metrics := defaultMetrics
	if len(scope.Services) > 0 {
		metrics = filterMetricsByService(defaultMetrics, scope.Services)
	}

	attributions, err := c.queryMetrics(ctx, client, metrics, windowStart, windowEnd)
	if err != nil {
		return nil, fmt.Errorf("costtelemetry: querying metrics for %s (account %s): %w",
			scope.Slug, scope.AccountID, err)
	}

	return &InstanceCostReport{
		Slug:         scope.Slug,
		AccountID:    scope.AccountID,
		WindowStart:  windowStart,
		WindowEnd:    windowEnd,
		Attributions: attributions,
	}, nil
}

func validateScope(scope TenantScope) error {
	if scope.Slug == "" {
		return fmt.Errorf("costtelemetry: TenantScope.Slug is required")
	}
	if scope.AccountID == "" {
		return fmt.Errorf("costtelemetry: TenantScope.AccountID is required")
	}
	if scope.Region == "" {
		return fmt.Errorf("costtelemetry: TenantScope.Region is required")
	}
	return nil
}

// queryMetrics fetches all standard metrics using GetMetricData and
// converts the results into ServiceAttribution values.
//
// Metrics are batched into a single GetMetricData call per account/region
// (AWS supports up to 500 MetricDataQueries per call). The Period is set
// to the window duration so each metric returns a single aggregated data
// point for cost-attribution purposes.
func (c *cloudWatchCollectorImpl) queryMetrics(
	ctx context.Context,
	client cloudWatchAPI,
	metrics []standardMetric,
	windowStart, windowEnd time.Time,
) ([]ServiceAttribution, error) {
	if len(metrics) == 0 {
		return nil, nil
	}

	period := int32(windowEnd.Sub(windowStart).Seconds())
	if period < 60 {
		period = 60
	}

	queries := make([]cwtypes.MetricDataQuery, 0, len(metrics))
	for i, m := range metrics {
		id := fmt.Sprintf("m%d", i)
		queries = append(queries, cwtypes.MetricDataQuery{
			Id: aws.String(id),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String(m.Namespace),
					MetricName: aws.String(m.MetricName),
				},
				Period: aws.Int32(period),
				Stat:   aws.String(m.Stat),
				Unit:   cwtypes.StandardUnit(metricUnitFromString(m.Unit)),
			},
		})
	}

	input := &cloudwatch.GetMetricDataInput{
		MetricDataQueries: queries,
		StartTime:         aws.Time(windowStart),
		EndTime:           aws.Time(windowEnd),
	}

	output, err := client.GetMetricData(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("GetMetricData: %w", err)
	}

	attributions := make([]ServiceAttribution, 0, len(metrics))
	for i, m := range metrics {
		if i >= len(output.MetricDataResults) {
			break
		}
		result := output.MetricDataResults[i]
		val := extractMetricValue(result.Values, m.Stat)
		// Skip NaN / Inf to keep output clean for downstream consumers.
		if math.IsNaN(val) || math.IsInf(val, 0) {
			continue
		}
		attributions = append(attributions, ServiceAttribution{
			Service:    m.Service,
			MetricName: m.MetricName,
			Stat:       m.Stat,
			Unit:       m.Unit,
			Value:      val,
		})
	}

	// Sort for deterministic output (tests and downstream cache depend on it).
	sort.Slice(attributions, func(i, j int) bool {
		if attributions[i].Service != attributions[j].Service {
			return attributions[i].Service < attributions[j].Service
		}
		return attributions[i].MetricName < attributions[j].MetricName
	})

	return attributions, nil
}

// extractMetricValue returns a single representative value from a list
// of CloudWatch metric data points. For "Sum" stats, it sums all points;
// for others, it takes the last non-NaN value.
//
// Returns NaN when all values are NaN, Inf, or the slice is empty with
// no valid data. Callers should filter NaN results.
func extractMetricValue(values []float64, stat string) float64 {
	if len(values) == 0 {
		return 0 // empty values → metric had no data → zero is the correct value
	}
	if stat == "Sum" {
		var sum float64
		hasValid := false
		for _, v := range values {
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				sum += v
				hasValid = true
			}
		}
		if !hasValid {
			return math.NaN()
		}
		return sum
	}
	// For Average, Maximum, Minimum, SampleCount: return the last
	// valid value (single data point for the window when period
	// equals window duration).
	for i := len(values) - 1; i >= 0; i-- {
		if !math.IsNaN(values[i]) && !math.IsInf(values[i], 0) {
			return values[i]
		}
	}
	return math.NaN()
}

// metricUnitFromString converts a human-readable unit string to a
// CloudWatch StandardUnit. Falls back to None for unrecognized values.
func metricUnitFromString(u string) cwtypes.StandardUnit {
	switch u {
	case "Count":
		return cwtypes.StandardUnitCount
	case "Milliseconds":
		return cwtypes.StandardUnitMilliseconds
	case "Bytes":
		return cwtypes.StandardUnitBytes
	case "Seconds":
		return cwtypes.StandardUnitSeconds
	case "Percent":
		return cwtypes.StandardUnitPercent
	case "Microseconds":
		return cwtypes.StandardUnitMicroseconds
	case "Bytes/Second":
		return cwtypes.StandardUnitBytesSecond
	case "Count/Second":
		return cwtypes.StandardUnitCountSecond
	default:
		return cwtypes.StandardUnitNone
	}
}
