# M1 Primitives — Design Fidelity Evidence

**Issue:** [#535](https://github.com/equaltoai/lesser-host/issues/535)
**Date:** 2026-05-27
**Branch:** `aron/portal-m1-primitives`

## Screenshot status

**PNG evidence is pending.** This environment is headless (no display server, no browser automation tool installed). A pixel-accurate screenshot will be produced post-lab-deployment via browser DevTools screenshot at 2× DPR.

## Component inventory vs. design fixture

Design source: `/tmp/design-sNM/lesser-host/project/src/primitives.jsx`
CSS source: `/tmp/design-sNM/lesser-host/project/assets/app.css`
Token source: `/tmp/design-sNM/lesser-host/project/assets/tokens.css`

### Eyebrow

| Aspect | Design Fixture | Implementation | Match |
|--------|---------------|----------------|-------|
| Element | `<As>` (default `p`) | `<p>` (fixed) | Partial — CSP-safe simplification. Dynamic element `<{as}>` is unsupported by eslint-plugin-svelte at current version. Consumer can wrap in desired tag. |
| CSS class | `.eyebrow` | `.eyebrow` | Exact |
| Tokens | `--ds-eyebrow-size`, `--ds-eyebrow-weight`, `--ds-eyebrow-track`, `--ds-fg-3`, `--ds-font-sans` | Same (via existing app.css) | Exact |
| text-transform | uppercase | uppercase (via CSS) | Exact |

### Metric

| Aspect | Design Fixture | Implementation | Match |
|--------|---------------|----------------|-------|
| Container | `<div class="metric">` with `position: relative` | `<div class="metric">` (position via app.css) | Exact |
| Label | `<span class="metric__label">` | Same | Exact |
| Value | `<div class="metric__value">` | Same | Exact |
| Subtext | `<div class="metric__delta">` | `<div class="metric__delta metric__delta--sub">` | Minimal deviation: added `--sub` modifier for CSS scoping |
| Delta arrow | `↗` / `↘` chars | Same chars in `<span class="metric__delta-arrow">` | Exact |
| Delta direction | Classes `metric__delta--up` / `--down` | Same | Exact |
| Icon | Inline `<span style={{ color: 'var(--ds-fg-3)' }}>` | `<span class="metric__icon">` (color via CSS) | CSP-safe equivalent |
| Accent | `style={{ color: accent }}` (arbitrary CSS value) | `tone` prop: `'success' | 'warning' | 'error' | 'info' | 'accent'` → CSS class `metric__value--<tone>` | **CSP-driven deviation**: inline `style` is prohibited. Tonal keywords replace arbitrary color values. CSS variables in class-selected rules provide equivalent visual effect. |

### CostGauge (circular SVG)

| Aspect | Design Fixture | Implementation | Match |
|--------|---------------|----------------|-------|
| Type | Circular SVG gauge | Same | Exact |
| Props | `{ used, budget, size=72, label }` | Same | Exact |
| Thresholds | 70% warning, 90% danger | Same (≥70 warning, ≥90 danger) | Exact |
| Colors | `var(--ds-error-500)` / `var(--ds-warning-500)` / `var(--ds-success-500)` | Same (via CSS classes `--ok`/`--warning`/`--danger`) | Exact |
| Rotation | `transform: rotate(-90deg)` on SVG | Same, scoped to ring `<g>` via CSS | Improved: text stays upright |
| Center text | HTML overlay div with inline styles | SVG `<text>` elements with presentation attributes | CSP-safe equivalent. Percentage font-size scaling preserved via SVG `font-size` attribute. |
| Stroke dashoffset | Inline `style={{ strokeDashoffset }}` | SVG attribute `stroke-dashoffset={offset}` | CSP-safe equivalent |
| Stroke color | Inline `style={{ stroke: tone }}` | CSS class on wrapper → child arc inherits | CSP-safe equivalent |
| Transition | `style={{ transition: 'stroke-dashoffset 0.5s' }}` | CSS `transition: stroke-dashoffset 500ms ease` | Equivalent |
| ARIA | None in design | `role="meter"`, `aria-valuenow/max/min`, `aria-valuetext` | Enhanced |
| Size | `style={{ width: size, height: size }}` on container | SVG `width`/`height` attributes on `<svg>` | CSP-safe equivalent |

### Sparkline

| Aspect | Design Fixture | Implementation | Match |
|--------|---------------|----------------|-------|
| Props | `{ values, width=120, height=32, color, fill=true }` | Same | Exact |
| Empty state | `return null` | `{#if hasData}...{/if}` (renders nothing) | Exact |
| Path algorithm | `toFixed(1)` coordinates | Same | Exact |
| Fill area | `fill={color} opacity="0.12"` | `fill={color} fill-opacity="0.12"` | Equivalent (SVG attribute vs inline style) |
| Stroke | `stroke={color} strokeWidth="1.6"` | `stroke={color} stroke-width="1.6"` | Equivalent (SVG attributes) |
| SVG sizing | `style={{ height }}` on SVG | SVG `height` attribute + `.sparkline-svg` CSS class (`width: 100%`) | CSP-safe equivalent |
| Dimensions | `viewBox="0 0 {width} {height}"` + `preserveAspectRatio="none"` | Same | Exact |
| ARIA | None in design | `role="img"`, `aria-label="Sparkline"`, `focusable="false"` | Enhanced |

### ProgressBar

| Aspect | Design Fixture | Implementation | Match |
|--------|---------------|----------------|-------|
| Props | `{ value, max=100, tone }` | `{ value, max=100, tone, label }` | Extended: added accessible label |
| Container | `<div class="bar">` | Same | Exact |
| Fill | `<div class="bar__fill bar__fill--{tone}">` | Same class pattern | Exact |
| Width | Inline `style={{ width: 'X%' }}` | `data-ratio={integer}` attribute + CSS selectors (0–100) | CSP-safe equivalent |
| Tone variants | `warning`, `error`, `success` | Same + `accent` | Extended: `accent` uses `var(--ds-action-gradient)` |
| ARIA | None in design | `role="progressbar"`, `aria-valuenow/min/max`, `aria-valuetext` | Enhanced |

## Dimensions summary

| Component | Default dimensions | CSS reference |
|-----------|-------------------|---------------|
| Eyebrow | Inherits from text content | `app.css .eyebrow` |
| Metric | Fills container, min-width: 0 | `app.css .metric` |
| CostGauge | 72×72px (configurable via `size`) | `CostGauge.css` |
| Sparkline | 120×32px viewBox (CSS: `width: 100%; height: auto`) | `app.css .sparkline-svg` |
| ProgressBar | 100% width, 7px height | `app.css .bar` |

## Color and token usage

All components consume `--ds-*` tokens bridged from `tokens.css`. No inline color values. Status communication pairs visual tone classes with text/icons (never color-only).

## Deviations from design fixture

1. **CSP-driven deviations** (mandatory — strict CSP prohibits inline styles):
   - Metric: `accent` prop → `tone` keyword + CSS class (not arbitrary CSS color)
   - Metric: icon color → `.metric__icon` CSS class (not inline `style`)
   - CostGauge: all inline styles → CSS classes + SVG presentation attributes
   - ProgressBar: `style="width: X%"` → `data-ratio` attribute + CSS selectors
   - Sparkline: `style={{ height }}` → SVG `height` attribute

2. **ARIA enhancements** (beyond design fixture):
   - CostGauge: `role="meter"` with full ARIA value contract
   - ProgressBar: `role="progressbar"` with ARIA value attributes
   - Sparkline: `role="img"` with `aria-label`

3. **Tooling simplification**:
   - Eyebrow: fixed `<p>` element (avoiding eslint-plugin-svelte limitation on dynamic element syntax)

## Test coverage

- CostGauge: 10 tests covering 0%, 50%, 75%, 95%, over-budget, zero budget, custom size, boundary transitions (70%, 90%)
- Metric: 10 tests covering all prop combinations (label, value, sub, delta/direction, icon placeholder, all 5 tones)
- ProgressBar: 11 tests covering value clamping, tone variants (warning/error/success/accent), edge cases (zero/negative max), accessibility labels
- Sparkline: 8 tests covering SVG structure, empty state, fill toggle, dimensions, deterministic output, accessibility
