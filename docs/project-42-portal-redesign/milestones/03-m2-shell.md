# M2 — PortalShell redesign

**Branch.** `aron/portal-m2-shell`
**PR title.** `feat(portal): redesigned PortalShell — grouped sidebar, identity chip, bell, ⌘K trigger`
**Concern.** Redesign the customer portal's outer chrome to match
the design fixture. **No content surface changes.** Each
content surface continues to render exactly what it renders today;
only the shell wrapping them changes.

## Scope (≤ 7 tasks)

1. **Grouped sidebar nav.** Render four sections with eyebrow
   labels: `Overview`, `Instances`, `Agents`, `Settings`. Maps to
   `project/src/shell.jsx:128–162`.
2. **Per-instance sidebar entries.** Under the `Instances` section,
   render one entry per instance the customer owns: status dot
   (success / warning / accent for provisioning / error), slug
   label, and an alert badge ("!") when `status === 'warning'`.
   Click navigates to `/portal/instances/{slug}`.
3. **User chip footer.** Replace today's raw-wallet footer with the
   design's pattern: avatar + `@{display_name}` + `{role} · {method}`
   subtext + sign-out icon button. The M0 immediate-fix is replaced
   by this richer version.
4. **"Operator console" footer button.** Below the user chip, when
   `session.role in {admin, operator}`, render an outlined button
   with a shield icon that navigates to `/operator`. Reuses the
   existing operator route.
5. **Topbar bell icon (placeholder).** Add a non-functional bell
   icon button on the topbar right side. Clicking opens an empty
   popover that says "Notifications coming soon." This is a
   placeholder; wiring the bell to real data is a future milestone.
6. **Topbar ⌘K trigger button.** Add the "Search instances, souls,
   jobs… ⌘K" button. Clicking it dispatches an event the M3 palette
   will hook. In M2 the button is rendered but inert (the M3 PR
   wires it up). Document this in the PR body.
7. **Crumb-rendering cleanup.** Audit the existing breadcrumb
   logic; if it mismatches the design's path → crumb mapping for
   any portal route, fix here. The design's mapping lives in
   `project/src/app.jsx:60–106`.

## Out of scope

- ⌘K command palette — that's M3.
- Notification bell wiring — future milestone.
- Operator shell redesign — Layer 3.
- Any surface-content change.
- New endpoints.

## Acceptance criteria

- Side-by-side fidelity artifact at 1440 × 900 viewport for:
  - Sidebar full height (showing groups + per-instance entries +
    user chip + Operator-console button).
  - Topbar full width (showing crumbs + ⌘K trigger + bell).
- Per-instance status dots use the same colour tokens the design
  uses (`--ds-success-500`, `--ds-warning-500`, `--ds-accent-500`,
  `--ds-error-500`).
- Sign-out button still works (no regression).
- Operator-console button visible only when role is admin or
  operator; hidden otherwise. Unit test covers both branches.
- CSP unchanged; no new external origins.
- AGPL headers on new files.
- Web lint + typecheck + test + build green.
- Gov rubric green.

## Risks

- The per-instance list in the sidebar depends on the instance list
  already loading client-side at shell mount. Verify the load order;
  if the list arrives asynchronously, the sidebar should skeleton
  the section rather than show "no instances" momentarily.
- The CommandPalette M3 PR will hook into the ⌘K trigger; if M2
  ships first and M3 lags, the button is visibly inert. Acceptable
  as long as M3 follows promptly.
- Operator-console button must navigate but not pre-fetch operator
  data, to keep customer-side requests scoped.

## Evidence

- Design-fidelity artifact under
  `gov-infra/evidence/design-fidelity/m2-shell/{sidebar,topbar,full-shell}.{png,md}`.

## Estimated size

≤ 7 commits, ≤ 500 lines diff. Reviewable in ≤ 20 minutes.
