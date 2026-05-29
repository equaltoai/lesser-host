/**
 * M15 Trust UI Fixture — entry point.
 *
 * Intercepts `window.fetch` to serve realistic mock API responses for all
 * endpoints consumed by PortalTrust (Trust.svelte), then mounts the real
 * page component. This entry is ONLY loaded via `web/fixtures/m15-trust.html`
 * for headless PNG capture — it is never imported by any customer portal route.
 *
 * Import order mirrors the real app entrypoints so screenshot evidence
 * reflects the actual Trust.svelte rendering rather than unstyled layout.
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
import PortalTrust from 'src/pages/portal/Trust.svelte';

// ── Mock data: two portal instances with realistic trust data ─────────

const mockInstances = {
	instances: [
		{
			slug: 'equaltoai',
			status: 'ok',
			hosted_region: 'us-east-1',
			lesser_version: 'v2.4.1',
			translation_enabled: false,
			hosted_previews_enabled: true,
			link_safety_enabled: true,
			renders_enabled: true,
			render_policy: '',
			overage_policy: '',
			moderation_enabled: true,
			moderation_trigger: '',
			moderation_virality_min: 0,
			ai_enabled: true,
			ai_model_set: 'claude-sonnet-4-6',
			ai_batching_mode: '',
			ai_batch_max_items: 0,
			ai_batch_max_total_bytes: 0,
			ai_pricing_multiplier_bps: 0,
			ai_max_inflight_jobs: 0,
			created_at: '2025-10-04T14:30:00Z',
		},
		{
			slug: 'maeve-studio',
			status: 'ok',
			hosted_region: 'eu-west-1',
			lesser_version: 'v2.4.1',
			translation_enabled: false,
			hosted_previews_enabled: true,
			link_safety_enabled: false,
			renders_enabled: true,
			render_policy: '',
			overage_policy: '',
			moderation_enabled: true,
			moderation_trigger: '',
			moderation_virality_min: 0,
			ai_enabled: true,
			ai_model_set: 'gpt-5.1',
			ai_batching_mode: '',
			ai_batch_max_items: 0,
			ai_batch_max_total_bytes: 0,
			ai_pricing_multiplier_bps: 0,
			ai_max_inflight_jobs: 0,
			created_at: '2025-12-09T10:00:00Z',
		},
	],
	count: 2,
};

const equaltoaiTrust = {
	instance_slug: 'equaltoai',
	federation: {
		reachable: 91,
		warning: 6,
		severed: 1,
		peers: [
			{ domain: 'guild.greater.website', status: 'reachable' as const, follower_count: 143, last_seen: '2026-05-29T08:15:00Z' },
			{ domain: 'maeve-studio.greater.website', status: 'reachable' as const, follower_count: 87, last_seen: '2026-05-29T07:44:00Z' },
			{ domain: 'press-room.greater.website', status: 'warning' as const, last_seen: '2026-05-28T12:10:00Z' },
			{ domain: 'dev-sandbox.greater.website', status: 'reachable' as const, follower_count: 12, last_fetch: '2026-05-29T10:30:00Z' },
			{ domain: 'partner-net.greater.website', status: 'severed' as const, last_seen: '2026-05-15T03:22:00Z' },
			{ domain: 'ops-hub.greater.website', status: 'reachable' as const, follower_count: 210, last_seen: '2026-05-29T09:01:00Z' },
			{ domain: 'data-lake.greater.website', status: 'warning' as const, last_fetch: '2026-05-29T10:30:00Z' },
			{ domain: 'staging-env.greater.website', status: 'reachable' as const, last_fetch: '2026-05-29T10:30:00Z' },
		],
	},
	signatures: {
		window_hours: 24,
		total_failures: 14,
		by_source: [
			{ source: '0x1a2b3c4d5e6f7890abcdef1234567890abcdef12', failures: 5 },
			{ source: '0x2b3c4d5e6f7890abcdef1234567890abcdef1234', failures: 3 },
			{ source: '0x3c4d5e6f7890abcdef1234567890abcdef123456', failures: 4 },
			{ source: '0x4d5e6f7890abcdef1234567890abcdef12345678', failures: 2 },
		],
	},
	queue_depth: {
		series: [
			{ timestamp: '2026-05-29T06:00:00Z', depth: 12 },
			{ timestamp: '2026-05-29T06:30:00Z', depth: 18 },
			{ timestamp: '2026-05-29T07:00:00Z', depth: 24 },
			{ timestamp: '2026-05-29T07:30:00Z', depth: 15 },
			{ timestamp: '2026-05-29T08:00:00Z', depth: 8 },
			{ timestamp: '2026-05-29T08:30:00Z', depth: 11 },
			{ timestamp: '2026-05-29T09:00:00Z', depth: 20 },
			{ timestamp: '2026-05-29T09:30:00Z', depth: 7 },
		],
	},
	trust_score: {
		score: 78.3,
		formula: 'composite_weighted_average',
		dimensions: {
			operational: 82.0,
			attestation: 91.0,
			social: 68.0,
			economic: 75.0,
			integrity: 88.0,
		},
		source: 'lesser-host:soul_agent_reputation',
	},
	vouches: {
		items: [
			{ peer: '0x3c4d5e6f7890abcdef1234567890abcdef123456', strength: 1.0, type: 'endorsement', created_at: '2026-05-27T09:15:00Z' },
			{ peer: '0x4d5e6f7890abcdef1234567890abcdef12345678', strength: 1.0, type: 'endorsement', created_at: '2026-05-26T14:20:00Z' },
			{ peer: '0x5e6f7890abcdef1234567890abcdef1234567890', strength: 1.0, type: 'endorsement', created_at: '2026-05-25T08:00:00Z' },
		],
		count: 3,
	},
};

const maeveStudioTrust = {
	instance_slug: 'maeve-studio',
	federation: {
		reachable: 22,
		warning: 1,
		severed: 0,
		peers: [
			{ domain: 'equaltoai.greater.website', status: 'reachable' as const, follower_count: 310, last_seen: '2026-05-29T09:30:00Z' },
			{ domain: 'guild.greater.website', status: 'reachable' as const, last_seen: '2026-05-29T08:15:00Z' },
			{ domain: 'press-room.greater.website', status: 'warning' as const, follower_count: 43, last_seen: '2026-05-28T12:10:00Z' },
		],
	},
	signatures: {
		window_hours: 24,
		total_failures: 7,
		by_source: [
			{ source: '0x5e6f7890abcdef1234567890abcdef1234567890', failures: 4 },
			{ source: '0x6f7890abcdef1234567890abcdef123456789012', failures: 3 },
		],
	},
	queue_depth: {
		series: [
			{ timestamp: '2026-05-29T06:00:00Z', depth: 5 },
			{ timestamp: '2026-05-29T06:30:00Z', depth: 3 },
			{ timestamp: '2026-05-29T07:00:00Z', depth: 7 },
			{ timestamp: '2026-05-29T07:30:00Z', depth: 2 },
		],
	},
	trust_score: {
		score: 64.7,
		formula: 'composite_weighted_average',
		dimensions: {
			operational: 71.0,
			attestation: 85.0,
			social: 52.0,
			economic: 58.0,
			integrity: 74.0,
		},
		source: 'lesser-host:soul_agent_reputation',
	},
	vouches: {
		items: [
			{ peer: '0x1a2b3c4d5e6f7890abcdef1234567890abcdef12', strength: 1.0, type: 'endorsement', created_at: '2026-05-28T10:00:00Z' },
		],
		count: 1,
	},
};

const trustBySlug: Record<string, typeof equaltoaiTrust> = {
	equaltoai: equaltoaiTrust,
	'maeve-studio': maeveStudioTrust,
};

// ── Fetch interceptor ────────────────────────────────────────────────

function mockFetch(input: RequestInfo | URL, _init?: RequestInit): Promise<Response> {
	void _init;
	const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;

	const json = (body: unknown) =>
		new Response(JSON.stringify(body), {
			status: 200,
			headers: { 'content-type': 'application/json' },
		});

	// Portal instances list
	if (url === '/api/v1/portal/instances' || url.endsWith('/api/v1/portal/instances')) {
		return Promise.resolve(json(mockInstances));
	}

	// Per-instance trust data
	const trustMatch = url.match(/\/api\/v1\/portal\/instances\/([^/]+)\/trust\/data/);
	if (trustMatch) {
		const slug = decodeURIComponent(trustMatch[1]);
		const td = trustBySlug[slug];
		if (td) return Promise.resolve(json(td));
		return Promise.resolve(
			new Response(JSON.stringify({ message: 'instance not found' }), {
				status: 404,
				headers: { 'content-type': 'application/json' },
			}),
		);
	}

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

// ── Mount PortalTrust (Trust.svelte) directly ─────────────────────────

const app = mount(PortalTrust, {
	target: document.getElementById('fixture-root')!,
	props: { token: 'mock-fixture-token' },
});

export default app;
