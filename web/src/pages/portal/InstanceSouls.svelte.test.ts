/**
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (C) 2026 Equal-to-AI. All rights reserved.
 */
/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

const source = readFileSync(join(process.cwd(), 'src/pages/portal/InstanceSouls.svelte'), 'utf8');

describe('InstanceSouls M10 Souls tab', () => {
	// ── Table layout ────────────────────────────────────────────────────

	it('renders souls in a table with Handle / Stage / Model / Anchor / Tips (lifetime) columns', () => {
		expect(source).toContain('instance-souls__table');
		expect(source).toContain('<th>Handle</th>');
		expect(source).toContain('<th>Stage</th>');
		expect(source).toContain('<th>Model</th>');
		expect(source).toContain('<th>Anchor</th>');
		expect(source).toContain('<th>Tips (lifetime)</th>');
	});

	it('uses Panel with "Souls on this instance" title and Request soul CTA', () => {
		expect(source).toContain('Souls on this instance');
		expect(source).toContain('+ Request soul');
	});

	it('shows bound count in eyebrow', () => {
		expect(source).toContain('boundAgents.length');
		expect(source).toContain('bound');
		expect(source).toContain('instance-souls__eyebrow');
	});

	// ── Request soul CTA disabled ───────────────────────────────────────

	it('renders Request soul button as disabled (coming soon)', () => {
		expect(source).toContain('disabled');
		expect(source).toContain('Request soul');
		expect(source).toContain('coming soon');
	});

	it('has explanatory tooltip on disabled Request soul button', () => {
		expect(source).toContain('Soul request workflow is coming soon');
		expect(source).toContain('Simulacrum agent workspace');
	});

	// ── Stage badge mapping ─────────────────────────────────────────────

	it('defines badgeForStage function mapping lifecycle_status to badge style', () => {
		expect(source).toContain('function badgeForStage(');
		expect(source).toContain("'graduated'");
		expect(source).toContain("'in_review'");
		expect(source).toContain("'requested'");
		expect(source).toContain("'on_hold'");
	});

	it('graduated → filled success badge with "graduated" label', () => {
		expect(source).toContain("return { variant: 'filled', color: 'success', label: 'graduated' }");
	});

	it('in_review → outlined info badge with "in review" label', () => {
		expect(source).toContain("return { variant: 'outlined', color: 'info', label: 'in review' }");
	});

	it('falls back to agent.status when lifecycle_status is absent', () => {
		expect(source).toContain('item.agent.lifecycle_status, item.agent.status');
	});

	it('renders stage badge via Badge component with mapped variant/color', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('<Badge');
		expect(template).toContain('{stageStyle.variant}');
		expect(template).toContain('{stageStyle.color}');
	});

	// ── Anchor freshness ────────────────────────────────────────────────

	it('defines anchorDisplay function from anchor_assurance state', () => {
		expect(source).toContain('function anchorDisplay(');
		expect(source).toContain('item.agent.anchor_assurance?.state');
		expect(source).toContain("'immutable_onchain'");
		expect(source).toContain("'hosted_offchain'");
	});

	it('immutable_onchain → "fresh" with success dot', () => {
		expect(source).toContain("label: 'fresh'");
		expect(source).toContain("color: 'success'");
	});

	it('hosted_offchain → "pending" with warning dot', () => {
		expect(source).toContain("label: 'pending'");
		expect(source).toContain("color: 'warning'");
	});

	it('absent anchor_assurance → "—" gray', () => {
		expect(source).toContain("label: '—'");
		expect(source).toContain("dot: false");
		expect(source).toContain("color: 'gray'");
	});

	// ── Model unavailable ───────────────────────────────────────────────

	it('shows model as "—" because model metadata is not on the DTO', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);

		// The model column contains "—" inside the mono span.
		expect(template).toContain('instance-souls__cell-model');
		expect(source).not.toContain('agent.model');
	});

	// ── Tips display ────────────────────────────────────────────────────

	it('defines tipsDisplay function from reputation.tips_received', () => {
		expect(source).toContain('function tipsDisplay(');
		expect(source).toContain('item.reputation?.tips_received');
		expect(source).toContain('.toFixed(2)');
	});

	it('renders tips with dollar sign prefix', () => {
		expect(source).toContain("`$${tips.toFixed(2)}`");
	});

	it('returns "—" when tips_received is null or undefined', () => {
		expect(source).toContain("if (tips == null) return '—'");
	});

	// ── Row navigation (keyboard-accessible) ────────────────────────────

	it('navigates to /portal/souls/{agent_id} on row click', () => {
		expect(source).toContain("const soulPath = `/portal/souls/${item.agent.agent_id}`");
		expect(source).toContain("navigate(soulPath)");
	});

	it('tr has role="link" and tabindex="0" for keyboard accessibility', () => {
		expect(source).toContain('role="link"');
		expect(source).toContain('tabindex="0"');
	});

	it('tr onkeydown handles Enter and Space for keyboard navigation', () => {
		expect(source).toContain("e.key === 'Enter' || e.key === ' '");
		expect(source).toContain('e.preventDefault()');
	});

	it('tr has aria-label identifying the soul', () => {
		expect(source).toContain('aria-label="Open soul');
	});

	it('chevron column has a Link component for dedicated keyboard target', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('instance-souls__cell-chevron');
		expect(template).toContain('<Link');
	});

	// ── Avatar rendering ────────────────────────────────────────────────

	it('renders Avatar with image src and local_id name fallback', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('<Avatar');
		expect(template).toContain('{item.agent.avatar?.image}');
		expect(template).toContain('{item.agent.local_id}');
	});

	// ── M0.5 domain-matching fix preserved ──────────────────────────────

	it('preserves M0.5 managed_lesser_domain matching fix', () => {
		expect(source).toContain('managed_lesser_domain');
		expect(source).toContain('candidateDomains[managedDomain] = true');
		expect(source).toContain('hosted_base_domain');
		expect(source).toContain('candidateDomains[baseDomain] = true');
	});

	// ── Empty / no-domain / error states ────────────────────────────────

	it('shows "Instance not provisioned" when no hosted_base_domain', () => {
		expect(source).toContain('Instance not provisioned');
		expect(source).toContain('no managed base domain yet');
	});

	it('shows "No souls bound to this instance" when boundAgents is empty', () => {
		expect(source).toContain('No souls bound to this instance');
	});

	it('shows "Failed to load souls" on error', () => {
		expect(source).toContain('Failed to load souls');
	});

	it('shows loading spinner while fetching', () => {
		expect(source).toContain('Loading souls');
		expect(source).toContain('<Spinner');
	});

	// ── CSP / security invariants ───────────────────────────────────────

	it('has no inline style attributes', () => {
		expect(source).not.toMatch(/\sstyle\s*=\s*["'{]/);
	});

	it('has no inline event handlers beyond Svelte onclick/onkeydown', () => {
		// Svelte onclick/onkeydown use the expression syntax, not string-based.
		expect(source).not.toMatch(/\sonclick\s*=\s*["']/);
		expect(source).not.toMatch(/\sonkeydown\s*=\s*["']/);
	});

	it('never renders raw agent/wallet material in the table', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);

		// The wallet field should not appear in the table template.
		expect(template).not.toContain('{item.agent.wallet}');
		// Raw agent_id should be hidden from plain display (only used in hrefs/aria).
		// The visible display uses local_id.
		expect(template).toContain('{item.agent.local_id');
	});

	// ── Data freshness (date formatting) ──────────────────

	it('defines formatDate helper for timestamp display', () => {
		expect(source).toContain('function formatDate(');
		expect(source).toContain("startsWith('0001-01-01T00:00:00')");
	});
});
