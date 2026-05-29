/**
 * M15 Trust UI contract tests — Issue #573 real-data rendering.
 *
 * Route compatibility, CSP safety, attestation route preservation,
 * real DTO field rendering, honest empty states, and sparkline truthfulness.
 *
 * @license AGPL-3.0-only
 */

/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

import { mount, unmount } from 'svelte';
import { tick } from 'svelte';

import PortalTrust from './Trust.svelte';

// ── Source-text tests (no DOM) ────────────────────────────────────────

const source = readFileSync(
	join(process.cwd(), 'src/pages/portal/Trust.svelte'),
	'utf8',
);

const appSource = readFileSync(
	join(process.cwd(), 'src/App.svelte'),
	'utf8',
);

describe('M15 Trust UI — source-level contracts', () => {
	describe('CSP safety (strict no-inline)', () => {
		it('contains no inline style= attribute', () => {
			const styleAttrPattern = /\bstyle\s*=\s*"/g;
			const matches = source.match(styleAttrPattern);
			if (matches) {
				for (const m of matches) {
					expect(m).toBe('');
				}
			}
			expect(true).toBe(true);
		});

		it('contains no inline onclick/onload/onerror handlers', () => {
			expect(source).not.toMatch(/\son\w+\s*=\s*["']/);
		});

		it('imports no third-party origin resources', () => {
			expect(source).not.toMatch(/https?:\/\/[^/]+\/[^"'\s]*\.(?:js|css|woff2?)/);
		});
	});

	describe('M16 data contract consumption', () => {
		it('imports trust data types and function from portalTrust API client', () => {
			expect(source).toContain("from 'src/lib/api/portalTrust'");
			expect(source).toContain('portalGetTrustData');
		});

		it('references federation, signatures, queue_depth, trust_score, and vouches fields', () => {
			expect(source).toContain('.federation.');
			expect(source).toContain('.signatures.');
			expect(source).toContain('.queue_depth.');
			expect(source).toContain('.trust_score.');
			expect(source).toContain('.vouches.');
		});

		it('renders vouches as list/count, not strength bars', () => {
			const styleIdx = source.lastIndexOf('<style>');
			const templatePart = styleIdx > 0 ? source.substring(0, styleIdx) : source;
			expect(templatePart).toContain('trust__vouch-list');
			expect(templatePart).toContain('trust__vouch-item');
			expect(templatePart).not.toMatch(/vouch-item.*ProgressBar/i);
			expect(source).toContain('vouches.items');
		});

		// ── #573: Honest empty states ───────────────────────────────────

		it('shows honest empty state when no scoped peer data is present', () => {
			expect(source).toContain('No scoped federation peer data is present');
		});

		it('does not claim federation telemetry is "not yet instrumented"', () => {
			// #573: the normal empty-state path must not say instrumentation is missing
			expect(source).not.toContain('not yet instrumented');
		});

		it('shows honest queue depth empty state', () => {
			expect(source).toContain('No inbound queue depth data is present');
		});
	});

	describe('CSP: no inline style= attribute in template', () => {
		it('avoids dynamic width via style attribute in progress bars', () => {
			expect(source).not.toMatch(/style\s*=\s*["']width:\s*\{/);
		});
	});

	describe('label correctness', () => {
		it('does not hardcode "Sig failures 24h" as a literal', () => {
			// The label is dynamic: "Sig failures {windowHours}h".
			// With the backend 24h window the rendered label is "Sig failures 24h",
			// but the source must derive it, not hardcode it.
			expect(source).toContain('sigFailuresLabel');
			expect(source).not.toContain("'Sig failures 24h'");
		});

		it('derives sig-failures label from signatures.windowHours', () => {
			expect(source).toContain('sigFailuresLabel');
			expect(source).toMatch(/signatures\.windowHours/);
		});

		it('does not hardcode "Severed last 30d"', () => {
			expect(source).not.toContain('Severed last 30d');
		});

		it('uses "Severed peers" label', () => {
			expect(source).toContain('Severed peers');
		});
	});

	// ── #573: Peer grid renders follower_count and timestamps ─────────

	describe('peer grid renders real fields', () => {
		it('renders follower_count conditionally', () => {
			expect(source).toContain('peer.follower_count');
		});

		it('shows "followers unavailable" when follower_count is null/absent', () => {
			expect(source).toContain('followers unavailable');
		});

		it('renders last_seen timestamp', () => {
			expect(source).toContain('peer.last_seen');
			expect(source).toContain('Seen:');
		});

		it('renders last_fetch as fallback when last_seen is absent', () => {
			expect(source).toContain('peer.last_fetch');
			expect(source).toContain('Fetch:');
		});
	});

	// ── #573: Signature failures — no fabricated sparkline ────────────

	describe('signature failures panel truthfulness', () => {
		it('does not render a Sparkline in the signature failures panel', () => {
			// Per #573: the signature DTO has total_failures + by_source but
			// no time-series "series" field. The UI must not fabricate a
			// sparkline from per-source aggregate counts.
			// The only Sparkline in Trust.svelte should be in the queue depth panel.
			const styleIdx = source.lastIndexOf('<style>');
			const templatePart = styleIdx > 0 ? source.substring(0, styleIdx) : source;

			// Count Sparkline occurrences in template (before <style>)
			const sparklineMatches = templatePart.match(/<Sparkline/g);
			// Only the queue depth Sparkline should exist (1 occurrence).
			expect(sparklineMatches?.length ?? 0).toBe(1);
		});

		it('renders source list honestly without time-series claim', () => {
			expect(source).toContain('trust__source-list');
			expect(source).toContain('Failures by source');
		});
	});

	// ── #573: Queue depth sparkline from real series ──────────────────

	describe('queue depth renders real time series', () => {
		it('renders Sparkline from queueDepth.seriesDepthValues', () => {
			expect(source).toContain('queueDepth.seriesDepthValues');
		});

		it('renders queue depth Sparkline with values from series', () => {
			const styleIdx = source.lastIndexOf('<style>');
			const templatePart = styleIdx > 0 ? source.substring(0, styleIdx) : source;
			// Sparkline should be inside the queue depth panel
			const qdSectionStart = templatePart.indexOf('Inbound queue depth');
			const qdSectionEnd = templatePart.indexOf('</section>', qdSectionStart);
			const qdSection = templatePart.substring(qdSectionStart, qdSectionEnd);
			expect(qdSection).toContain('Sparkline');
		});
	});
});

// ── App.svelte route dispatch tests ────────────────────────────────────

describe('M15 Trust UI — App.svelte route dispatch', () => {
	it('imports PortalTrust from src/pages/portal/Trust.svelte', () => {
		expect(appSource).toContain(
			"import PortalTrust from 'src/pages/portal/Trust.svelte'",
		);
	});

	it('defines isPortalTrustDashboardRoute for exact /portal/trust match', () => {
		expect(appSource).toContain('isPortalTrustDashboardRoute');
		expect(appSource).toContain("=== '/portal/trust'");
	});

	it('defines isPortalTrustAttestationRoute for /portal/trust/attestations prefix', () => {
		expect(appSource).toContain('isPortalTrustAttestationRoute');
		expect(appSource).toContain("/portal/trust/attestations");
	});

	it('renders PortalTrust for dashboard route and Trust for attestation subroute', () => {
		expect(appSource).toContain('<PortalTrust');
		expect(appSource).toContain('<Trust />');
	});

	it('preserves public /trust route dispatch to Trust component', () => {
		expect(appSource).toContain("startsWith('/trust/')");
		expect(appSource).toContain('<Trust />');
	});
});

// ── DOM mount tests — component renders ─────────────────────────────────

describe('M15 Trust UI — DOM mount', () => {
	it('mounts without error and renders the page header', async () => {
		const target = document.createElement('div');
		document.body.appendChild(target);

		const instance = mount(PortalTrust, {
			target,
			props: { token: '' },
		});

		await tick();

		const header = target.querySelector('.trust__header');
		expect(header).not.toBeNull();
		expect(header?.textContent).toContain('Trust');
		expect(header?.textContent).toContain('federation');

		unmount(instance);
		document.body.removeChild(target);
	});

	it('renders peer constellation section', () => {
		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(PortalTrust, {
			target,
			props: { token: '' },
		});

		const section = target.querySelector('[aria-label="Federation peer constellation"]');
		expect(section).not.toBeNull();

		document.body.removeChild(target);
	});

	it('renders trust score gauge SVG', () => {
		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(PortalTrust, {
			target,
			props: { token: '' },
		});

		const gauge = target.querySelector('.trust-gauge');
		expect(gauge).not.toBeNull();
		expect(gauge?.tagName).toBe('svg');

		document.body.removeChild(target);
	});

	it('renders signature failures panel', () => {
		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(PortalTrust, {
			target,
			props: { token: '' },
		});

		const panel = target.querySelector('[aria-label="HTTP signature failures"]');
		expect(panel).not.toBeNull();

		document.body.removeChild(target);
	});

	it('renders queue depth panel', () => {
		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(PortalTrust, {
			target,
			props: { token: '' },
		});

		const panel = target.querySelector('[aria-label="Inbound queue depth"]');
		expect(panel).not.toBeNull();

		document.body.removeChild(target);
	});

	it('renders vouches section', () => {
		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(PortalTrust, {
			target,
			props: { token: '' },
		});

		const panel = target.querySelector('[aria-label="Vouches from peers"]');
		expect(panel).not.toBeNull();

		document.body.removeChild(target);
	});

	it('trust score gauge has proper ARIA attributes', () => {
		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(PortalTrust, {
			target,
			props: { token: '' },
		});

		const gauge = target.querySelector('.trust-gauge');
		expect(gauge?.getAttribute('role')).toBe('meter');
		expect(gauge?.getAttribute('aria-valuemin')).toBe('0');
		expect(gauge?.getAttribute('aria-valuemax')).toBe('100');

		document.body.removeChild(target);
	});
});
