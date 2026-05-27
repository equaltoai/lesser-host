/**
 * M1 CostGauge unit tests.
 *
 * Acceptance gate: covers 0%, 50%, 75%, and 95% as required by issue #535.
 * Additional coverage: edge cases (zero budget, over-budget, exact thresholds).
 *
 * @license AGPL-3.0-only
 */
import { describe, expect, it } from 'vitest';
import { mount } from 'svelte';
import CostGauge from './CostGauge.svelte';

describe('CostGauge', () => {
	it('renders 0% with ok status (used=0, budget=100)', () => {
		const target = document.createElement('div');
		mount(CostGauge, { target, props: { used: 0, budget: 100 } });

		const svg = target.querySelector('svg');
		expect(svg).not.toBeNull();
		expect(svg!.getAttribute('role')).toBe('meter');
		expect(svg!.getAttribute('aria-valuenow')).toBe('0');
		expect(svg!.getAttribute('aria-valuemax')).toBe('100');
		expect(svg!.getAttribute('aria-valuetext')).toBe('0% of budget');

		const percent = target.querySelector('.gr-m1-cost-gauge__percent');
		expect(percent?.textContent?.trim()).toBe('0%');

		const status = target.querySelector('[data-test="cost-gauge-status"]');
		expect(status?.textContent?.trim()).toBe('Within budget');

		const root = target.querySelector('.gr-m1-cost-gauge');
		expect(root?.classList.contains('gr-m1-cost-gauge--ok')).toBe(true);
	});

	it('renders 50% with ok status (used=50, budget=100)', () => {
		const target = document.createElement('div');
		mount(CostGauge, { target, props: { used: 50, budget: 100 } });

		const percent = target.querySelector('.gr-m1-cost-gauge__percent');
		expect(percent?.textContent?.trim()).toBe('50%');

		const svg = target.querySelector('svg');
		expect(svg!.getAttribute('aria-valuenow')).toBe('50');

		const status = target.querySelector('[data-test="cost-gauge-status"]');
		expect(status?.textContent?.trim()).toBe('Within budget');

		const root = target.querySelector('.gr-m1-cost-gauge');
		expect(root?.classList.contains('gr-m1-cost-gauge--ok')).toBe(true);

		// Verify arc exists with correct stroke-dasharray
		const arc = target.querySelector('.gr-m1-cost-gauge__arc');
		expect(arc).not.toBeNull();
		expect(arc!.getAttribute('stroke-dashoffset')).not.toBeNull();
	});

	it('renders 75% with warning status (threshold ≥70%)', () => {
		const target = document.createElement('div');
		mount(CostGauge, { target, props: { used: 75, budget: 100 } });

		const percent = target.querySelector('.gr-m1-cost-gauge__percent');
		expect(percent?.textContent?.trim()).toBe('75%');

		const status = target.querySelector('[data-test="cost-gauge-status"]');
		expect(status?.textContent?.trim()).toBe('Approaching limit');

		const root = target.querySelector('.gr-m1-cost-gauge');
		expect(root?.classList.contains('gr-m1-cost-gauge--warning')).toBe(true);
		expect(root?.classList.contains('gr-m1-cost-gauge--ok')).toBe(false);
	});

	it('renders 95% with danger status (threshold ≥90%)', () => {
		const target = document.createElement('div');
		mount(CostGauge, { target, props: { used: 95, budget: 100 } });

		const percent = target.querySelector('.gr-m1-cost-gauge__percent');
		expect(percent?.textContent?.trim()).toBe('95%');

		const status = target.querySelector('[data-test="cost-gauge-status"]');
		expect(status?.textContent?.trim()).toBe('Exceeds threshold');

		const root = target.querySelector('.gr-m1-cost-gauge');
		expect(root?.classList.contains('gr-m1-cost-gauge--danger')).toBe(true);
	});

	it('renders label when provided', () => {
		const target = document.createElement('div');
		mount(CostGauge, { target, props: { used: 42, budget: 100, label: 'Monthly' } });

		const label = target.querySelector('.gr-m1-cost-gauge__label');
		expect(label?.textContent?.trim()).toBe('Monthly');
	});

	it('clamps percentage >100% to 100% with danger status', () => {
		const target = document.createElement('div');
		mount(CostGauge, { target, props: { used: 150, budget: 100 } });

		const percent = target.querySelector('.gr-m1-cost-gauge__percent');
		expect(percent?.textContent?.trim()).toBe('100%');

		const root = target.querySelector('.gr-m1-cost-gauge');
		expect(root?.classList.contains('gr-m1-cost-gauge--danger')).toBe(true);
	});

	it('handles zero budget by falling back to role="img" and 0%', () => {
		const target = document.createElement('div');
		mount(CostGauge, { target, props: { used: 10, budget: 0 } });

		const svg = target.querySelector('svg');
		expect(svg!.getAttribute('role')).toBe('img');
		expect(svg!.getAttribute('aria-valuenow')).toBeNull();

		const percent = target.querySelector('.gr-m1-cost-gauge__percent');
		expect(percent?.textContent?.trim()).toBe('0%');
	});

	it('renders with custom size', () => {
		const target = document.createElement('div');
		mount(CostGauge, { target, props: { used: 25, budget: 100, size: 96 } });

		const svg = target.querySelector('svg');
		expect(svg!.getAttribute('width')).toBe('96');
		expect(svg!.getAttribute('height')).toBe('96');

		// Percentage font-size should scale: 96 * 0.24 = 23.04
		const percent = target.querySelector('.gr-m1-cost-gauge__percent');
		expect(percent!.getAttribute('font-size')).toBe('23.04');
	});

	it('transitions from ok to warning at exactly 70% (boundary)', () => {
		const target = document.createElement('div');
		mount(CostGauge, { target, props: { used: 70, budget: 100 } });

		const percent = target.querySelector('.gr-m1-cost-gauge__percent');
		expect(percent?.textContent?.trim()).toBe('70%');

		const status = target.querySelector('[data-test="cost-gauge-status"]');
		expect(status?.textContent?.trim()).toBe('Approaching limit');

		const root = target.querySelector('.gr-m1-cost-gauge');
		expect(root?.classList.contains('gr-m1-cost-gauge--warning')).toBe(true);
	});

	it('transitions from warning to danger at exactly 90% (boundary)', () => {
		const target = document.createElement('div');
		mount(CostGauge, { target, props: { used: 90, budget: 100 } });

		const percent = target.querySelector('.gr-m1-cost-gauge__percent');
		expect(percent?.textContent?.trim()).toBe('90%');

		const status = target.querySelector('[data-test="cost-gauge-status"]');
		expect(status?.textContent?.trim()).toBe('Exceeds threshold');

		const root = target.querySelector('.gr-m1-cost-gauge');
		expect(root?.classList.contains('gr-m1-cost-gauge--danger')).toBe(true);
	});
});
