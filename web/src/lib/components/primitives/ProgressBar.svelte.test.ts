/**
 * M1 ProgressBar unit tests.
 *
 * Covers the prop matrix: value, max, tone variants, edge cases.
 */
import { describe, expect, it } from 'vitest';
import { mount } from 'svelte';
import ProgressBar from './ProgressBar.svelte';

describe('ProgressBar', () => {
	it('renders with default props', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: 42 } });

		const bar = target.querySelector('.bar');
		expect(bar).not.toBeNull();
		expect(bar!.getAttribute('role')).toBe('progressbar');
		expect(bar!.getAttribute('aria-valuenow')).toBe('42');
		expect(bar!.getAttribute('aria-valuemin')).toBe('0');
		expect(bar!.getAttribute('aria-valuemax')).toBe('100');

		const fill = target.querySelector('.bar__fill');
		expect(fill).not.toBeNull();
		expect(fill!.getAttribute('data-ratio')).toBe('42');
	});

	it('renders with custom max', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: 5, max: 10 } });

		const fill = target.querySelector('.bar__fill');
		expect(fill!.getAttribute('data-ratio')).toBe('50');

		const bar = target.querySelector('.bar');
		expect(bar!.getAttribute('aria-valuenow')).toBe('5');
		expect(bar!.getAttribute('aria-valuemax')).toBe('10');
	});

	it('renders warning tone', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: 75, tone: 'warning' } });

		const fill = target.querySelector('.bar__fill');
		expect(fill!.classList.contains('bar__fill--warning')).toBe(true);
	});

	it('renders error tone', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: 95, tone: 'error' } });

		const fill = target.querySelector('.bar__fill');
		expect(fill!.classList.contains('bar__fill--error')).toBe(true);
	});

	it('renders success tone', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: 10, tone: 'success' } });

		const fill = target.querySelector('.bar__fill');
		expect(fill!.classList.contains('bar__fill--success')).toBe(true);
	});

	it('renders accent tone', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: 50, tone: 'accent' } });

		const fill = target.querySelector('.bar__fill');
		expect(fill!.classList.contains('bar__fill--accent')).toBe(true);
	});

	it('clamps value at 100%', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: 150, max: 100 } });

		const fill = target.querySelector('.bar__fill');
		expect(fill!.getAttribute('data-ratio')).toBe('100');
	});

	it('clamps value at 0% for negative values', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: -10, max: 100 } });

		const fill = target.querySelector('.bar__fill');
		expect(fill!.getAttribute('data-ratio')).toBe('0');
	});

	it('handles zero max by returning 0%', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: 5, max: 0 } });

		const fill = target.querySelector('.bar__fill');
		expect(fill!.getAttribute('data-ratio')).toBe('0');
	});

	it('uses accessible label from prop', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: 50, label: 'Disk usage' } });

		const bar = target.querySelector('.bar');
		expect(bar!.getAttribute('aria-valuetext')).toBe('Disk usage');
	});

	it('generates default accessible label when not provided', () => {
		const target = document.createElement('div');
		mount(ProgressBar, { target, props: { value: 73 } });

		const bar = target.querySelector('.bar');
		expect(bar!.getAttribute('aria-valuetext')).toBe('Progress: 73%');
	});
});
