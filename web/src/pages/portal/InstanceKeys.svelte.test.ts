/**
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (C) 2026 Equal-to-AI. All rights reserved.
 */
/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

const source = readFileSync(join(process.cwd(), 'src/pages/portal/InstanceKeys.svelte'), 'utf8');

describe('InstanceKeys M9 Keys tab', () => {
	// ── Table layout ────────────────────────────────────────────────────

	it('renders keys in a table with Token / Scopes / Created / Last used columns', () => {
		expect(source).toContain('keys__table');
		expect(source).toContain('<th>Token</th>');
		expect(source).toContain('<th>Scopes</th>');
		expect(source).toContain('<th>Created</th>');
		expect(source).toContain('<th>Last used</th>');
	});

	it('uses Panel with "API keys" title and Issue key action', () => {
		expect(source).toContain('API keys');
		expect(source).toContain('Issue key');
		expect(source).toContain('portalCreateInstanceKey');
	});

	// ── Key ID masking ──────────────────────────────────────────────────

	it('defines maskKeyId function', () => {
		expect(source).toContain('function maskKeyId(');
		expect(source).toContain('.slice(0, 8)');
		expect(source).toContain('.slice(-4)');
	});

	it('masks key IDs as prefix...suffix format', () => {
		// The mask format: first 8 chars + "..." + last 4 chars.
		expect(source).toContain('...');
		expect(source).toContain('maskKeyId(k.id)');
	});

	it('falls back to full ID when too short to mask', () => {
		expect(source).toContain('trimmed.length <= 14');
		expect(source).toContain('return trimmed;');
	});

	// ── Raw key safety ──────────────────────────────────────────────────

	it('never renders raw key material in the keys table', () => {
		// The raw key (`k.key`, `created.key`) is only shown once at creation
		// in the one-time alert. The table renders only `k.id` (masked).
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);

		// The table body iterates over keys and accesses only k.id, not the raw key.
		// There should be no reference to a `key` property on the list item in the table.
		expect(template).not.toContain('{k.key}');
	});

	it('shows raw key only in the one-time creation alert (not in table)', () => {
		// The raw key is in the `created` object (CreateInstanceKeyResponse).
		// It is displayed in the creation-warning Alert, not in the keys table.
		expect(source).toContain('created?.key');
		// `created?.key` only appears in the creation-alert block.
		const firstCreatedKey = source.indexOf('created?.key');
		const secondCreatedKey = source.indexOf('created?.key', firstCreatedKey + 1);
		expect(secondCreatedKey).toBe(-1); // appears only once
	});

	it('copy button is safe — never copies raw key from table', () => {
		// The CopyButton in the table copies `k.id` (token identifier), not the raw key.
		// Find the CopyButton in the table context (after the each block).
		const eachIdx = source.indexOf('each keys as k');
		expect(eachIdx).toBeGreaterThan(0);
		const afterEach = source.slice(eachIdx);

		// The CopyButton in the table must use k.id, never k.key or the raw key.
		expect(afterEach).toContain('CopyButton');
		expect(afterEach).toContain('text={k.id}');
	});

	// ── Scopes unavailable ──────────────────────────────────────────────

	it('shows scopes as "—" because scope metadata is not available', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('keys__cell-scopes');
		expect(template).toContain('—');
	});

	// ── Date formatting ─────────────────────────────────────────────────

	it('formats dates with toLocaleDateString en-US medium style', () => {
		expect(source).toContain("toLocaleDateString('en-US'");
		expect(source).toContain("month: 'short'");
		expect(source).toContain("day: 'numeric'");
	});

	it('handles zero-date (0001-01-01) as "—"', () => {
		expect(source).toContain("startsWith('0001-01-01T00:00:00')");
		expect(source).toContain("return '—'");
	});

	// ── Revoke functionality ────────────────────────────────────────────

	it('shows Revoke button for non-revoked keys only', () => {
		expect(source).toContain('!k.revoked_at');
		expect(source).toContain('portalRevokeInstanceKey');
		expect(source).toContain('Revoke');
	});

	it('shows "Revoked" text for revoked keys', () => {
		expect(source).toContain('>Revoked<');
	});

	it('requires confirmation before revoking', () => {
		expect(source).toContain('window.confirm');
		expect(source).toContain('Revoke key');
	});

	// ── CSP / security invariants ───────────────────────────────────────

	it('has no inline style attributes', () => {
		expect(source).not.toMatch(/\sstyle\s*=\s*["'{]/);
	});

	it('has no inline event handlers beyond Svelte onclick', () => {
		expect(source).not.toMatch(/\sonclick\s*=\s*["']/);
	});

	// ── Active count eyebrow ────────────────────────────────────────────

	it('shows active key count in eyebrow', () => {
		expect(source).toContain('activeCount()');
		expect(source).toContain('keys__eyebrow');
	});
});
