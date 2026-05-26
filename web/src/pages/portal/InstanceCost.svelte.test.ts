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

	it('marks the per-Lambda / Dynamo / egress breakdown for M3 replacement', () => {
		// Anchor the deferral to the M3 milestone so the static placeholder
		// is not left as a visual artifact once live telemetry lands.
		expect(source).toContain('TODO(M3)');
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
		expect(template).toContain('loading={loading}');
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
});
