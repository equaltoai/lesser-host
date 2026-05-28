/**
 * M8 Instance Cost UI Fixture — mock portalUsage API.
 *
 * Returns realistic cost, budget, and usage telemetry data so the
 * InstanceCost component renders in its fully-loaded M8 design state
 * without contacting the backend. This mock is aliased into the
 * `src/lib/api/portalUsage` import path by the M8 fixture Vite config
 * and is NEVER loaded by any customer portal route.
 *
 * The mock data is crafted to exercise every visual surface:
 *   - MTD spend header card (CostGauge + ProgressBar at ~47% usage)
 *   - Compute GB-sec header card with Metric + Sparkline
 *   - Egress GB header card with Metric
 *   - "Where the dollars go" breakdown table (5 services)
 *   - Budget alarms panel (all disabled buttons — no mock needed)
 *   - Monthly credit summary with request/cache stats
 *
 * @license AGPL-3.0-only
 */

export interface BudgetMonthResponse {
	instance_slug: string;
	month: string;
	included_credits: number;
	used_credits: number;
	remaining_credits?: number;
	updated_at?: string;
}

export interface ListBudgetsResponse {
	budgets: BudgetMonthResponse[];
	count: number;
}

export interface UsageLedgerEntry {
	id: string;
	instance_slug: string;
	month: string;
	module: string;
	target?: string;
	cached: boolean;
	reason?: string;
	request_id?: string;
	requested_credits: number;
	list_credits?: number;
	pricing_multiplier_bps?: number;
	debited_credits: number;
	included_debited_credits: number;
	overage_debited_credits: number;
	billing_type: string;
	actor_uri?: string;
	object_uri?: string;
	content_hash?: string;
	links_hash?: string;
	created_at: string;
}

export interface ListUsageResponse {
	entries: UsageLedgerEntry[];
	count: number;
}

export interface UsageSummaryResponse {
	instance_slug: string;
	month: string;
	requests: number;
	cache_hits: number;
	cache_misses: number;
	cache_hit_rate: number;
	list_credits: number;
	requested_credits: number;
	debited_credits: number;
	discount_credits: number;
	included_credits?: number;
	used_credits?: number;
	remaining_credits?: number;
}

export interface CostAttributionMetric {
	service: string;
	metric_name: string;
	stat: string;
	unit: string;
	value: number;
}

export interface CostServiceEntry {
	date: string;
	service: string;
	cost: number;
	currency: string;
	metrics?: CostAttributionMetric[];
}

export interface CostDayEntry {
	date: string;
	day_cost: number;
	currency: string;
	entries: CostServiceEntry[];
}

export interface PortalCostResponse {
	instance_slug: string;
	from_date: string;
	to_date: string;
	days: CostDayEntry[];
	count: number;
	total_cost: number;
	currency: string;
}

// ---------------------------------------------------------------------------
// Fixture data
// ---------------------------------------------------------------------------

const FIXTURE_BUDGET: BudgetMonthResponse = {
	instance_slug: 'simulacrum',
	month: '2026-05',
	included_credits: 5_000,
	used_credits: 2_347,
	remaining_credits: 2_653,
	updated_at: '2026-05-28T12:00:00Z',
};

const FIXTURE_SUMMARY: UsageSummaryResponse = {
	instance_slug: 'simulacrum',
	month: '2026-05',
	requests: 142_857,
	cache_hits: 98_765,
	cache_misses: 44_092,
	cache_hit_rate: 0.6913,
	list_credits: 1_250,
	requested_credits: 3_250,
	debited_credits: 2_347,
	discount_credits: 120,
	included_credits: 5_000,
	used_credits: 2_347,
	remaining_credits: 2_653,
};

/** Helper: create one day of cost telemetry entries. */
function makeDay(date: string, entries: CostServiceEntry[]): CostDayEntry {
	const dayCost = entries.reduce((sum, e) => sum + e.cost, 0);
	return { date, day_cost: Math.round(dayCost * 100) / 100, currency: 'USD', entries };
}

/** Helper: create a service cost entry with optional metrics. */
function makeEntry(service: string, cost: number, metrics?: CostAttributionMetric[]): CostServiceEntry {
	return { date: '', service, cost, currency: 'USD', metrics };
}

const FIXTURE_COST: PortalCostResponse = {
	instance_slug: 'simulacrum',
	from_date: '2026-05-22',
	to_date: '2026-05-28',
	days: [
		makeDay('2026-05-22', [
			makeEntry('Lambda', 0.78, [
				{ service: 'Lambda', metric_name: 'Duration', stat: 'Sum', unit: 'GB-sec', value: 2_512_000 },
				{ service: 'Lambda', metric_name: 'Invocations', stat: 'Sum', unit: 'Count', value: 85_000 },
			]),
			makeEntry('DynamoDB', 0.45, [
				{ service: 'DynamoDB', metric_name: 'ConsumedReadCapacityUnits', stat: 'Sum', unit: 'Count', value: 12_400 },
			]),
			makeEntry('DataTransfer', 0.32, [
				{ service: 'DataTransfer', metric_name: 'BytesOut', stat: 'Sum', unit: 'Bytes', value: 15_200_000_000 },
			]),
			makeEntry('S3', 0.08),
			makeEntry('ApiGateway', 0.22, [
				{ service: 'ApiGateway', metric_name: 'Count', stat: 'Sum', unit: 'Count', value: 42_000 },
			]),
		]),
		makeDay('2026-05-23', [
			makeEntry('Lambda', 0.65, [
				{ service: 'Lambda', metric_name: 'Duration', stat: 'Sum', unit: 'GB*s', value: 2_100_000 },
				{ service: 'Lambda', metric_name: 'Invocations', stat: 'Sum', unit: 'Count', value: 72_000 },
			]),
			makeEntry('DynamoDB', 0.52, [
				{ service: 'DynamoDB', metric_name: 'ConsumedReadCapacityUnits', stat: 'Sum', unit: 'Count', value: 14_200 },
			]),
			makeEntry('CloudFront', 0.38, [
				{ service: 'CloudFront', metric_name: 'BytesOut', stat: 'Sum', unit: 'GB', value: 18.5 },
			]),
			makeEntry('S3', 0.06),
			makeEntry('ApiGateway', 0.19),
		]),
		makeDay('2026-05-24', [
			makeEntry('Lambda', 0.82, [
				{ service: 'Lambda', metric_name: 'Duration', stat: 'Sum', unit: 'GBs', value: 2_780_000 },
				{ service: 'Lambda', metric_name: 'Invocations', stat: 'Sum', unit: 'Count', value: 91_000 },
			]),
			makeEntry('DynamoDB', 0.38, [
				{ service: 'DynamoDB', metric_name: 'ConsumedReadCapacityUnits', stat: 'Sum', unit: 'Count', value: 9_800 },
			]),
			makeEntry('DataTransfer', 0.55, [
				{ service: 'DataTransfer', metric_name: 'BytesOut', stat: 'Sum', unit: 'Bytes', value: 26_400_000_000 },
			]),
			makeEntry('S3', 0.11),
			makeEntry('ApiGateway', 0.25),
		]),
		makeDay('2026-05-25', [
			makeEntry('Lambda', 0.71, [
				{ service: 'Lambda', metric_name: 'Duration', stat: 'Sum', unit: 'GB-sec', value: 2_350_000 },
				{ service: 'Lambda', metric_name: 'Invocations', stat: 'Sum', unit: 'Count', value: 78_500 },
			]),
			makeEntry('DynamoDB', 0.49, [
				{ service: 'DynamoDB', metric_name: 'ConsumedReadCapacityUnits', stat: 'Sum', unit: 'Count', value: 13_100 },
			]),
			makeEntry('CloudFront', 0.41, [
				{ service: 'CloudFront', metric_name: 'BytesOut', stat: 'Sum', unit: 'GB', value: 20.1 },
			]),
			makeEntry('S3', 0.09),
			makeEntry('ApiGateway', 0.21),
		]),
		makeDay('2026-05-26', [
			makeEntry('Lambda', 0.58, [
				{ service: 'Lambda', metric_name: 'Duration', stat: 'Sum', unit: 'GB-sec', value: 1_890_000 },
				{ service: 'Lambda', metric_name: 'Invocations', stat: 'Sum', unit: 'Count', value: 64_000 },
			]),
			makeEntry('DynamoDB', 0.55, [
				{ service: 'DynamoDB', metric_name: 'ConsumedReadCapacityUnits', stat: 'Sum', unit: 'Count', value: 15_600 },
			]),
			makeEntry('DataTransfer', 0.48, [
				{ service: 'DataTransfer', metric_name: 'BytesOut', stat: 'Sum', unit: 'Bytes', value: 22_800_000_000 },
			]),
			makeEntry('S3', 0.07),
			makeEntry('ApiGateway', 0.18),
		]),
		makeDay('2026-05-27', [
			makeEntry('Lambda', 0.93, [
				{ service: 'Lambda', metric_name: 'Duration', stat: 'Sum', unit: 'GB-sec', value: 3_120_000 },
				{ service: 'Lambda', metric_name: 'Invocations', stat: 'Sum', unit: 'Count', value: 104_000 },
			]),
			makeEntry('DynamoDB', 0.41, [
				{ service: 'DynamoDB', metric_name: 'ConsumedReadCapacityUnits', stat: 'Sum', unit: 'Count', value: 10_500 },
			]),
			makeEntry('CloudFront', 0.44, [
				{ service: 'CloudFront', metric_name: 'BytesOut', stat: 'Sum', unit: 'GB', value: 21.7 },
			]),
			makeEntry('S3', 0.10),
			makeEntry('ApiGateway', 0.27),
		]),
		makeDay('2026-05-28', [
			makeEntry('Lambda', 0.37, [
				{ service: 'Lambda', metric_name: 'Duration', stat: 'Sum', unit: 'GB-sec', value: 1_210_000 },
				{ service: 'Lambda', metric_name: 'Invocations', stat: 'Sum', unit: 'Count', value: 41_000 },
			]),
			makeEntry('DynamoDB', 0.26, [
				{ service: 'DynamoDB', metric_name: 'ConsumedReadCapacityUnits', stat: 'Sum', unit: 'Count', value: 6_800 },
			]),
			makeEntry('DataTransfer', 0.21, [
				{ service: 'DataTransfer', metric_name: 'BytesOut', stat: 'Sum', unit: 'Bytes', value: 10_100_000_000 },
			]),
			makeEntry('S3', 0.04),
			makeEntry('ApiGateway', 0.12),
		]),
	],
	count: 7,
	total_cost: 12.47,
	currency: 'USD',
};

// ---------------------------------------------------------------------------
// Mock API functions
// ---------------------------------------------------------------------------

export function portalListBudgets(_token?: string, _slug?: string): Promise<ListBudgetsResponse> {
	void _token; void _slug;
	return Promise.resolve({ budgets: [FIXTURE_BUDGET], count: 1 });
}

export function portalGetBudgetMonth(_token?: string, _slug?: string, _month?: string): Promise<BudgetMonthResponse> {
	void _token; void _slug; void _month;
	return Promise.resolve({ ...FIXTURE_BUDGET });
}

export function portalSetBudgetMonth(
	_token?: string, _slug?: string, _month?: string, _includedCredits?: number,
): Promise<BudgetMonthResponse> {
	void _token; void _slug; void _month; void _includedCredits;
	return Promise.resolve({ ...FIXTURE_BUDGET });
}

export function portalListUsage(_token?: string, _slug?: string, _month?: string): Promise<ListUsageResponse> {
	void _token; void _slug; void _month;
	return Promise.resolve({ entries: [], count: 0 });
}

export function portalGetUsageSummary(_token?: string, _slug?: string, _month?: string): Promise<UsageSummaryResponse> {
	void _token; void _slug; void _month;
	return Promise.resolve({ ...FIXTURE_SUMMARY });
}

export function portalGetInstanceCost(
	_token?: string, _slug?: string, _from?: string, _to?: string,
): Promise<PortalCostResponse> {
	void _token; void _slug; void _from; void _to;
	return Promise.resolve({ ...FIXTURE_COST });
}
