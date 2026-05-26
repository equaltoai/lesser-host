/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

const source = readFileSync(join(process.cwd(), 'src/pages/portal/InstanceCost.svelte'), 'utf8');

describe('InstanceCost cost & usage tab', () => {
	it('prefers server-computed remaining_credits, falling back to floor(included - used)', () => {
		// Server-field precedence (M1.12 / PR #506 forward-compat).
		expect(source).toContain(
			'const server = budget?.remaining_credits ?? summary?.remaining_credits;',
		);
		expect(source).toContain(
			"if (typeof server === 'number' && Number.isFinite(server)) return server;",
		);

		// Client fallback floors at zero so transient overruns never render as negative.
		expect(source).toContain('return Math.max(0, included - used);');
	});

	it('formats cache_hit_rate as a 0.0–1.0 ratio scaled to a percentage', () => {
		// Wire contract: cache_hit_rate is a ratio, not a 0–100 percentage.
		// `p * 1000 / 10` yields one decimal place after multiplying by 100.
		expect(source).toContain('return `${Math.round(p * 1000) / 10}%`;');
		expect(source).toContain("if (!Number.isFinite(p)) return '—';");
	});

	it('pins displayMonth at load time so header, data, and refresh stay consistent', () => {
		// Pinning prevents UTC-midnight-on-the-1st straddle skew between
		// the header label and the data the user is looking at.
		expect(source).toContain('let displayMonth = $state<string>(currentMonthUTC());');
		expect(source).toContain('displayMonth = month;');
		expect(source).toContain('Current month ({displayMonth})');

		// The template must not call currentMonthUTC() at render time.
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).not.toContain('currentMonthUTC()');
	});

	it('flags overrun (used > included) as danger status on the Remaining credits stat', () => {
		// Once M1.12 server-side remaining_credits can express overrun semantics,
		// the Remaining credits card must surface danger rather than warning when
		// used exceeds included. ('danger' is the StatCardStatus value, not 'error'.)
		expect(source).toContain("used > included");
		expect(source).toContain("? 'danger'");
	});

	it('redirects to /login on 401 from either budget or usage endpoint', () => {
		expect(source).toContain("(err as Partial<ApiError>).status === 401");
		expect(source).toContain('await logout();');
		expect(source).toContain("navigate('/login');");
	});

	it('surfaces inflight-refresh state via Button loading prop', () => {
		// The Refresh button must convey inflight state during a refresh after
		// initial load — when data is already present, the page-level spinner
		// gate is closed, so feedback has to come from the button itself.
		// greater-components Button exposes `loading` + `loadingBehavior` for
		// this; the consumer just has to wire them.
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('loading={loading || costLoading}');
		expect(template).toContain('loadingBehavior="prepend"');
	});

	it('does not clear budget/summary before the await in loadAll', () => {
		// REGRESSION GUARD: clearing budget/summary at the start of loadAll
		// makes the page-level spinner gate `loading && !budget && !summary`
		// win on every refresh, replacing the Refresh button subtree — at
		// which point Button's `loading` prop is no longer in the DOM and
		// the inflight-refresh affordance is dead.
		//
		// Reviewer caught this on PR #507 head 91cc28f (review 4359812637).
		// The fix is to keep prior `budget` / `summary` values during the
		// refresh round-trip; the page-level spinner then fires only on
		// first load (when both are still null), and the Refresh button's
		// inline `loading` affordance carries the rest.
		const fnStart = source.indexOf('async function loadAll()');
		expect(fnStart).toBeGreaterThan(0);
		const tryStart = source.indexOf('try {', fnStart);
		expect(tryStart).toBeGreaterThan(fnStart);

		// Region between `async function loadAll()` and the opening `try {`
		// must not contain any `budget = null` or `summary = null`
		// assignment. The reassignments inside `try` (on success) and in
		// callers outside loadAll are still permitted.
		const preludeRegion = source.slice(fnStart, tryStart);
		expect(preludeRegion).not.toMatch(/\bbudget\s*=\s*null\b/);
		expect(preludeRegion).not.toMatch(/\bsummary\s*=\s*null\b/);

		// `loading = true` must still appear in the prelude so the
		// inflight state is visible to the template.
		expect(preludeRegion).toContain('loading = true');
	});

	it('reserves the page-level spinner branch for the no-prior-data case', () => {
		// The template's loading gate must AND on `!budget && !summary` so
		// that the page-level spinner only takes over when there is no
		// prior data to show. During a refresh after a successful initial
		// load, `budget` and `summary` are non-null and the `{:else}`
		// branch (containing the Refresh button with its inline loading
		// affordance) keeps the user oriented.
		expect(source).toContain('{#if loading && !budget && !summary}');
	});

	// ── M3 cost telemetry wiring tests (#456) ────────────────────────────

	it('imports and calls portalGetInstanceCost for real-time cost telemetry', () => {
		// M3.12: the page must request the cost endpoint.
		expect(source).toContain('import { portalGetBudgetMonth, portalGetUsageSummary, portalGetInstanceCost }');
		expect(source).toContain('portalGetInstanceCost(token, slug)');
	});

	it('does not render the old TODO(M3) placeholder or coming-soon empty state', () => {
		// M3.12: the "coming soon" empty state and TODO(M3) static breakdown
		// must be replaced with live cost data. The doc comment may reference
		// TODO(M3) historically, but the template must not contain any TODO(M3)
		// directives or coming-soon placeholder text.
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).not.toContain('TODO(M3)');
		expect(template).not.toContain('Real-time cost telemetry coming soon');
		expect(template).not.toContain('cost__coming-soon-grid');
		expect(template).not.toContain('cost__coming-soon-item');
	});

	it('renders per-category cost breakdown grid from live data', () => {
		// M3.12: the service grid must exist, aggregating entries by category
		// via the categorization layer (categoryForService).
		expect(source).toContain('aggregateByCategory(cost.days)');
		expect(source).toContain('instance-cost__service-grid');
		expect(source).toContain('instance-cost__service-item');
	});

	it('categorizes Lambda, DynamoDB, and Egress services with explicit labels', () => {
		// M3.12 #456: the categorization layer must map services to
		// user-facing category labels. Removing the categoryForService
		// function or its Egress mapping must fail this test.
		expect(source).toContain('function categoryForService(');
		expect(source).toContain("return 'Lambda'");
		expect(source).toContain("return 'DynamoDB'");
		expect(source).toContain("return 'Egress'");

		// DataTransfer and CloudFront must both map to Egress.
		expect(source).toContain("lower === 'datatransfer'");
		expect(source).toContain("lower === 'cloudfront'");

		// Egress-like patterns must be caught.
		expect(source).toContain("lower.includes('data transfer')");
		expect(source).toContain("lower.includes('egress')");
	});

	it('renders daily cost breakdown from live data', () => {
		// M3.12: daily breakdown section must exist.
		expect(source).toContain('Daily breakdown');
		expect(source).toContain('instance-cost__daily-list');
		expect(source).toContain('instance-cost__daily-row');
	});

	it('handles 401 on cost endpoint', () => {
		// M3.12: 401 from the cost endpoint must also trigger logout + redirect.
		// The cost-specific catch block must contain the 401 check.
		expect(source).toContain('portalGetInstanceCost(token, slug)');

		// Verify there's a second 401 check in the cost catch block
		// (the first is in the budget/summary catch).
		const first401 = source.indexOf(').status === 401');
		expect(first401).toBeGreaterThan(0);
		const second401 = source.indexOf(').status === 401', first401 + 1);
		expect(second401).toBeGreaterThan(first401);
	});
});
