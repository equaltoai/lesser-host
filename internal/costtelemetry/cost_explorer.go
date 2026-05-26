package costtelemetry

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

// costExplorerAPI is the subset of the AWS Cost Explorer SDK used by
// the collector. Kept unexported: only the factory creates instances;
// tests implement against this interface.
type costExplorerAPI interface {
	GetCostAndUsage(ctx context.Context, params *costexplorer.GetCostAndUsageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error)
}

// CostExplorerClientFactory creates Cost Explorer API clients scoped
// to a given account and region.
//
// Cost Explorer is typically queried from the management / payer account
// with a LINKED_ACCOUNT filter rather than directly from the linked
// account. Production implementations must translate the accountID
// parameter into the appropriate Cost Explorer filter when the worker
// runs from the host (payer) account. The interface does not prescribe
// the implementation — the accountID and region parameters carry tenant
// dimensions, and the factory's Client method is the seam for test
// injection.
//
// M3.8 watchpoint: CloudWatch no-dimension queries are treated as
// per-tenant-account aggregates because the tenant account is the
// isolation boundary. Cost Explorer similarly treats all queries as
// per-tenant-account, with the factory / filter translation absorbing
// the payer-account-vs-linked-account wiring difference. Do not
// introduce cross-tenant aggregation.
type CostExplorerClientFactory interface {
	// Client returns a Cost Explorer API client for the given account
	// and region. Production wiring must apply tenant scoping (e.g.,
	// LINKED_ACCOUNT filter) when the worker runs from the host account.
	Client(ctx context.Context, accountID, region string) (costExplorerAPI, error)
}

// CostExplorerCollector queries AWS Cost Explorer for billing-grain
// cost data and reconciles it with CloudWatch metric attributions.
// This is the M3.9 test seam: tests inject mocks; production injects
// the AWS SDK-backed implementation.
type CostExplorerCollector interface {
	// CollectCosts queries Cost Explorer for billing-grain cost data
	// for the given tenant scope over the specified time window.
	// The returned CostExplorerResult carries explicit tenant dimensions
	// (Slug, AccountID) so downstream steps cannot accidentally merge
	// data across tenant accounts.
	CollectCosts(ctx context.Context, scope TenantScope, windowStart, windowEnd time.Time) (*CostExplorerResult, error)

	// Reconcile merges Cost Explorer billing data with CloudWatch metric
	// attributions into a per-instance per-day reconciled report.
	//
	// This is a pure function — it makes no AWS calls. Call CollectCosts
	// first, then pass the result here along with the CloudWatch report.
	//
	// The report preserves tenant scoping and contains no PII, tenant
	// content, raw instance keys, secrets, or request bodies.
	Reconcile(ceResult *CostExplorerResult, cwReport *InstanceCostReport) (*ReconciledCostReport, error)
}

// costBreakdownMetric is the Cost Explorer metric name used for
// billing-grain cost queries.
const costBreakdownMetric = "UnblendedCost"

// CostBreakdown represents a single AWS service's cost from Cost Explorer.
type CostBreakdown struct {
	Service  string  `json:"service"`  // e.g., "Amazon Lambda"
	Amount   float64 `json:"amount"`   // cost in USD (or the queried currency)
	Currency string  `json:"currency"` // e.g., "USD"
}

// CostExplorerResult is the raw billing-grain cost data from Cost Explorer
// for a tenant. Contains no PII, tenant content, raw instance keys, secrets,
// or request bodies.
type CostExplorerResult struct {
	Slug        string          `json:"slug"`
	AccountID   string          `json:"account_id"`
	WindowStart time.Time       `json:"window_start"`
	WindowEnd   time.Time       `json:"window_end"`
	Costs       []CostBreakdown `json:"costs"`
	TotalCost   float64         `json:"total_cost"`
	Currency    string          `json:"currency"`
}

// ReconciledCostEntry combines Cost Explorer billing data with CloudWatch
// metric attributions for a single service dimension on a single day.
// Safe for future customer exposure.
type ReconciledCostEntry struct {
	Date     string               `json:"date"`    // YYYY-MM-DD
	Service  string               `json:"service"` // normalized (CW-style), e.g., "Lambda"
	Cost     float64              `json:"cost"`    // from Cost Explorer (USD)
	Currency string               `json:"currency"`
	Metrics  []ServiceAttribution `json:"metrics,omitempty"` // CloudWatch attributions
}

// ReconciledCostReport is the M3.9 output: aggregate per-instance per-day
// cost data that reconciles Cost Explorer billing with CloudWatch metrics.
// Safe for caching (M3.10) and customer exposure (M3.11+).
type ReconciledCostReport struct {
	Slug        string                `json:"slug"`
	AccountID   string                `json:"account_id"`
	WindowStart time.Time             `json:"window_start"`
	WindowEnd   time.Time             `json:"window_end"`
	Entries     []ReconciledCostEntry `json:"entries"`
	TotalCost   float64               `json:"total_cost"`
	Currency    string                `json:"currency"`
}

// costExplorerCollectorImpl is the AWS SDK-backed CostExplorerCollector.
type costExplorerCollectorImpl struct {
	factory CostExplorerClientFactory
}

// NewCostExplorerCollector constructs an AWS SDK-backed CostExplorerCollector.
// The factory is used to create per-account clients, which in production
// may apply a LINKED_ACCOUNT filter when the worker runs from the host
// (payer) account.
func NewCostExplorerCollector(factory CostExplorerClientFactory) CostExplorerCollector {
	return &costExplorerCollectorImpl{factory: factory}
}

// CollectCosts implements CostExplorerCollector.
func (c *costExplorerCollectorImpl) CollectCosts(ctx context.Context, scope TenantScope, windowStart, windowEnd time.Time) (*CostExplorerResult, error) {
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	if err := validateWindow(windowStart, windowEnd); err != nil {
		return nil, err
	}

	client, err := c.factory.Client(ctx, scope.AccountID, scope.Region)
	if err != nil {
		return nil, fmt.Errorf("costtelemetry: creating Cost Explorer client for account %s region %s: %w",
			scope.AccountID, scope.Region, err)
	}

	// Normalize the window to full-day boundaries. Cost Explorer uses
	// YYYY-MM-DD strings with exclusive end, so we anchor at midnight.
	startDate := windowStart.UTC().Truncate(24 * time.Hour)
	endDate := ceilingDay(windowEnd.UTC())

	results, err := c.queryCostExplorer(ctx, client, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("costtelemetry: querying Cost Explorer for %s (account %s): %w",
			scope.Slug, scope.AccountID, err)
	}

	return buildCostExplorerResult(scope, windowStart, windowEnd, results)
}

// queryCostExplorer calls GetCostAndUsage with DAILY granularity grouped
// by SERVICE, paginating when the response carries a continuation token.
func (c *costExplorerCollectorImpl) queryCostExplorer(
	ctx context.Context,
	client costExplorerAPI,
	startDate, endDate time.Time,
) ([]cetypes.ResultByTime, error) {
	input := &costexplorer.GetCostAndUsageInput{
		Granularity: cetypes.GranularityDaily,
		Metrics:     []string{costBreakdownMetric},
		TimePeriod: &cetypes.DateInterval{
			Start: aws.String(startDate.Format("2006-01-02")),
			End:   aws.String(endDate.Format("2006-01-02")),
		},
		GroupBy: []cetypes.GroupDefinition{
			{
				Type: cetypes.GroupDefinitionTypeDimension,
				Key:  aws.String(string(cetypes.DimensionService)),
			},
		},
	}

	var allResults []cetypes.ResultByTime
	for {
		output, err := client.GetCostAndUsage(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("GetCostAndUsage: %w", err)
		}
		allResults = append(allResults, output.ResultsByTime...)
		if output.NextPageToken == nil {
			break
		}
		input.NextPageToken = output.NextPageToken
	}
	return allResults, nil
}

// buildCostExplorerResult converts paginated Cost Explorer results into
// a CostExplorerResult carrying a flat list of per-service cost breakdowns
// across the full window.
func buildCostExplorerResult(
	scope TenantScope,
	windowStart, windowEnd time.Time,
	results []cetypes.ResultByTime,
) (*CostExplorerResult, error) {
	costMap := make(map[string]*CostBreakdown) // keyed by CE service name
	var totalCost float64
	currency := "USD"

	for _, rbt := range results {
		// Aggregate per-period totals (groups may or may not sum to
		// the period total due to rounding / ungrouped line items).
		for _, group := range rbt.Groups {
			ceService := ""
			if len(group.Keys) > 0 {
				ceService = group.Keys[0]
			}
			if ceService == "" {
				continue
			}
			am, cur := extractMetricValueCE(group.Metrics[costBreakdownMetric])
			if cur != "" {
				currency = cur
			}
			entry, ok := costMap[ceService]
			if !ok {
				entry = &CostBreakdown{
					Service:  ceService,
					Currency: currency,
				}
				costMap[ceService] = entry
			}
			entry.Amount += am
			totalCost += am
		}
	}

	costs := make([]CostBreakdown, 0, len(costMap))
	for _, cb := range costMap {
		// Round to meaningful precision (6 decimal places for
		// sub-cent fractional costs common in AWS billing).
		cb.Amount = math.Round(cb.Amount*1e6) / 1e6
		// Omit zero-cost entries — they represent nil/unparseable
		// amounts or services with no meaningful cost, and add noise.
		if cb.Amount == 0 {
			continue
		}
		costs = append(costs, *cb)
	}
	totalCost = math.Round(totalCost*1e6) / 1e6

	// Sort for deterministic output.
	sort.Slice(costs, func(i, j int) bool {
		return costs[i].Service < costs[j].Service
	})

	return &CostExplorerResult{
		Slug:        scope.Slug,
		AccountID:   scope.AccountID,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
		Costs:       costs,
		TotalCost:   totalCost,
		Currency:    currency,
	}, nil
}

// Reconcile implements CostExplorerCollector.
func (c *costExplorerCollectorImpl) Reconcile(ceResult *CostExplorerResult, cwReport *InstanceCostReport) (*ReconciledCostReport, error) {
	if ceResult == nil {
		return nil, fmt.Errorf("costtelemetry: ceResult is nil")
	}
	if cwReport == nil {
		return nil, fmt.Errorf("costtelemetry: cwReport is nil")
	}
	if ceResult.Slug != cwReport.Slug {
		return nil, fmt.Errorf("costtelemetry: slug mismatch between CE (%q) and CW (%q)",
			ceResult.Slug, cwReport.Slug)
	}
	if ceResult.AccountID != cwReport.AccountID {
		return nil, fmt.Errorf("costtelemetry: account mismatch between CE (%q) and CW (%q)",
			ceResult.AccountID, cwReport.AccountID)
	}

	// Build a lookup of CW attributions by normalized service name.
	cwByService := buildCWAttributionLookup(cwReport.Attributions)

	entries := make([]ReconciledCostEntry, 0, len(ceResult.Costs))

	// For the reconciliation, CE data is per-day (DAILY granularity)
	// but CW data is a single window aggregate. Each daily entry
	// carries the full window's CW metric attributions as context.
	// Future M3.11 can split CW metrics per-day if needed.
	for _, cb := range ceResult.Costs {
		normService := mapCEToCWService(cb.Service)

		var metrics []ServiceAttribution
		if attrs, ok := cwByService[normService]; ok {
			metrics = attrs
		}

		entries = append(entries, ReconciledCostEntry{
			Service:  normService,
			Cost:     cb.Amount,
			Currency: cb.Currency,
			Metrics:  metrics,
		})
	}

	// Sort entries for deterministic output.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Service != entries[j].Service {
			return entries[i].Service < entries[j].Service
		}
		return entries[i].Cost < entries[j].Cost
	})

	// Use the tighter of the two windows.
	windowStart := ceResult.WindowStart
	if cwReport.WindowStart.After(windowStart) {
		windowStart = cwReport.WindowStart
	}
	windowEnd := ceResult.WindowEnd
	if cwReport.WindowEnd.Before(windowEnd) {
		windowEnd = cwReport.WindowEnd
	}

	return &ReconciledCostReport{
		Slug:        ceResult.Slug,
		AccountID:   ceResult.AccountID,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
		Entries:     entries,
		TotalCost:   ceResult.TotalCost,
		Currency:    ceResult.Currency,
	}, nil
}

// buildCWAttributionLookup indexes CloudWatch attributions by normalized
// service name for O(1) reconciliation lookups.
func buildCWAttributionLookup(attributions []ServiceAttribution) map[string][]ServiceAttribution {
	if len(attributions) == 0 {
		return nil
	}
	out := make(map[string][]ServiceAttribution)
	for _, a := range attributions {
		out[a.Service] = append(out[a.Service], a)
	}
	return out
}

// validateWindow validates the time window for Cost Explorer queries.
func validateWindow(windowStart, windowEnd time.Time) error {
	if !windowStart.Before(windowEnd) {
		return fmt.Errorf("costtelemetry: windowStart must be before windowEnd (start=%s, end=%s)",
			windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339))
	}
	return nil
}

// ceilingDay returns the start of the next UTC day after t.
// Used to normalize Cost Explorer end-dates which are exclusive.
func ceilingDay(t time.Time) time.Time {
	dayStart := t.Truncate(24 * time.Hour)
	if t.Equal(dayStart) {
		// Already at midnight — return the same value (empty range
		// is caught by validateWindow).
		return t
	}
	return dayStart.Add(24 * time.Hour)
}

// extractMetricValueCE extracts a float64 amount and currency string
// from a Cost Explorer MetricValue. Returns (0, "") when the Amount
// is nil or unparseable.
func extractMetricValueCE(mv cetypes.MetricValue) (float64, string) {
	currency := ""
	if mv.Unit != nil {
		currency = *mv.Unit
	}
	if mv.Amount == nil {
		return 0, currency
	}
	val, err := strconv.ParseFloat(*mv.Amount, 64)
	if err != nil {
		return 0, currency
	}
	return val, currency
}

// mapCEToCWService maps a Cost Explorer service name (e.g., "Amazon Lambda")
// to the normalized service name used by CloudWatch attributions
// (e.g., "Lambda"). Returns the original input when no mapping exists.
//
// The mapping is the reconciliation bridge: CE returns display names
// while CW uses short identifiers. This is a best-effort mapping;
// AWS may add or rename service display names over time. Unknown
// services pass through unchanged.
func mapCEToCWService(ceService string) string {
	if normalized, ok := ceToCWService[ceService]; ok {
		return normalized
	}
	return ceService
}

// ceToCWService maps Cost Explorer service display names to CloudWatch
// normalized service names. This is the reconciliation bridge between
// the two AWS APIs.
//
// NOTE: AWS may add service names or change display labels. This table
// is the maintained set for costtelemetry. Missing entries cause the CE
// service name to pass through unchanged.
var ceToCWService = map[string]string{
	"Amazon API Gateway":                 "APIGateway",
	"Amazon CloudFront":                  "CloudFront",
	"Amazon CloudWatch":                  "CloudWatch",
	"Amazon Cognito":                     "Cognito",
	"Amazon DynamoDB":                    "DynamoDB",
	"Amazon EC2":                         "EC2",
	"Amazon Elastic Compute Cloud":       "EC2",
	"Amazon Kinesis":                     "Kinesis",
	"Amazon Route 53":                    "Route53",
	"Amazon Simple Notification Service": "SNS",
	"Amazon Simple Queue Service":        "SQS",
	"Amazon Simple Storage Service":      "S3",
	"AWS Certificate Manager":            "ACM",
	"AWS CloudFront":                     "CloudFront",
	"AWS CodeBuild":                      "CodeBuild",
	"AWS Data Transfer":                  "DataTransfer",
	"AWS Elastic Load Balancing":         "ELB",
	"AWS Key Management Service":         "KMS",
	"AWS Lambda":                         "Lambda",
	"AWS Route 53":                       "Route53",
	"AWS Secrets Manager":                "SecretsManager",
	"AWS Step Functions":                 "StepFunctions",
	"EC2 - Other":                        "EC2",
}
