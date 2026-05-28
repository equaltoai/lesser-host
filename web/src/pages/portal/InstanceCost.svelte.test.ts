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

	it('formats dollars with micro-precision for sub-cent values', () => {
		// M8: dollar formatting from cost telemetry.
		expect(source).toContain('function formatDollars(');
		expect(source).toContain("if (n < 0.01) return `$${n.toFixed(6)}`;");
		expect(source).toContain("return `$${n.toFixed(2)}`;");
	});

	it('pins displayMonth at load time for budget/usage data consistency', () => {
		// Pinning prevents UTC-midnight-on-the-1st straddle skew between
		// the header label and the data the user is looking at.
		expect(source).toContain('let displayMonth = $state<string>(currentMonthUTC());');
		expect(source).toContain('displayMonth = month;');

		// The template must not call currentMonthUTC() at render time.
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).not.toContain('currentMonthUTC()');
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
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('loading={loading || costLoading}');
		expect(template).toContain('loadingBehavior="prepend"');
	});

	it('does not clear budget/summary before the await in loadAll', () => {
		// REGRESSION GUARD: clearing budget/summary at the start of loadAll
		// makes the page-level spinner gate `loading && !budget && !summary`
		// win on every refresh, replacing the Refresh button subtree.
		const fnStart = source.indexOf('async function loadAll()');
		expect(fnStart).toBeGreaterThan(0);
		const tryStart = source.indexOf('try {', fnStart);
		expect(tryStart).toBeGreaterThan(fnStart);

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
		// prior data to show.
		expect(source).toContain('{#if loading && !budget && !summary}');
	});

	// ── M8 cost UI redesign tests (#542) ──────────────────────────────

	it('imports M8 primitives: CostGauge, Sparkline, ProgressBar, Metric', () => {
		expect(source).toContain("import CostGauge from 'src/lib/components/primitives/CostGauge.svelte';");
		expect(source).toContain("import Sparkline from 'src/lib/components/primitives/Sparkline.svelte';");
		expect(source).toContain("import ProgressBar from 'src/lib/components/primitives/ProgressBar.svelte';");
		expect(source).toContain("import Metric from 'src/lib/components/primitives/Metric.svelte';");
	});

	it('derives compute GB-sec from entry.metrics[] by unit pattern', () => {
		expect(source).toContain('function deriveComputeGbSec(');
		expect(source).toContain("unit.includes('gb-sec')");
		expect(source).toContain("unit.includes('gb*s')");
		expect(source).toContain("unit.includes('gbs')");
	});

	it('derives egress GB from entry.metrics[] by service and unit pattern', () => {
		expect(source).toContain('function deriveEgressGb(');
		expect(source).toContain("unit.includes('gb')");
		expect(source).toContain("unit.includes('byte')");
	});

	it('renders header metric cards: MTD spend, Compute, Egress', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('MTD spend vs budget');
		expect(template).toContain('Compute');
		expect(template).toContain('Egress');
		expect(template).toContain('cost__metrics');
	});

	it('renders "Where the dollars go" breakdown table', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('Where the dollars go');
		expect(template).toContain('cost__table');
	});

	it('renders budget alarms panel with disabled switches', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('Budget alarms');
		expect(template).toContain('cost__alarms');
		// All alarm toggles must be disabled with honest tooltip about persistence.
		expect(template).toContain('Budget alarm persistence is not yet available');
	});

	it('renders unavailable states for compute/egress when metrics absent', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('Not available');
		expect(template).toContain('no GB-sec metrics in cost data');
		expect(template).toContain('no egress metrics in cost data');
	});

	it('retains categoryForService for Egress/Lambda/DynamoDB categorization', () => {
		expect(source).toContain('function categoryLabel(');
		expect(source).toContain("return 'Lambda'");
		expect(source).toContain("return 'DynamoDB'");
		expect(source).toContain("return 'Egress'");
		expect(source).toContain("lower === 'datatransfer'");
		expect(source).toContain("lower === 'cloudfront'");
	});

	it('calls portalGetInstanceCost for real-time cost telemetry', () => {
		expect(source).toContain('import { portalGetBudgetMonth, portalGetUsageSummary, portalGetInstanceCost }');
		expect(source).toContain('portalGetInstanceCost(token, slug)');
	});

	it('handles 401 on cost endpoint in separate catch block', () => {
		// M8: 401 from the cost endpoint must also trigger logout + redirect.
		const first401 = source.indexOf(').status === 401');
		expect(first401).toBeGreaterThan(0);
		const second401 = source.indexOf(').status === 401', first401 + 1);
		expect(second401).toBeGreaterThan(first401);
	});

	it('removed M3 cost telemetry card and daily breakdown in favor of M8 panels', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		// Old M3 patterns replaced by M8 panels.
		expect(template).not.toContain('Real-time cost telemetry');
		expect(template).not.toContain('instance-cost__service-grid');
		expect(template).not.toContain('instance-cost__daily-list');
		expect(template).not.toContain('Daily breakdown');
	});
});
