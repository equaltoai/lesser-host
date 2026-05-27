# Project 42 — Portal Redesign — planning bundle

This directory supersedes the 2026-05-24 Project 39 planning bundle
(`docs/project-39-web-ui-rework-2026-05-24.md`,
`docs/roadmap-web-ui-rework-2026-05-24.md`,
`docs/enumerated-changes-web-ui-rework-2026-05-24.md`,
`docs/governance-rubric-web-ui-rework-2026-05-24.md`).

Arch-driven board (created 2026-05-27): https://github.com/orgs/equaltoai/projects/42
Parent issue: https://github.com/equaltoai/lesser-host/issues/525
Older board (Project 39, superseded but still public):
https://github.com/orgs/equaltoai/projects/39

The earlier bundle landed the FaceTheory adoption, the AppTheory CDK upgrade,
the strict-CSP / OAC asset transport, the cost-telemetry endpoint + web
wiring, the SEC-13 verifier, and the Project 39 design tokens + branded
shell on lab. It did **not** deliver the per-surface content redesign
specified in the design fixture, and an automated browser audit on
2026-05-27 confirmed the gap is substantial (6 P0 correctness bugs and a
long list of P1/P2 design absences across Fleet, Instance Detail, Billing,
Souls, Trust, Account, and the shell).

This bundle is the corrected plan for **closing that gap**, organised under
explicit rules that the previous bundle violated.

## The design source of truth

The canonical design is the Claude Design handoff bundle at:

> https://api.anthropic.com/v1/design/h/sNM_e6zaXsWRLX9VetdC4w

The bundle is a tarball; the readable assets are at
`lesser-host/project/src/*.jsx`, `lesser-host/project/assets/*.css`,
`lesser-host/chats/chat1.md`, and `lesser-host/README.md`. A working
extraction lives at `/tmp/design-sNM/lesser-host/` on the steward's host.

Per the bundle README, the design medium is HTML/CSS/JS prototypes — the
job is to **recreate them pixel-perfectly** in the target codebase
(Svelte 5 + greater-components). Match the visual output; do **not** copy
the prototype's internal structure unless it happens to fit.

`design-spec-summary.md` in this directory captures the design intent
per surface with file:line references back into the bundle.

## What the previous plan got wrong

Three structural failings, documented so the new plan does not repeat
them:

1. **Macro-milestones bundled too much.** Prior M0 contained 29 enumerated
   sub-tasks across FaceTheory adoption (framework), CDK plumbing,
   design tokens, route porting, web build tests, shell adoption,
   `CommandPalette`, `FleetCard`, `CostGauge`, and `ActivitySparkline`.
   That is six separable concerns. The bundle's macro-milestone never
   produced a single coherent PR.
2. **UI and backend rode together.** Cost-telemetry endpoint, web wiring,
   verifier, and pack bump were all under one "M3" header even though
   each is a distinct PR with distinct review needs.
3. **No per-surface design-fidelity gate.** Each slice was self-graded
   against tests and the gov rubric, neither of which catches "the
   surface does not look like the design." Surfaces drifted while
   plumbing landed green.

## The rules this bundle enforces

These rules are non-negotiable for the work the bundle scopes. The
steward refuses milestones that violate them.

1. **One milestone, one branch, one PR, ≤ 8 tasks, single concern.**
   No mixing primitives with shell with content. No mixing UI with
   backend. Each PR is reviewable in ≤ 20 minutes.
2. **Backend and UI ship in separate milestones.** If a surface needs a
   new endpoint, the endpoint ships in milestone N (handler + DTO +
   redaction proof + unit test). The UI consuming it ships in milestone
   N+1. Never both in one PR.
3. **Per-surface UI milestones consume already-shipped primitives.**
   Primitives are added in foundation milestones, not invented inline
   on a content milestone.
4. **P0 correctness is its own slice.** Bug fixes do not ride alongside
   design milestones. M0 of this bundle is a bug-fix-only PR.
5. **Foundation precedes per-surface work.** Primitives (M1), shell (M2),
   and the command palette (M3) land before any per-surface UI milestone.
6. **Each design-milestone PR ships a side-by-side fidelity artifact.**
   A pair of screenshots — design fixture and live surface — at the same
   viewport, committed under
   `gov-infra/evidence/design-fidelity/<milestone>/<surface>.{png,md}`.
   The PR cannot merge if the comparison is visually wrong, even if all
   tests pass and the gov rubric is green. This is the gate the previous
   bundle did not have.
7. **One surface at a time.** Avoids merge conflicts in the shell and
   keeps each PR independently reviewable.

## Layered structure

| Layer | Purpose | Milestones |
|---|---|---|
| 0 — Correctness | Fix the 6 P0 bugs from the audit. No design work. | M0 |
| 1 — Foundation | Primitives, shell redesign, ⌘K palette. | M1, M2, M3 |
| 2 — Customer portal surfaces | One UI milestone per surface; data milestone only if endpoint is missing. | M4..M17 |
| 3 — Operator console | Dark warm-charcoal chrome + per-surface. Planned only after Layer 2 lands. | placeholder |

## Where the docs live

- `README.md` (this file) — rules, layering, design source of truth.
- `design-spec-summary.md` — per-surface design intent with file:line
  references back into the bundle.
- `audit-mapping.md` — every audit row (P0/P1/P2/P3) cross-referenced
  to the milestone that owns it.
- `milestones/00-index.md` — milestone index with one-line summaries.
- `milestones/01-m0-p0-correctness.md` — M0 detail.
- `milestones/02-m1-primitives.md` — M1 detail.
- `milestones/03-m2-shell.md` — M2 detail.
- `milestones/04-m3-cmdk.md` — M3 detail.
- `milestones/05-m4-fleet-ui.md` through `18-m17-account-ui.md` —
  outline only; detail is filled in only when the predecessor merges.
- `milestones/operator-console-layer.md` — Layer 3 placeholder.

## Tracking

Project tracking (Projects v2 board, GitHub issues, sub-issue
relationships) is driven by **arch.equaltoai@theorymcp.ai**. This
bundle is the host steward's proposed input to that planning step;
arch may adjust milestone ordering, names, or labels before issues are
filed. Until arch's board exists, M0 may still begin work locally on
its dedicated branch — the rules above govern the PR shape regardless
of whether the board is up.

## Cross-references

- Design bundle: https://api.anthropic.com/v1/design/h/sNM_e6zaXsWRLX9VetdC4w
- Audit (2026-05-27): in conversation transcript; rows mapped under
  `audit-mapping.md` in this directory.
- Prior planning bundle (superseded): `docs/project-39-web-ui-rework-2026-05-24.md`
- Already-shipped foundation work that this bundle assumes complete:
  PRs #520, #521, #522 for host-local billing/telemetry scaffolding and SEC-13
  verifier coverage; #523 (FaceTheory CDN cutover); #524 (Project 39 branded
  shell + FaceTheory v3.4.2 asset fix). Important caveat: PR #522 does **not**
  satisfy Project 42 M0.4 by itself. Portal instance cost/usage must read the
  managed Lesser instance metrics contract, not host-local or synthetic
  telemetry.
