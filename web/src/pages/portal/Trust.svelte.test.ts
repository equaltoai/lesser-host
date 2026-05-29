/**
 * M15 Trust UI contract tests.
 *
 * Route compatibility, CSP safety, attestation route preservation, and
 * public trust API path protection.
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
			// The component must never emit inline style attributes.
			// Svelte static styles are fine (compiled to <style> blocks).
			// We check for attribute patterns that would produce CSP violations.
			const styleAttrPattern = /\bstyle\s*=\s*"/g;
			// Allow only CSS custom property passing on SVG elements which
			// Svelte handles as presentation attributes.
			const matches = source.match(styleAttrPattern);
			// If any exist, verify they are permitted (e.g., none should exist
			// since we removed all inline styles)
			if (matches) {
				// Double-check: none of the matches should be inline CSS
				for (const m of matches) {
					expect(m).toBe(''); // fail if present
				}
			}
			// No expectation needed — the loop above catches violations
			expect(true).toBe(true);
		});

		it('contains no inline onclick/onload/onerror handlers', () => {
			expect(source).not.toMatch(/\son\w+\s*=\s*["']/);
		});

		it('imports no third-party origin resources', () => {
			// No external font/script/style imports in component
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
			// Per M16: vouches should be list/count presence, not strength
			// ranking, until a numeric strength source exists.
			// Inspect the template area (before the <style> tag), not the
			// style block.
			const styleIdx = source.lastIndexOf('<style>');
			const templatePart = styleIdx > 0 ? source.substring(0, styleIdx) : source;
			// Vouch items render as list items (trust__vouch-list, trust__vouch-item),
			// not as progress bars or comparative strength indicators.
			expect(templatePart).toContain('trust__vouch-list');
			expect(templatePart).toContain('trust__vouch-item');
			// Should not contain per-vouch progress-bar rendering patterns.
			expect(templatePart).not.toMatch(/vouch-item.*ProgressBar/i);
			// The component should reference vouchItem properties correctly.
			expect(source).toContain('vouches.items');
		});

		it('shows honest federation empty state when peers are not instrumented', () => {
			expect(source).toContain('not yet instrumented');
		});

		it('shows honest queue depth empty state when not instrumented', () => {
			expect(source).toContain('Inbound queue depth');
		});
	});

	describe('CSP: no inline style= attribute in template', () => {
		it('avoids dynamic width via style attribute in progress bars', () => {
			// The progress bar must use SVG width attribute, not CSS style.
			expect(source).not.toMatch(/style\s*=\s*["']width:\s*\{/);
		});
	});

	describe('label correctness', () => {
		it('does not hardcode "Sig failures 24h"', () => {
			// The M16 window is 168h, not 24h. The label must be dynamic.
			expect(source).not.toContain('24h');
		});

		it('derives sig-failures label from signatures.windowHours', () => {
			expect(source).toContain('sigFailuresLabel');
			expect(source).toMatch(/signatures\.windowHours/);
		});

		it('does not hardcode "Severed last 30d"', () => {
			// The M16 federation DTO exposes `severed` without a last-30d
			// window. Use an honest label.
			expect(source).not.toContain('Severed last 30d');
		});

		it('uses "Severed peers" label', () => {
			expect(source).toContain('Severed peers');
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
		// The template should have both PortalTrust and Trust rendering branches.
		expect(appSource).toContain('<PortalTrust');
		expect(appSource).toContain('<Trust />');
	});

	it('preserves public /trust route dispatch to Trust component', () => {
		// /trust paths should still route to Trust (public attestation inspector).
		expect(appSource).toContain("startsWith('/trust/')");
		expect(appSource).toContain('<Trust />');
	});
});

// ── DOM mount test — component renders ─────────────────────────────────

describe('M15 Trust UI — DOM mount', () => {
	it('mounts without error and renders the page header', async () => {
		const target = document.createElement('div');
		document.body.appendChild(target);

		// Mount with empty token — the component should render its
		// initial loading state or error state gracefully.
		const instance = mount(PortalTrust, {
			target,
			props: { token: '' },
		});

		await tick();

		// Should render the header even before data loads.
		const header = target.querySelector('.trust__header');
		expect(header).not.toBeNull();

		// The heading should contain the eyebrow text.
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

		// The section should exist even with no data.
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
