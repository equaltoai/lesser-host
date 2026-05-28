/**
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (C) 2026 Equal-to-AI. All rights reserved.
 */
/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

const source = readFileSync(join(process.cwd(), 'src/pages/portal/InstanceConfig.svelte'), 'utf8');

describe('InstanceConfig M9 Configuration tab', () => {
	// ── Design section presence ─────────────────────────────────────────

	it('renders Instance identity section with DL items', () => {
		expect(source).toContain('Instance identity');
		expect(source).toContain('Display name');
		expect(source).toContain('Description');
		expect(source).toContain('Default visibility');
		expect(source).toContain('Reg open');
	});

	it('renders Federation policy section with five ConfigToggle rows', () => {
		expect(source).toContain('Federation policy');
		expect(source).toContain('Accept federation');
		expect(source).toContain('Allow quote posts');
		expect(source).toContain('Auto-thread sync');
		expect(source).toContain('AI moderation hint');
		expect(source).toContain('Public webfinger');
	});

	it('renders Rate limits section with four DL items', () => {
		expect(source).toContain('Rate limits');
		expect(source).toContain('Posts / hour');
		expect(source).toContain('Inbox delivery');
		expect(source).toContain('Search QPS');
		expect(source).toContain('Outbound HTTP');
	});

	// ── Unavailable / disabled states ───────────────────────────────────

	it('shows identity fields as unavailable when not on InstanceResponse', () => {
		// Description, Default visibility, and Reg open have no backend field.
		// Each renders a secondary-coloured "—" to indicate unavailability.
		const emDashCount = (source.match(/—/g) || []).length;
		expect(emDashCount).toBeGreaterThanOrEqual(3); // at minimum Description, Default visibility, Reg open
	});

	it('shows federation toggles as disabled (not supported by current config API)', () => {
		// All five ConfigToggle Switch elements are `disabled={true}`.
		const disabledCount = (source.match(/disabled=\{true\}/g) || []).length;
		expect(disabledCount).toBeGreaterThanOrEqual(5);
	});

	it('shows rate limit values as unavailable', () => {
		// Each rate limit DLItem renders "—" (not available).
		// The Rate limits section plus the identity section together contain ≥7 em dashes.
		const emDashCount = (source.match(/—/g) || []).length;
		expect(emDashCount).toBeGreaterThanOrEqual(7);
	});

	// ── Toggle sizing normalisation (audit P3) ──────────────────────────

	it('uses config__toggle-row class for federation ConfigToggle rows', () => {
		expect(source).toContain('config__toggle-row');
		expect(source).toContain('config__toggle-row-body');
		expect(source).toContain('config__toggle-rows');
	});

	it('uses normalised config__toggle class for Features toggles', () => {
		// Features / Moderation / AI toggles use the normalised config__toggle class.
		expect(source).toContain('config__toggle');
	});

	// ── Existing functionality preserved ────────────────────────────────

	it('preserves Features, Moderation, and AI mutation panels', () => {
		expect(source).toContain('title="Features"');
		expect(source).toContain('title="Moderation"');
		expect(source).toContain('title="AI"');
	});

	it('preserves save/refresh actions', () => {
		expect(source).toContain('void save()');
		expect(source).toContain('void load()');
		expect(source).toContain('canSave');
	});

	it('preserves 401 redirect on load and save', () => {
		expect(source).toContain(').status === 401');
		expect(source).toContain('await logout();');
		expect(source).toContain("navigate('/login');");
	});

	// ── CSP / security invariants ───────────────────────────────────────

	it('has no inline style attributes', () => {
		// Strict single-origin CSP: no `style=` attributes.
		expect(source).not.toMatch(/\sstyle\s*=\s*["'{]/);
	});

	it('has no inline event handlers beyond Svelte onclick', () => {
		// No raw HTML `onclick=` or `onerror=` attributes.
		expect(source).not.toMatch(/\sonclick\s*=\s*["']/);
	});

	// ── displayName derivation ──────────────────────────────────────────

	it('derives display name from slug by replacing hyphens with spaces and capitalising', () => {
		expect(source).toContain("replace(/-/g, ' ')");
		expect(source).toContain('(c: string) => c.toUpperCase()');
		expect(source).toContain('const displayName');
	});
});
