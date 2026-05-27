/**
 * @fileoverview M1 Primitives — Project 42 foundation components.
 *
 * Visual primitives matching the Project 42 design fixture for consumption
 * by M2+ milestones. All components are strict-CSP safe (no inline styles
 * or event handlers), pure/presentational (no fetch logic, no global state,
 * no route assumptions), and consume `--ds-*` design tokens.
 *
 * Components:
 * - Eyebrow     — uppercase tracked label primitive
 * - Metric      — metric tile with label, value, subtext, delta, tone
 * - CostGauge   — circular SVG budget gauge with threshold colors
 * - Sparkline   — tiny SVG line chart with optional area fill
 * - ProgressBar — CSP-safe horizontal progress bar with tone variants
 *
 * @version 0.1.0
 * @license AGPL-3.0-only
 * @public
 */

export type { ComponentProps } from 'svelte';

export { default as Eyebrow } from './Eyebrow.svelte';
export { default as Metric } from './Metric.svelte';
export { default as CostGauge } from './CostGauge.svelte';
export { default as Sparkline } from './Sparkline.svelte';
export { default as ProgressBar } from './ProgressBar.svelte';
