/**
 * M14 Soul Detail UI Fixture — entry point.
 *
 * Intercepts `window.fetch` to serve realistic mock API responses for all
 * endpoints consumed by SoulDetail.svelte, then mounts the real page component.
 * This entry is ONLY loaded via `web/fixtures/m14-soul-detail.html` for headless
 * PNG capture — it is never imported by any customer portal route.
 *
 * Import order mirrors the real app entrypoints so screenshot evidence
 * reflects the actual SoulDetail.svelte rendering rather than unstyled layout.
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
import SoulDetailPage from 'src/pages/portal/SoulDetail.svelte';

// ── Target agent ─────────────────────────────────────────────────────

const TARGET_AGENT_ID = '0x1a2b3c4d5e6f7890abcdef1234567890abcdef12';
const FIXTURE_TOKEN = 'mock-fixture-token-for-m14';

// ── Mock data: portal soul roster (authorization gate) ───────────────

const maeveIdentity = {
	agent_id: TARGET_AGENT_ID,
	domain: 'equaltoai.greater.website',
	local_id: 'maeve',
	wallet: '0xabcdef1234567890abcdef1234567890abcdef12',
	token_id: '42',
	status: 'active',
	lifecycle_status: 'active',
	self_description_version: 3,
	anchor_assurance: {
		state: 'immutable_onchain' as const,
		source: 'onchain_receipt' as const,
		capability_gate: false as const,
		mutable: false,
		revocable: false,
	},
	minted_at: '2025-10-04T14:30:00Z',
	updated_at: '2026-05-27T09:15:00Z',
	capabilities: ['text-generation', 'federation'],
	avatar: {
		image: undefined,
	},
};

const maeveReputation = {
	agent_id: TARGET_AGENT_ID,
	block_ref: 21458700,
	composite: 0.847,
	economic: 0.92,
	social: 0.78,
	validation: 0.85,
	trust: 0.91,
	integrity: 0.88,
	tips_received: 184.5,
	interactions: 312,
	validations_passed: 47,
	endorsements: 12,
	flags: 0,
	updated_at: '2026-05-27T09:15:00Z',
};

const maeveRosterItem = {
	agent: { ...maeveIdentity },
	reputation: { ...maeveReputation },
	instance: { slug: 'equaltoai', domain: 'dev.equaltoai.greater.website' },
	lesser_agent: {
		username: 'maeve',
		display_name: 'Maeve',
		agent_type: 'assistant',
		agent_version: 'anthropic:claude-sonnet-4-6',
		status: 'loaded' as const,
		source: 'lesser:/api/v1/agents/maeve',
	},
	tips: {
		received: 184,
		period: 'all_time' as const,
		label: 'Tip events · All time',
		source: 'lesser-host:soul_agent_reputation',
	},
	anchor_assurance: {
		state: 'immutable_onchain' as const,
		source: 'onchain_receipt' as const,
		capability_gate: false as const,
		mutable: false,
		revocable: false,
	},
};

// Additional roster items for variety (not matched to target agentId)
const atlasIdentity = {
	agent_id: '0x2b3c4d5e6f7890abcdef1234567890abcdef1234',
	domain: 'equaltoai.greater.website',
	local_id: 'atlas',
	wallet: '0xbcdef1234567890abcdef1234567890abcdef1234',
	token_id: '43',
	status: 'active',
	lifecycle_status: 'active',
	self_description_version: 2,
	anchor_assurance: {
		state: 'immutable_onchain' as const,
		source: 'onchain_receipt' as const,
		capability_gate: false as const,
		mutable: false,
		revocable: false,
	},
	minted_at: '2025-12-09T10:00:00Z',
	updated_at: '2026-05-26T18:30:00Z',
	capabilities: ['text-generation', 'image-generation'],
	avatar: { image: undefined },
};

const atlasReputation = {
	agent_id: '0x2b3c4d5e6f7890abcdef1234567890abcdef1234',
	block_ref: 21458650,
	composite: 0.723,
	economic: 0.68,
	social: 0.71,
	validation: 0.76,
	trust: 0.82,
	integrity: 0.74,
	tips_received: 72.0,
	interactions: 198,
	validations_passed: 31,
	endorsements: 8,
	flags: 1,
	updated_at: '2026-05-26T18:30:00Z',
};

const atlasRosterItem = {
	agent: { ...atlasIdentity },
	reputation: { ...atlasReputation },
	instance: { slug: 'equaltoai', domain: 'dev.equaltoai.greater.website' },
	lesser_agent: {
		username: 'atlas',
		display_name: 'Atlas',
		agent_type: 'assistant',
		agent_version: 'openai:gpt-5.1',
		status: 'loaded' as const,
		source: 'lesser:/api/v1/agents/atlas',
	},
	tips: {
		received: 72,
		period: 'all_time' as const,
		label: 'Tip events · All time',
		source: 'lesser-host:soul_agent_reputation',
	},
	anchor_assurance: {
		state: 'immutable_onchain' as const,
		source: 'onchain_receipt' as const,
		capability_gate: false as const,
		mutable: false,
		revocable: false,
	},
};

const mockRoster = {
	souls: [maeveRosterItem, atlasRosterItem],
	count: 2,
};

// ── Mock data: agent detail (soulPublicGetAgent) ─────────────────────

const mockAgentDetail = {
	version: '1',
	agent: {
		...maeveIdentity,
		// anchor_assurance already present on maeveIdentity
	},
	reputation: { ...maeveReputation },
};

// ── Mock data: continuity (soulPublicGetContinuity) ──────────────────

function daysAgo(n: number): string {
	const d = new Date(Date.now() - n * 86_400_000);
	return d.toISOString();
}

const mockContinuity = {
	version: '1',
	entries: [
		// Today — 3 signals
		{
			agent_id: TARGET_AGENT_ID,
			type: 'heartbeat',
			summary: 'Scheduled heartbeat check passed',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(0.02),
		},
		{
			agent_id: TARGET_AGENT_ID,
			type: 'anchor_refresh',
			summary: 'Anchor signature refreshed on-chain',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(0.1),
		},
		{
			agent_id: TARGET_AGENT_ID,
			type: 'capability_check',
			summary: 'Capability attestation renewed: text-generation, federation',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(0.3),
		},
		// 1 day ago — 2 signals
		{
			agent_id: TARGET_AGENT_ID,
			type: 'heartbeat',
			summary: 'Scheduled heartbeat check passed',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(1.1),
		},
		{
			agent_id: TARGET_AGENT_ID,
			type: 'boundary_verification',
			summary: 'Operational boundary verification: all clear',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(1.5),
		},
		// 2 days ago — 5 signals
		{
			agent_id: TARGET_AGENT_ID,
			type: 'heartbeat',
			summary: 'Scheduled heartbeat check passed',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(2.0),
		},
		{
			agent_id: TARGET_AGENT_ID,
			type: 'interaction',
			summary: 'Processed 14 federated interactions',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(2.2),
		},
		{
			agent_id: TARGET_AGENT_ID,
			type: 'tip_event',
			summary: 'Tip received from 0x...def12: 2.5 ETH',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(2.4),
		},
		{
			agent_id: TARGET_AGENT_ID,
			type: 'anchor_refresh',
			summary: 'Anchor signature refreshed on-chain',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(2.6),
		},
		{
			agent_id: TARGET_AGENT_ID,
			type: 'capability_check',
			summary: 'Capability attestation renewed',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(2.8),
		},
		// 3 days ago — 0 signals (gap day)
		// 4 days ago — 1 signal
		{
			agent_id: TARGET_AGENT_ID,
			type: 'heartbeat',
			summary: 'Scheduled heartbeat check passed',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(4.3),
		},
		// 5 days ago — 2 signals
		{
			agent_id: TARGET_AGENT_ID,
			type: 'heartbeat',
			summary: 'Scheduled heartbeat check passed',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(5.1),
		},
		{
			agent_id: TARGET_AGENT_ID,
			type: 'tip_event',
			summary: 'Tip received from 0x...78901: 1.0 ETH',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(5.7),
		},
		// 6 days ago — 1 signal
		{
			agent_id: TARGET_AGENT_ID,
			type: 'heartbeat',
			summary: 'Scheduled heartbeat check passed',
			recovery: undefined,
			references: undefined,
			signature: undefined,
			timestamp: daysAgo(6.4),
		},
	],
	count: 14,
	has_more: false,
};

// ── Mock data: comm activity (soulAgentListCommActivity) ──────────────

function minsAgo(n: number): string {
	const d = new Date(Date.now() - n * 60_000);
	return d.toISOString();
}

function hoursAgo(n: number): string {
	return minsAgo(n * 60);
}

const mockCommActivity = {
	version: '1',
	activities: [
		{
			agent_id: TARGET_AGENT_ID,
			activity_id: 'act-001',
			channel_type: 'email' as const,
			direction: 'inbound' as const,
			counterparty: 'alice@example.com',
			action: 'received',
			subject: 'Q2 roadmap review',
			preview: 'Hey Maeve, can we schedule a review of the Q2 roadmap this week?',
			status: 'accepted' as const,
			timestamp: minsAgo(12),
		},
		{
			agent_id: TARGET_AGENT_ID,
			activity_id: 'act-002',
			channel_type: 'email' as const,
			direction: 'outbound' as const,
			counterparty: 'alice@example.com',
			action: 'replied',
			subject: 'Re: Q2 roadmap review',
			preview: 'Sure! How about Thursday at 2pm? I have the latest priorities ready.',
			status: 'sent' as const,
			timestamp: minsAgo(28),
		},
		{
			agent_id: TARGET_AGENT_ID,
			activity_id: 'act-003',
			channel_type: 'sms' as const,
			direction: 'inbound' as const,
			counterparty: '+1-555-0199',
			action: 'received',
			subject: undefined,
			preview: 'Status update on project Aurora?',
			status: 'delivered' as const,
			timestamp: hoursAgo(1.3),
		},
		{
			agent_id: TARGET_AGENT_ID,
			activity_id: 'act-004',
			channel_type: 'sms' as const,
			direction: 'outbound' as const,
			counterparty: '+1-555-0199',
			action: 'replied',
			subject: undefined,
			preview: 'Project Aurora is on track. Milestone 3 complete, moving to M4.',
			status: 'delivered' as const,
			timestamp: hoursAgo(1.5),
		},
		{
			agent_id: TARGET_AGENT_ID,
			activity_id: 'act-005',
			channel_type: 'email' as const,
			direction: 'inbound' as const,
			counterparty: 'bob@partner.org',
			action: 'received',
			subject: 'Federation handshake request',
			preview: 'Requesting federation approval for guild.greater.website.',
			status: 'accepted' as const,
			timestamp: hoursAgo(3.2),
		},
		{
			agent_id: TARGET_AGENT_ID,
			activity_id: 'act-006',
			channel_type: 'email' as const,
			direction: 'outbound' as const,
			counterparty: 'bob@partner.org',
			action: 'replied',
			subject: 'Re: Federation handshake request',
			preview: 'Approved. Federation with guild.greater.website is now active.',
			status: 'sent' as const,
			timestamp: hoursAgo(3.5),
		},
		{
			agent_id: TARGET_AGENT_ID,
			activity_id: 'act-007',
			channel_type: 'voice' as const,
			direction: 'inbound' as const,
			counterparty: '+1-555-0147',
			action: 'received',
			subject: undefined,
			preview: 'Voicemail: "Call me back about the deployment schedule"',
			status: 'queued' as const,
			timestamp: hoursAgo(8.7),
		},
		{
			agent_id: TARGET_AGENT_ID,
			activity_id: 'act-008',
			channel_type: 'email' as const,
			direction: 'inbound' as const,
			counterparty: 'ops@equaltoai.com',
			action: 'received',
			subject: 'Weekly instance health digest',
			preview: 'All instances healthy. Uptime 99.97%. No incidents.',
			status: 'accepted' as const,
			timestamp: hoursAgo(22.5),
		},
		{
			agent_id: TARGET_AGENT_ID,
			activity_id: 'act-009',
			channel_type: 'email' as const,
			direction: 'outbound' as const,
			counterparty: 'ops@equaltoai.com',
			action: 'replied',
			subject: 'Re: Weekly instance health digest',
			preview: 'Acknowledged. Continuity signals are green across the board.',
			status: 'sent' as const,
			timestamp: hoursAgo(23.0),
		},
		{
			agent_id: TARGET_AGENT_ID,
			activity_id: 'act-010',
			channel_type: 'sms' as const,
			direction: 'inbound' as const,
			counterparty: '+1-555-0163',
			action: 'received',
			subject: undefined,
			preview: 'Confirming tip of 5.0 ETH for the Q2 sprint completion.',
			status: 'delivered' as const,
			timestamp: daysAgo(1.2),
		},
	],
	count: 10,
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

	// Portal soul roster (authorization gate)
	if (url.includes('/api/v1/portal/souls/roster')) return Promise.resolve(json(mockRoster));

	// Agent detail (public, no token)
	if (url.includes('/api/v1/soul/agents/') && !url.includes('/continuity') && !url.includes('/comm/activity')) {
		// Extract agentId from URL
		const match = url.match(/\/api\/v1\/soul\/agents\/([^/?]+)/);
		const reqAgentId = match ? decodeURIComponent(match[1]) : '';
		if (reqAgentId === TARGET_AGENT_ID) {
			return Promise.resolve(json(mockAgentDetail));
		}
		return Promise.resolve(
			new Response(JSON.stringify({ message: 'agent not found' }), {
				status: 404,
				headers: { 'content-type': 'application/json' },
			}),
		);
	}

	// Continuity
	if (url.includes('/continuity')) return Promise.resolve(json(mockContinuity));

	// Comm activity
	if (url.includes('/comm/activity')) return Promise.resolve(json(mockCommActivity));

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

// ── Mount SoulDetail.svelte directly ─────────────────────────────────

const app = mount(SoulDetailPage, {
	target: document.getElementById('fixture-root')!,
	props: { token: FIXTURE_TOKEN, agentId: TARGET_AGENT_ID },
});

export default app;
