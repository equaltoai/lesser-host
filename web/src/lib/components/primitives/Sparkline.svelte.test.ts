/**
 * M1 Sparkline unit tests.
 *
 * Covers: data series rendering, area fill on/off, empty state,
 * custom dimensions, deterministic path output.
 *
 * @license AGPL-3.0-only
 */
import { describe, expect, it } from 'vitest';
import { mount } from 'svelte';
import Sparkline from './Sparkline.svelte';

describe('Sparkline', () => {
	it('renders an SVG with path for data series', () => {
		const target = document.createElement('div');
		mount(Sparkline, { target, props: { values: [3, 8, 5, 12, 9, 14, 10] } });

		const svg = target.querySelector('svg');
		expect(svg).not.toBeNull();
		expect(svg!.getAttribute('viewBox')).toBe('0 0 120 32');

		// Should have two paths: area fill + line
		const paths = svg!.querySelectorAll('path');
		expect(paths.length).toBe(2);

		// Area path should have fill-opacity
		const areaPath = paths[0];
		expect(areaPath.getAttribute('fill-opacity')).toBe('0.12');
		expect(areaPath.getAttribute('fill')).toBe('var(--ds-secondary-500)');
		expect(areaPath.getAttribute('d')?.length).toBeGreaterThan(0);

		// Line path should have stroke
		const linePath = paths[1];
		expect(linePath.getAttribute('fill')).toBe('none');
		expect(linePath.getAttribute('stroke')).toBe('var(--ds-secondary-500)');
		expect(linePath.getAttribute('stroke-width')).toBe('1.6');
	});

	it('renders nothing when values is empty', () => {
		const target = document.createElement('div');
		mount(Sparkline, { target, props: { values: [] } });

		const svg = target.querySelector('svg');
		expect(svg).toBeNull();

		const wrapper = target.querySelector('.gr-m1-sparkline');
		expect(wrapper).toBeNull();
	});

	it('omits area fill path when fill=false', () => {
		const target = document.createElement('div');
		mount(Sparkline, { target, props: { values: [1, 5, 3], fill: false } });

		const paths = target.querySelectorAll('path');
		expect(paths.length).toBe(1);
		expect(paths[0].getAttribute('fill')).toBe('none');
	});

	it('respects custom width, height, and color', () => {
		const target = document.createElement('div');
		mount(Sparkline, { target, props: {
			values: [1, 2, 3],
			width: 200,
			height: 48,
			color: 'var(--ds-warning-500)',
		} });

		const svg = target.querySelector('svg');
		expect(svg!.getAttribute('viewBox')).toBe('0 0 200 48');

		const linePath = svg!.querySelectorAll('path')[1];
		expect(linePath.getAttribute('stroke')).toBe('var(--ds-warning-500)');
	});

	it('produces deterministic path for single-value series', () => {
		// Single value should render a horizontal line at the midpoint
		const target = document.createElement('div');
		mount(Sparkline, { target, props: { values: [5], width: 120, height: 32 } });

		const svg = target.querySelector('svg');
		expect(svg).not.toBeNull();
		const paths = svg!.querySelectorAll('path');
		expect(paths.length).toBe(2);
		// The line path should start with M
		const d = paths[1].getAttribute('d') ?? '';
		expect(d.startsWith('M')).toBe(true);
	});

	it('produces deterministic path for two-value series', () => {
		// Two values should produce a diagonal line
		const target1 = document.createElement('div');
		mount(Sparkline, { target: target1, props: { values: [0, 10], width: 120, height: 32 } });

		const svg1 = target1.querySelector('svg')!;
		const d1 = svg1.querySelectorAll('path')[1].getAttribute('d') ?? '';

		// Same input → same output (deterministic)
		const target2 = document.createElement('div');
		mount(Sparkline, { target: target2, props: { values: [0, 10], width: 120, height: 32 } });

		const svg2 = target2.querySelector('svg')!;
		const d2 = svg2.querySelectorAll('path')[1].getAttribute('d') ?? '';

		expect(d1).toBe(d2);
	});

	it('has correct SVG accessibility attributes', () => {
		const target = document.createElement('div');
		mount(Sparkline, { target, props: { values: [1, 2, 3] } });

		const svg = target.querySelector('svg');
		expect(svg!.getAttribute('role')).toBe('img');
		expect(svg!.getAttribute('aria-label')).toBe('Sparkline');
		expect(svg!.getAttribute('focusable')).toBe('false');
	});

	it('renders with preserveAspectRatio="none" for responsive scaling', () => {
		const target = document.createElement('div');
		mount(Sparkline, { target, props: { values: [1, 2, 3] } });

		const svg = target.querySelector('svg');
		expect(svg!.getAttribute('preserveAspectRatio')).toBe('none');
	});
});
