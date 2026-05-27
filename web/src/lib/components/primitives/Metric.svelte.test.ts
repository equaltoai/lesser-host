/**
 * M1 Metric unit tests.
 *
 * Covers the prop matrix: label, value, sub, delta/deltaDir, icon, tone.
 *
 * @license AGPL-3.0-only
 */
import { describe, expect, it } from 'vitest';
import { mount } from 'svelte';
import Metric from './Metric.svelte';

describe('Metric', () => {
	it('renders label and value', () => {
		const target = document.createElement('div');
		mount(Metric, { target, props: { label: 'Active users', value: '1,243' } });

		const label = target.querySelector('.metric__label');
		expect(label?.textContent).toBe('Active users');

		const value = target.querySelector('.metric__value');
		expect(value?.textContent).toBe('1,243');

		// Default: no tone class on value
		expect(value?.classList.contains('metric__value--success')).toBe(false);
	});

	it('renders subtext', () => {
		const target = document.createElement('div');
		mount(Metric, { target, props: { label: 'CPU', value: '42%', sub: 'Avg across 4 cores' } });

		const sub = target.querySelector('.metric__delta--sub');
		expect(sub?.textContent).toBe('Avg across 4 cores');
	});

	it('renders positive delta with up arrow', () => {
		const target = document.createElement('div');
		mount(Metric, { target, props: { label: 'Revenue', value: '$4,200', delta: '+12%', deltaDir: 'up' } });

		const delta = target.querySelector('.metric__delta');
		expect(delta?.textContent).toContain('+12%');
		expect(delta?.classList.contains('metric__delta--up')).toBe(true);

		const arrow = target.querySelector('.metric__delta-arrow');
		expect(arrow?.textContent?.trim()).toBe('↗');
	});

	it('renders negative delta with down arrow', () => {
		const target = document.createElement('div');
		mount(Metric, { target, props: { label: 'Errors', value: '3', delta: '-40%', deltaDir: 'down' } });

		const delta = target.querySelector('.metric__delta');
		expect(delta?.textContent).toContain('-40%');
		expect(delta?.classList.contains('metric__delta--down')).toBe(true);

		const arrow = target.querySelector('.metric__delta-arrow');
		expect(arrow?.textContent?.trim()).toBe('↘');
	});

	it('renders delta without direction (no arrow)', () => {
		const target = document.createElement('div');
		mount(Metric, { target, props: { label: 'Users', value: '500', delta: 'Unchanged' } });

		const delta = target.querySelector('.metric__delta');
		expect(delta?.textContent?.trim()).toBe('Unchanged');
		expect(delta?.classList.contains('metric__delta--up')).toBe(false);
		expect(delta?.classList.contains('metric__delta--down')).toBe(false);

		const arrow = target.querySelector('.metric__delta-arrow');
		expect(arrow).toBeNull();
	});

	it('renders with success tone', () => {
		const target = document.createElement('div');
		mount(Metric, { target, props: { label: 'Uptime', value: '99.9%', tone: 'success' } });

		const value = target.querySelector('.metric__value');
		expect(value?.classList.contains('metric__value--success')).toBe(true);
	});

	it('renders with warning tone', () => {
		const target = document.createElement('div');
		mount(Metric, { target, props: { label: 'Latency', value: '250ms', tone: 'warning' } });

		const value = target.querySelector('.metric__value');
		expect(value?.classList.contains('metric__value--warning')).toBe(true);
	});

	it('renders with error tone', () => {
		const target = document.createElement('div');
		mount(Metric, { target, props: { label: 'Failures', value: '12', tone: 'error' } });

		const value = target.querySelector('.metric__value');
		expect(value?.classList.contains('metric__value--error')).toBe(true);
	});

	it('renders with accent tone', () => {
		const target = document.createElement('div');
		mount(Metric, { target, props: { label: 'Total', value: '$9.4k', tone: 'accent' } });

		const value = target.querySelector('.metric__value');
		expect(value?.classList.contains('metric__value--accent')).toBe(true);
	});

	it('renders without tone class when tone is omitted', () => {
		const target = document.createElement('div');
		mount(Metric, { target, props: { label: 'Plain', value: '0' } });

		const value = target.querySelector('.metric__value');
		// Only the base class, no tone variant
		expect(value?.classList.contains('metric__value')).toBe(true);
		expect(value?.classList.length).toBe(1);
	});
});
