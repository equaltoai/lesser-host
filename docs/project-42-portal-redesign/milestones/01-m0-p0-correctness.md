# M0 — P0 correctness bundle

**Branch (arch-specified).** `aron/project42-m0-portal-correctness`
**PR title (arch-specified).** `fix(portal): M0 correctness and immediate chrome hygiene`
**Project / parent.** equaltoai/projects/42 · parent issue `equaltoai/lesser-host#525`
**Source brief.** `arch.equaltoai@theorymcp.ai` delivery
`delivery-d08bce036c8f3243` (2026-05-27 17:48 UTC).
**Concern.** Six P0 audit rows + two chrome-hygiene rows, total
eight tasks. No design changes, no new components, no new endpoints,
no token changes.

Per the bundle rules, this milestone exists separately from any
design work so the legacy portal is stable before foundation
redesign begins.

## Scope — sub-issues (≤ 8 tasks)

Arch-created sub-issues under parent `#525`:

| # | Sub-issue | Task |
|---|---|---|
| 1 | `#526` M0.1 | Route Trust and Account inside portal chrome |
| 2 | `#527` M0.2 | Correct Billing page heading |
| 3 | `#528` M0.3 | Hide provisioning form for live instances |
| 4 | `#529` M0.4 | Degrade cost telemetry failure without hiding root cause |
| 5 | `#530` M0.5 | Fix Instance Souls bound-agent fetch |
| 6 | `#531` M0.6 | Remove literal slug template leak |
| 7 | `#532` M0.7 | Replace raw sidebar wallet identity preview or prove deferred |
| 8 | `#533` M0.8 | Resolve or defer duplicate Instance Overview Refresh control |

## Original outline (kept for traceability)

1. **Route fix — Trust + Account inside portal namespace.** Move
   `/trust` and `/account` under `/portal/*` (matching the design's
   `/portal/trust` and `/portal/account`). Preserve the PortalShell on
   both routes. Update sidebar nav entries to the new paths. Verify
   refresh + deep-link both work.
2. **Billing h1 fix.** Replace the page `<h1>` "Portal Dashboard"
   with "Billing" on `/portal/billing`. Verify other Portal pages
   are unaffected.
3. **Hide provisioning form on healthy instances.** In
   `InstanceDetail.svelte` (and/or `Instances.svelte`), gate the
   "Not started" warning banner + "Start provisioning" form behind
   `instance.status === 'provisioning' || instance.status === 'unprovisioned'`.
   For active / live instances the section disappears entirely.
4. **Graceful cost-telemetry degradation.** Wrap the
   `portalGetInstanceCost` call in `InstanceCost.svelte` so an HTTP
   500 or network failure renders a soft empty state ("Cost
   telemetry warming up · last value at HH:MM" or "temporarily
   unavailable") instead of bubbling the error to the page. Keep the
   budget + summary sections functional independently per the
   existing pattern noted in M3.12.
5. **Souls tab fetch fix.** Investigate why `InstanceSouls.svelte`
   shows the "no souls bound" empty state on instances that have
   souls. Likely candidates: wrong filter key, mismatched slug vs.
   instance ID, missing pagination, or wrong endpoint. Fix the
   fetch; preserve the existing empty-state markup for instances
   that genuinely have no souls.
6. **Slug template literal fix.** `Instances.svelte` description
   string contains a literal `{slug}` token. Replace with the
   selected slug, or rephrase the description so the literal is
   not exposed.
7. **Sidebar identity fix (preview only).** Replace the raw wallet
   hash in the sidebar footer with `@{display_name}` + truncated
   wallet (`0x4f29…f9c2` pattern). The full M2 redesign supersedes
   this; this task is the immediate-fix variant.

Optional 8th task (stretch, can defer to M2):

8. **Remove duplicate Refresh button on Instance Overview if
   trivially safe.** The audit flagged two identically styled
   Refresh buttons. If the panel-scoped one is clearly redundant,
   remove it; if behaviour differs, leave both for M6 to address
   properly.

## Out of scope

- No layout redesign.
- No new metric cards / sparklines / gauges.
- No right-rail panels.
- No new endpoints.
- No `gov-infra/pack.json` bump (no new verifier).
- No primitives.

## Acceptance criteria

- Each P0 row in `../audit-mapping.md` flagged "M0" is reproducible
  before the PR and not reproducible after.
- `web lint`, `web typecheck`, `web test` green.
- `cd cdk && npm test` unchanged.
- `gov-infra/verifiers/gov-verify-rubric.sh` green at existing
  pass count.
- Manual smoke: navigate to `/portal/trust`, `/portal/account`,
  `/portal/billing`, `/portal/instances/{slug}` (Souls tab and
  Cost tab) on a lab-deployed branch; verify each P0 is fixed.

## Risks

- The Souls tab fetch fix may reveal an underlying backend bug
  rather than a UI bug. If so, split: fix the obvious UI path
  here, file an issue for the backend regression, document the
  finding in the PR body.
- Trust + Account route changes may regress sidebar nav active-
  state matching; verify the active highlight still tracks
  correctly on both old and new paths during the transition.

## Evidence

- M0 is correctness-only, so no design-fidelity artifact.
- Capture before/after screenshots in the PR description for each
  P0 row.
- No new gov-infra evidence artifact required.

## Estimated size

≤ 8 commits, ≤ 400 lines diff total. Reviewable in ≤ 20 minutes.
