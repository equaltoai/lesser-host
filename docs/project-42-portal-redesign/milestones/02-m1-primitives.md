# M1 — Foundation primitives

**Branch.** `aron/portal-m1-primitives`
**PR title.** `feat(portal): primitives for Project 39 design (CostGauge, Sparkline, Metric, Eyebrow)`
**Concern.** Add the visual primitives every later milestone consumes.
No surfaces are modified by this PR. No data wiring. No layout
changes.

## Scope (≤ 7 tasks)

1. **Inventory check against greater-components.** Verify whether
   `@equaltoai/greater-components-primitives` already exports
   equivalents of `Metric`, `Eyebrow`, `Sparkline`, `CostGauge`,
   `ProgressBar` with `tone` variants, or close-enough versions.
   Add only what is missing. Where greater has an equivalent,
   either consume it directly or, if a small extension is needed,
   open a coordination signal with the greater steward via
   `coordinate-framework-feedback` (separate scope; this PR does
   not block on it).
2. **`Eyebrow.svelte`** — uppercase tracked label primitive
   (`primitives.jsx:7–9`, CSS `.eyebrow`).
3. **`Metric.svelte`** — metric tile: label + value + sub + delta
   with direction arrow, optional icon, optional accent colour.
   Maps to `primitives.jsx:102–120` and the `.metric` CSS block.
4. **`CostGauge.svelte`** — circular SVG gauge for budget %; rotates
   at -90deg, stroke colour switches at 70 % / 90 % thresholds, big
   pct label + small all-caps `label` prop in the centre. Maps to
   `primitives.jsx:198–218`.
5. **`Sparkline.svelte`** — tiny SVG line chart with optional area
   fill; props `values`, `width`, `height`, `color`, `fill`. Maps to
   `primitives.jsx:133–147`.
6. **`ProgressBar` tone variants.** If host's existing ProgressBar
   does not accept `tone in {warning, error, success, accent}`, add
   it. The design uses warning when spend > 80 % of budget.
7. **Tests + visual fixtures.** One unit test per primitive covering
   the prop matrix. Add a small `web/src/lib/primitives/__fixtures__/`
   page (or Storybook stories if the project uses them) so the
   primitives render in isolation for the design-fidelity check in
   later milestones.

## Out of scope

- No surface mounts these primitives yet — that lands in later
  milestones.
- No shell changes — those are M2.
- No ⌘K — that is M3.
- No new tokens unless a primitive needs one that doesn't exist
  yet; if so, scope minimal additions inside this PR and note
  them in the PR body. **Do not** restructure the existing token
  set.

## Acceptance criteria

- Each primitive renders correctly against the design fixture at a
  matched viewport (compare to the relevant section of
  `project/src/primitives.jsx` and `project/assets/app.css`).
- All primitives are pure Svelte components with no fetch logic and
  no global state.
- Unit tests pass for every prop variant exercised by the design
  (e.g. CostGauge at 0 %, 50 %, 75 %, 95 %).
- Web lint + typecheck + test + build green.
- CSP unchanged (no inline styles, no third-party origins).
- AGPL headers on new files.

## Risks

- Greater-components may have a different `Metric` shape; consuming
  the existing one is preferred to a host-local copy. If consuming,
  document the mapping in the PR body so future updates trace
  upstream.
- `CostGauge` and `Sparkline` are SVG; verify they render correctly
  in the SSR snapshot (FaceTheory) without hydration mismatch.

## Evidence

- Design-fidelity artifact under
  `gov-infra/evidence/design-fidelity/m1-primitives/primitives.{png,md}`
  showing each primitive rendered in the fixtures page next to the
  design's rendering. Markdown notes record exact dimensions,
  colours used, and any deliberate deviations.

## Estimated size

≤ 8 commits, ≤ 600 lines diff. Reviewable in ≤ 20 minutes.
