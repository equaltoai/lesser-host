# M3 — ⌘K command palette

**Branch.** `aron/portal-m3-cmdk`
**PR title.** `feat(portal): ⌘K command palette`
**Concern.** Global command palette that opens with ⌘K (and Ctrl-K
on non-mac), exposes Navigate / Actions / Instances / Souls groups,
fuzzy-filters by label and hint, navigates on Enter, closes on
Escape and click-outside.

## Scope (≤ 5 tasks)

1. **Global keybinding.** Register `metaKey/ctrlKey + 'k'` at the
   PortalShell root via a `window.addEventListener('keydown', …)`
   inside an `onMount` lifecycle, with matching `onDestroy` cleanup.
   `Escape` closes when open.
2. **CommandPalette component.** A modal backdrop + centered panel
   with a search input. Four groups: Navigate (static seed),
   Actions (static seed), Instances (dynamic from session
   instance list), Souls (dynamic from session soul list). Maps to
   `project/src/shell.jsx:249–329`.
3. **Fuzzy filter.** Filter each group by case-insensitive substring
   match against `label` or `hint`. Hide groups whose item list is
   empty. Map order matches the design's group order.
4. **Wire to existing routes.** Selecting an item navigates via
   the existing router (`onNavigate(path)`); no new routes are
   created. Action items without paths (e.g. "Refresh data") are
   either deferred (stubbed onClick that logs) or wired to existing
   no-op behaviours; pick whichever is honest per item and
   document in the PR body.
5. **A11y + tests.**
   - Focus traps inside the modal while open; Tab cycles within it.
   - ARIA `role="dialog"`, `aria-label="Command palette"`.
   - Unit tests: opens on ⌘K, closes on Esc, click-outside closes,
     filter narrows results, enter navigates.

## Out of scope

- Real action wiring for items the design lists as "Refresh data",
  "New instance…", "Request a soul…" — those land with their
  respective surface milestones. M3 stubs them with a TODO log.
- Search-on-the-server. Filtering is fully client-side over the
  in-memory session lists.
- Operator-console palette variants — Layer 3.

## Acceptance criteria

- Side-by-side fidelity artifact at the design's modal size.
- Opens reliably on ⌘K and Ctrl-K from any portal page; topbar
  trigger button (wired in M2) also opens it.
- Closes on Esc, click-outside, and after selecting an item.
- Keyboard navigation works (arrow up / down highlights items,
  Enter selects). Optional in M3 if it inflates scope past
  task limit — if so, file as a polish milestone.
- No new dependencies added to `web/package.json`.
- CSP unchanged.
- Web lint + typecheck + test + build green.
- Gov rubric green.

## Risks

- ⌘K bindings can clash with browser extensions (1Password etc.).
  This is acceptable; ⌘K is industry-standard for command palettes
  and users with conflicts can use the topbar trigger.
- SSR + global listener registration: bind inside `onMount` to
  avoid running on the server.
- Focus-trap is a small but real a11y surface; using a vetted
  library is fine if one is already in `web/`; otherwise hand-roll
  the trap with the minimum elements needed.

## Evidence

- Design-fidelity artifact under
  `gov-infra/evidence/design-fidelity/m3-cmdk/cmdk.{png,md}`.
- A short markdown note recording the action items that were
  stubbed (not yet wired) so a later milestone can sweep them up.

## Estimated size

≤ 5 commits, ≤ 350 lines diff. Reviewable in ≤ 15 minutes.
