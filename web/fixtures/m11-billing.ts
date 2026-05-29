/**
 * M11 Billing UI Fixture — entry point.
 *
 * Intercepts `window.fetch` to serve realistic mock API responses for all
 * endpoints consumed by Billing.svelte, then mounts the real page component.
 * This entry is ONLY loaded via `web/fixtures/m11-billing.html` for headless
 * PNG capture — it is never imported by any customer portal route.
 *
 * Import order mirrors the real app entrypoints so screenshot evidence
 * reflects the actual Billing.svelte rendering rather than unstyled layout.
 *
 * @license AGPL-3.0-only
 */

/* Design tokens: Greater base + Agent Genesis bridge */
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';

/* Greater Shell CSS */
import 'src/lib/styles/greater/shell.css';

/* Greater host-platform CSS */
import 'src/lib/styles/greater/host-platform.css';

/* M1 foundation primitives (Metric, ProgressBar, Sparkline, etc.) */
import 'src/lib/styles/m1-primitives.css';

/* Host-specific global resets + skin overrides */
import 'src/app.css';

import { mount } from 'svelte';
import BillingPage from 'src/pages/portal/Billing.svelte';

// ── Mock data (realistic, covers May 2026) ───────────────────────────

const now = new Date();
const Y = now.getFullYear();
const M = String(now.getMonth() + 1).padStart(2, '0');
const currentMonth = `${Y}-${M}`;
const daysInMonth = new Date(Y, now.getMonth() + 1, 0).getDate();

/** Build an array of daily cost entries for the current month. */
function buildCostDays(slug: string, baseDaily: number, variance: number): object[] {
	const days: object[] = [];
	for (let d = 1; d <= daysInMonth; d++) {
		const day = String(d).padStart(2, '0');
		const cost = baseDaily + Math.round((Math.random() - 0.5) * variance * 100) / 100;
		days.push({
			date: `${currentMonth}-${day}`,
			day_cost: Math.max(0, cost),
			currency: 'USD',
			entries: [
				{
					date: `${currentMonth}-${day}`,
					service: 'lesser-host',
					cost: Math.max(0, cost),
					currency: 'USD',
					metrics: [
						{ service: 'lesser-host', metric_name: 'UniqueUsers', stat: 'Maximum', unit: 'Count', value: Math.floor(Math.random() * 80) + 20 },
					],
				},
			],
		});
	}
	return days;
}

function makeCostResponse(slug: string, baseDaily: number, variance: number) {
	const days = buildCostDays(slug, baseDaily, variance);
	const total = (days as { day_cost: number }[]).reduce((s, d) => s + d.day_cost, 0);
	return {
		instance_slug: slug,
		from_date: `${currentMonth}-01`,
		to_date: `${currentMonth}-${String(daysInMonth).padStart(2, '0')}`,
		days,
		count: days.length,
		total_cost: Math.round(total * 100) / 100,
		currency: 'USD',
	};
}

const mockInstances = {
	instances: [
		{
			slug: 'my-instance',
			status: 'active',
			provision_status: 'active',
			managed_lesser_domain: 'my-instance.greater.website',
			hosted_region: 'us-east-1',
			lesser_version: 'v2.4.1',
			lesser_body_version: 'v1.3.0',
			translation_enabled: true,
			hosted_previews_enabled: true,
			link_safety_enabled: true,
			renders_enabled: true,
			render_policy: 'all',
			overage_policy: 'block',
			moderation_enabled: true,
			moderation_trigger: 'auto',
			moderation_virality_min: 3,
			ai_enabled: true,
			ai_model_set: 'default',
			ai_batching_mode: 'batch',
			ai_batch_max_items: 10,
			ai_batch_max_total_bytes: 1048576,
			ai_pricing_multiplier_bps: 100,
			ai_max_inflight_jobs: 5,
			created_at: '2025-11-15T10:00:00Z',
			updated_at: '2026-05-20T14:30:00Z',
		},
		{
			slug: 'staging-env',
			status: 'active',
			provision_status: 'active',
			managed_lesser_domain: 'staging-env.greater.website',
			hosted_region: 'eu-west-1',
			lesser_version: 'v2.4.0',
			lesser_body_version: 'v1.2.9',
			translation_enabled: false,
			hosted_previews_enabled: false,
			link_safety_enabled: true,
			renders_enabled: false,
			render_policy: 'none',
			overage_policy: 'allow',
			moderation_enabled: false,
			moderation_trigger: 'none',
			moderation_virality_min: 0,
			ai_enabled: false,
			ai_model_set: '',
			ai_batching_mode: '',
			ai_batch_max_items: 0,
			ai_batch_max_total_bytes: 0,
			ai_pricing_multiplier_bps: 0,
			ai_max_inflight_jobs: 0,
			created_at: '2026-01-10T08:00:00Z',
			updated_at: '2026-05-18T09:15:00Z',
		},
	],
	count: 2,
};

const mockCostMyInstance = makeCostResponse('my-instance', 3.2, 1.5);
const mockCostStaging = makeCostResponse('staging-env', 1.1, 0.6);

const mockBudgetMyInstance = {
	instance_slug: 'my-instance',
	month: currentMonth,
	included_credits: 5000,
	used_credits: 3200,
	remaining_credits: 1800,
	updated_at: '2026-05-28T10:00:00Z',
};

const mockBudgetStaging = {
	instance_slug: 'staging-env',
	month: currentMonth,
	included_credits: 2000,
	used_credits: 850,
	remaining_credits: 1150,
	updated_at: '2026-05-28T09:00:00Z',
};

const mockActivityMyInstance = {
	instance_slug: 'my-instance',
	statuses: 247,
	weeks: 4,
	month: currentMonth,
};

const mockActivityStaging = {
	instance_slug: 'staging-env',
	statuses: 83,
	weeks: 4,
	month: currentMonth,
};

const mockInvoices = {
	invoices: [
		{
			id: 'in_1QxYzA',
			period_start: '2026-05-01',
			period_end: '2026-05-28',
			amount_due: 2450,
			currency: 'usd',
			status: 'paid',
			hosted_invoice_url: 'https://stripe.com/invoice/stub-1',
			invoice_pdf_url: 'https://stripe.com/invoice/stub-1/pdf',
		},
		{
			id: 'in_1QwXyB',
			period_start: '2026-04-01',
			period_end: '2026-04-30',
			amount_due: 2180,
			currency: 'usd',
			status: 'paid',
			hosted_invoice_url: 'https://stripe.com/invoice/stub-2',
			invoice_pdf_url: 'https://stripe.com/invoice/stub-2/pdf',
		},
	],
	count: 2,
};

const mockPaymentMethod = {
	payment_method: {
		id: 'pm_1AbCdEfG',
		type: 'card',
		brand: 'visa',
		last4: '4242',
		exp_month: 12,
		exp_year: 2028,
		status: 'active',
	},
};

// ── Fetch interceptor ────────────────────────────────────────────────

/**
 * Match URL patterns against mock endpoints and return canned JSON.
 * Uses path matching to avoid coupling to origin/host.
 */
function mockFetch(input: RequestInfo | URL, _init?: RequestInit): Promise<Response> {
	void _init;
	const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;

	// Helper to create a JSON response
	const json = (body: unknown) =>
		new Response(JSON.stringify(body), {
			status: 200,
			headers: { 'content-type': 'application/json' },
		});

	if (url.includes('/portal/instances/my-instance/cost')) return Promise.resolve(json(mockCostMyInstance));
	if (url.includes('/portal/instances/staging-env/cost')) return Promise.resolve(json(mockCostStaging));
	if (url.includes('/portal/instances/my-instance/budgets/')) return Promise.resolve(json(mockBudgetMyInstance));
	if (url.includes('/portal/instances/staging-env/budgets/')) return Promise.resolve(json(mockBudgetStaging));
	if (url.includes('/portal/instances/my-instance/activity')) return Promise.resolve(json(mockActivityMyInstance));
	if (url.includes('/portal/instances/staging-env/activity')) return Promise.resolve(json(mockActivityStaging));
	if (url.includes('/portal/billing/invoices')) return Promise.resolve(json(mockInvoices));
	if (url.includes('/portal/billing/payment-method')) return Promise.resolve(json(mockPaymentMethod));
	if (url.includes('/portal/instances') && !url.includes('/cost') && !url.includes('/budgets') && !url.includes('/activity'))
		return Promise.resolve(json(mockInstances));

	// Fallback: return empty JSON with 404 for unmatched routes
	return Promise.resolve(
		new Response(JSON.stringify({ message: 'not found in fixture' }), {
			status: 404,
			headers: { 'content-type': 'application/json' },
		}),
	);
}

// Override global fetch before any API module imports it
window.fetch = mockFetch as typeof fetch;

// ── Mount Billing.svelte directly (not through PortalShell) ──────────

const app = mount(BillingPage, {
	target: document.getElementById('fixture-root')!,
	props: { token: 'mock-fixture-token' },
});

export default app;
