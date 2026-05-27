# M1 Primitives — Design Fidelity Evidence

**Issue:** [#535](https://github.com/equaltoai/lesser-host/issues/535)
**Date:** 2026-05-27
**Branch:** `aron/portal-m1-primitives`

## Screenshot

**PNG evidence captured.** See `primitives.png` (960×1400px, 103 KB).

### Capture details

| Property | Value |
|----------|-------|
| Command | `/usr/bin/google-chrome --headless=new --no-sandbox --disable-gpu --window-size=960,1400 --virtual-time-budget=5000 --screenshot` |
| Browser | Google Chrome 148.0.7778.178 |
| Viewport | 960×1400px (window size; 1× DPR) |
| Fixture path | `web/fixtures/m1-primitives.html` (served via Vite build → static HTTP) |
| Components rendered | Eyebrow (3 variants), Metric (8 variants: base, success/warning/error/info/accent tones, up/down deltas, subtext), CostGauge (4 variants: 0%/50%/75%/95%), Sparkline (2 variants: with/without fill), ProgressBar (5 variants: default, success, warning, error, accent) |
| Comparison basis | `/tmp/design-sNM/lesser-host/project/src/primitives.jsx` + `assets/app.css` |
| Build config | `web/fixtures/vite.fixture.config.ts` (standalone; not the main web build) |
| Fixture CSS | `web/fixtures/m1-primitives-fixture.css` (layout-only; all primitive base classes ship in the M1 runtime CSS bundle at `web/src/lib/components/primitives/base.css`, imported via `web/src/lib/styles/m1-primitives.css`) |

### Visual inspection notes

- All five primitives render in a single column layout with section headings.
- Eyebrow renders as uppercase tracked labels in `--ds-fg-3`.
- Metric tiles render with card backgrounds, tone-colored values, and correct delta arrows (↗/↘).
- CostGauge rings render at correct rotation with threshold colors: green (0%/50%), amber (75%), red (95%).
- Sparkline renders SVG paths with and without area fill.
- ProgressBar renders with correct width ratios and tone-based gradient fills.
- Colors match the design fixture's `--ds-*` token references. The fixture page does not set the operator-chrome dark theme, so the Agent Genesis light theme is active — colors and background are consistent with the default token scale.

## Component inventory vs. design fixture

Design source: `/tmp/design-sNM/lesser-host/project/src/primitives.jsx`
CSS source: `/tmp/design-sNM/lesser-host/project/assets/app.css`
Token source: `/tmp/design-sNM/lesser-host/project/assets/tokens.css`

### Eyebrow

| Aspect | Design Fixture | Implementation | Match |
|--------|---------------|----------------|-------|
| Element | `<As>` (default `p`) | `<p>` (fixed) | Partial — CSP-safe simplification. Dynamic element `<{as}>` is unsupported by eslint-plugin-svelte at current version. Consumer can wrap in desired tag. |
| CSS class | `.eyebrow` | `.eyebrow` | Exact |
| Tokens | `--ds-eyebrow-size`, `--ds-eyebrow-weight`, `--ds-eyebrow-track`, `--ds-fg-3`, `--ds-font-sans` | Same (via base.css) | Exact |
| text-transform | uppercase | uppercase (via CSS) | Exact |

### Metric

| Aspect | Design Fixture | Implementation | Match |
|--------|---------------|----------------|-------|
| Container | `<div class="metric">` with `position: relative` | `<div class="metric">` (position via base.css) | Exact |
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
| Eyebrow | Inherits from text content | `base.css .eyebrow` |
| Metric | Fills container, min-width: 0 | `base.css .metric` |
| CostGauge | 72×72px (configurable via `size`) | `CostGauge.css` |
| Sparkline | 120×32px viewBox (CSS: `width: 100%; height: auto`) | `base.css .sparkline-svg` |
| ProgressBar | 100% width, 7px height | `base.css .bar` |

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

## Isolated fixture

A committed fixture renders all M1 primitives in isolation for visual review and PNG capture. It is NOT mounted by any customer portal route — no App.svelte changes, no route additions. Files:

- `web/src/lib/components/primitives/__fixtures__/M1PrimitivesFixture.svelte` — renders Eyebrow, Metric (tone + delta variants), CostGauge (0/50/75/95), Sparkline (with/without fill), ProgressBar (all tones)
- `web/fixtures/m1-primitives.html` — standalone HTML entry (Vite-served)
- `web/fixtures/m1-primitives.ts` — entry point (imports CSS, mounts fixture)
- `web/fixtures/m1-primitives-fixture.css` — layout-only (no primitive base classes; those ship in `web/src/lib/components/primitives/base.css`)
- `web/fixtures/vite.fixture.config.ts` — standalone Vite build config (no file watching, SPA-only)

M1 ships the complete runtime CSS for all primitives via `web/src/lib/styles/m1-primitives.css` (imports `base.css` + `CostGauge.css` + `Metric.css` + `ProgressBar.css`). No CSS work is deferred to M2.
All new source, fixture, test, CSS, TS, and Svelte files carry `@license AGPL-3.0-only` headers.
