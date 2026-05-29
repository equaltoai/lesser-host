/**
 * M13 Souls Top-Level UI Fixture — entry point.
 *
 * Intercepts `window.fetch` to serve realistic mock API responses for all
 * endpoints consumed by Souls.svelte, then mounts the real page component.
 * This entry is ONLY loaded via `web/fixtures/m13-souls.html` for headless
 * PNG capture — it is never imported by any customer portal route.
 *
 * Import order mirrors the real app entrypoints so screenshot evidence
 * reflects the actual Souls.svelte rendering rather than unstyled layout.
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
import SoulsPage from 'src/pages/portal/Souls.svelte';

// ── Mock data: realistic soul roster covering all stage variants ─────

const mockSouls = {
	agents: [
		{
			agent: {
				agent_id: '0x1a2b3c4d5e6f7890abcdef1234567890abcdef12',
				domain: 'equaltoai.greater.website',
				local_id: 'maeve',
				wallet: '0xabcdef1234567890abcdef1234567890abcdef12',
				token_id: '42',
				status: 'active',
				lifecycle_status: 'active',
				self_description_version: 3,
				anchor_assurance: { state: 'immutable_onchain' as const, source: 'onchain_receipt' as const, capability_gate: false as const, mutable: false, revocable: false },
				minted_at: '2025-10-04T14:30:00Z',
				updated_at: '2026-05-27T09:15:00Z',
				capabilities: ['text-generation', 'federation'],
				avatar: {
					image: undefined,
				},
			},
			reputation: {
				agent_id: '0x1a2b3c4d5e6f7890abcdef1234567890abcdef12',
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
			},
		},
		{
			agent: {
				agent_id: '0x2b3c4d5e6f7890abcdef1234567890abcdef1234',
				domain: 'equaltoai.greater.website',
				local_id: 'atlas',
				wallet: '0xbcdef1234567890abcdef1234567890abcdef1234',
				token_id: '43',
				status: 'active',
				lifecycle_status: 'active',
				self_description_version: 2,
				anchor_assurance: { state: 'immutable_onchain' as const, source: 'onchain_receipt' as const, capability_gate: false as const, mutable: false, revocable: false },
				minted_at: '2025-12-09T10:00:00Z',
				updated_at: '2026-05-26T18:30:00Z',
				capabilities: ['text-generation', 'image-generation'],
				avatar: {
					image: undefined,
				},
			},
			reputation: {
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
			},
		},
		{
			agent: {
				agent_id: '0x3c4d5e6f7890abcdef1234567890abcdef123456',
				domain: 'maeve-studio.greater.website',
				local_id: 'mae',
				wallet: '0xcdef1234567890abcdef1234567890abcdef123456',
				status: 'active',
				lifecycle_status: 'active',
				self_description_version: 1,
				anchor_assurance: { state: 'immutable_onchain' as const, source: 'onchain_receipt' as const, capability_gate: false as const, mutable: false, revocable: false },
				minted_at: '2026-02-10T08:00:00Z',
				updated_at: '2026-05-25T12:00:00Z',
				capabilities: ['text-generation'],
				avatar: {
					image: undefined,
				},
			},
			reputation: {
				agent_id: '0x3c4d5e6f7890abcdef1234567890abcdef123456',
				block_ref: 21458500,
				composite: 0.541,
				economic: 0.45,
				social: 0.52,
				validation: 0.61,
				trust: 0.67,
				integrity: 0.59,
				tips_received: 31.0,
				interactions: 87,
				validations_passed: 14,
				endorsements: 3,
				flags: 0,
				updated_at: '2026-05-25T12:00:00Z',
			},
		},
		{
			agent: {
				agent_id: '0x4d5e6f7890abcdef1234567890abcdef12345678',
				domain: 'guild.greater.website',
				local_id: 'ribbon',
				wallet: '0xdef1234567890abcdef1234567890abcdef12345678',
				token_id: '45',
				status: 'active',
				lifecycle_status: 'active',
				self_description_version: 2,
				anchor_assurance: { state: 'immutable_onchain' as const, source: 'onchain_receipt' as const, capability_gate: false as const, mutable: false, revocable: false },
				minted_at: '2026-03-12T16:00:00Z',
				updated_at: '2026-05-24T20:00:00Z',
				capabilities: ['text-generation', 'voice'],
				avatar: {
					image: undefined,
				},
			},
			reputation: {
				agent_id: '0x4d5e6f7890abcdef1234567890abcdef12345678',
				block_ref: 21458400,
				composite: 0.612,
				economic: 0.58,
				social: 0.63,
				validation: 0.59,
				trust: 0.71,
				integrity: 0.64,
				tips_received: 58.0,
				interactions: 142,
				validations_passed: 22,
				endorsements: 6,
				flags: 0,
				updated_at: '2026-05-24T20:00:00Z',
			},
		},
		{
			agent: {
				agent_id: '0x5e6f7890abcdef1234567890abcdef1234567890',
				domain: 'equaltoai.greater.website',
				local_id: 'iris',
				wallet: '0xef1234567890abcdef1234567890abcdef1234567890',
				status: 'active',
				lifecycle_status: 'active',
				self_description_version: 0,
				anchor_assurance: { state: 'hosted_offchain' as const, source: 'host_record' as const, capability_gate: false as const, mutable: true, revocable: true },
				minted_at: '2026-05-19T11:00:00Z',
				updated_at: '2026-05-27T08:00:00Z',
				capabilities: ['text-generation'],
				avatar: {
					image: undefined,
				},
			},
			reputation: undefined,
		},
		{
			agent: {
				agent_id: '0x6f7890abcdef1234567890abcdef123456789012',
				domain: 'press-room.greater.website',
				local_id: 'drone-04',
				wallet: '0xf1234567890abcdef1234567890abcdef123456789012',
				status: 'suspended',
				lifecycle_status: 'suspended',
				anchor_assurance: { state: 'hosted_offchain' as const, source: 'host_record' as const, capability_gate: false as const, mutable: true, revocable: true },
				updated_at: '2026-05-20T00:00:00Z',
				capabilities: ['text-generation'],
				avatar: {
					image: undefined,
				},
			},
			reputation: {
				agent_id: '0x6f7890abcdef1234567890abcdef123456789012',
				block_ref: 21457000,
				composite: 0.312,
				economic: 0.28,
				social: 0.34,
				validation: 0.29,
				trust: 0.31,
				integrity: 0.33,
				tips_received: 0,
				interactions: 23,
				validations_passed: 3,
				endorsements: 1,
				flags: 4,
				updated_at: '2026-05-20T00:00:00Z',
			},
		},
		{
			agent: {
				agent_id: '0x7890abcdef1234567890abcdef12345678901234',
				domain: 'guild.greater.website',
				local_id: 'hex',
				wallet: '0x1234567890abcdef1234567890abcdef12345678901234',
				status: 'pending',
				lifecycle_status: 'pending',
				anchor_assurance: { state: 'hosted_offchain' as const, source: 'host_record' as const, capability_gate: false as const, mutable: true, revocable: true },
				updated_at: '2026-05-22T15:00:00Z',
				capabilities: ['text-generation'],
				avatar: {
					image: undefined,
				},
			},
			reputation: undefined,
		},
	],
	count: 7,
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

	if (url.includes('/api/v1/soul/agents/mine')) return Promise.resolve(json(mockSouls));

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

// ── Mount Souls.svelte directly (not through PortalShell) ────────────

const app = mount(SoulsPage, {
	target: document.getElementById('fixture-root')!,
	props: { token: 'mock-fixture-token' },
});

export default app;
