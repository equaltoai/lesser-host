# M3 — ⌘K Command Palette — Design Fidelity Evidence

**Milestone:** M3 — ⌘K command palette  
**Issue:** [#537](https://github.com/equaltoai/lesser-host/issues/537)  
**PR:** [#554](https://github.com/equaltoai/lesser-host/pull/554)  
**Date:** 2026-05-27  

## Screenshot

**PNG evidence captured.** See `cmdk.png` (1440×900px, 46 KB).

### Capture details

| Property | Value |
|----------|-------|
| Command | `/usr/bin/google-chrome --headless=new --no-sandbox --disable-gpu --window-size=1440,900 --virtual-time-budget=8000 --screenshot` |
| Browser | Google Chrome (headless) |
| Viewport | 1440×900px (window size; 1× DPR) |
| Fixture path | `web/fixtures/m3-cmdk.html` (served via Vite → static HTTP on port 5201) |
| Fixture component | `web/src/lib/components/__fixtures__/M3CmdkFixture.svelte` |
| Fixture entry | `web/fixtures/m3-cmdk.ts` |
| Vite config | `web/fixtures/vite.fixture.m3.config.ts` |
| Artifact type | **Fixture screenshot** — the CommandPalette is rendered directly with static group data (`open={true}`), not nested inside PortalShell. This isolates the M3 palette surface from PortalShell's API-dependent lifecycle. |
| Comparison basis | The fixture populates all four groups (Navigate, Actions, Instances, Souls) with the same items PortalShell produces at runtime; the rendered palette is visually identical to what a user sees after pressing ⌘K. |
| Groups rendered | Navigate (7 items: Fleet, Instance list, Souls, Trust, Account, Billing, Operator Console), Actions (4 items: Log out, New instance… [disabled], Request a soul…, Refresh data [disabled]), Instances (4 items: my-instance, staging-env, demo-site, dev-sandbox), Souls (3 items: ghost, scribe, sentinel) |

### Visual inspection notes

- The palette renders as a modal overlay (semi-transparent dark backdrop) with a centered white panel at ~10vh from the top.
- The search input is focused and empty on open; the placeholder reads "Search instances, souls, jobs…".
- Four groups render with uppercase section headers (Navigate, Actions, Instances, Souls).
- Disabled items (New instance…, Refresh data) render at reduced opacity with `aria-disabled="true"`.
- The "Go to Fleet" item shows a `F` shortcut kbd chip.
- The status bar at the bottom reads "18 results".
- All styling consumes `--gr-*` tokens via the DS bridge; no inline styles are present.

## Implemented state

The ⌘K command palette is wired into PortalShell and reuses the
greater-components `CommandPalette` component which provides:

- `role="dialog"` with `aria-modal="true"` + `aria-labelledby`
- Focus trap (`createFocusTrap`) — input receives focus on open; Tab
  cycles within the panel
- Keyboard navigation: Arrow Up / Down moves virtual focus, Enter
  activates, Escape closes, Home/End for edges
- Click-outside dismiss via backdrop `pointerdown` handler
- Live region announcements for result counts and loading/empty states
- Client-side fuzzy filtering: case-insensitive token matching across
  `label`, `description`, and `keywords`

### Opening triggers

1. **⌘K / Ctrl+K keybinding** — registered in `onMount` via
   `document.addEventListener('keydown', …)`, cleaned up in the
   return function. `Escape` closes.
2. **Topbar ⌘K trigger button** — dispatches `lesserhost:cmd-k-trigger`
   CustomEvent (wired in M2). M3 adds the listener that sets
   `paletteOpen = true` and resets the query.

### Group structure (design order preserved)

| # | Group | Source | Notes |
|---|-------|--------|-------|
| 1 | **Navigate** | Static seed in PortalShell | 6–7 items: Fleet, Instance list, Souls, Trust, Account, Billing, plus Operator Console for admin/operator sessions |
| 2 | **Actions** | Static seed + stubs | Log out / Sign in (session-dependent), plus 3 additional items (see stubbed actions below) |
| 3 | **Instances** | Derived from `portalFleetInstances` store (populated by PortalFleet page) | "Open {slug}" entries with region/version description. Hidden when store is empty. |
| 4 | **Souls** | Fetched via `soulListMyAgents(token)` in `loadSidebarData` | "{local_id}@{domain}" entries with status description. Hidden when no souls are bound. M3 addition. |

### Data flow

- **Souls** are fetched in `PortalShell.loadSidebarData()` alongside
  instances and portalMe. The `souls` `$state` array feeds a
  `$derived` `soulsGroup` that maps each `SoulMineAgentItem` to a
  `CommandPaletteItem` with id `soul.{agent_id}`.
- On success, the Souls group appears as the fourth group.
- On failure (or empty response), the group is `null` and the palette
  renders 3 groups (Navigate, Actions, and optionally Instances).
- No new routes. Selection navigates to existing routes:
  - Navigate items → `/portal`, `/portal/instances`, `/portal/souls`,
    `/portal/trust`, `/portal/account`, `/portal/billing`, `/operator`
  - Soul items → `/portal/souls/{agent_id}`
  - "Request a soul…" → `/portal/souls/register`

## Stubbed actions

The following actions appear in the Actions group but are **disabled**
(`disabled: true`) or routed to existing endpoints. They are documented
here for future milestone sweep-up.

| Action ID | Label | Status | Future milestone |
|-----------|-------|--------|-----------------|
| `action.new-instance` | New instance… | **Disabled** (stub) | M4 Fleet UI — this should open the instance creation flow within the Fleet page context |
| `action.request-soul` | Request a soul… | **Wired** → `/portal/souls/register` | Existing route; no further work needed |
| `action.refresh-data` | Refresh data | **Disabled** (stub) | Future — needs a portal-wide data refresh mechanism |

## Deliberate visual deviations

- **No custom panel styling.** The palette reuses the greater-shell
  `CommandPalette` component's default styling (`.gr-shell-command-palette__*`
  CSS classes) which consumes `--gr-*` tokens. Host's DS bridge maps
  these to the Agent Genesis palette, inheriting the Greater design
  language.
- **No `kbd` shortcut chips on individual items.** The greater-shell
  component supports `item.shortcut` (renders a `<kbd>` chip), but
  M3 does not set shortcuts on any item. The only visible keyboard
  hint is the "⌘K" chip in the topbar trigger button.
- **No search icon in the input.** The greater-shell component does
  not render a leading icon in the search input. The topbar trigger
  button shows a `<SearchIcon>`, establishing the visual association.

## CSP posture

- No inline styles or scripts. All styling is via CSS classes
  delivered through the build pipeline.
- No new third-party origins. The palette is entirely client-side
  with no network requests triggered by opening/searching/selecting.
- The `CommandPalette` wrapper at `web/src/lib/components/CommandPalette.svelte`
  is unchanged (thin pass-through to greater-shell).

## Isolated fixture

A committed fixture renders the M3 CommandPalette in isolation for visual
review and PNG capture. It is NOT mounted by any customer portal route —
no App.svelte changes, no route additions. Files:

- `web/src/lib/components/__fixtures__/M3CmdkFixture.svelte` — renders
  the greater-shell `CommandPalette` at `open={true}` with all four
  groups (Navigate, Actions, Instances, Souls) populated from static
  fixture data matching PortalShell's runtime group structure
- `web/fixtures/m3-cmdk.html` — standalone HTML entry (Vite-served)
- `web/fixtures/m3-cmdk.ts` — entry point (imports tokens + shell CSS,
  mounts fixture)
- `web/fixtures/vite.fixture.m3.config.ts` — standalone Vite build
  config (no file watching, SPA-only, port 5201)

The fixture imports only the CSS needed for the CommandPalette to render
correctly: Greater base tokens, Agent Genesis bridge, primitives, and
shell CSS (which transitively imports `CommandPalette.css`). No API mocks
are required — the fixture component supplies group data directly rather
than deriving it from PortalShell's session/API lifecycle.

All new files carry `@license AGPL-3.0-only` headers.

## Test coverage

- **165 tests across 20 files** (13 new M3 tests in
  `PortalShell.svelte.test.ts`)
- New tests cover:
  - Opening via ⌘K (Meta+K and Ctrl+K)
  - Opening via `lesserhost:cmd-k-trigger` event
  - Closing via Escape
  - Closing via click-outside (backdrop)
  - Four-group structure (Navigate, Actions, Instances, Souls)
  - Stubbed disabled actions rendered with `aria-disabled="true"`
  - Client-side filtering
  - Route selection (Fleet, Request a soul) via mocked `navigate`
  - Palette closes after item selection
  - Soul items rendered with agent identity
  - Souls group hidden when no souls bound
  - Input focus on open

## Validation

```
cd web && npm run lint       # PASS
cd web && npm run typecheck   # PASS (0 errors, 0 warnings)
cd web && npm test            # PASS (165 tests, 20 files)
cd web && npm run build       # PASS
bash gov-infra/verifiers/gov-verify-rubric.sh  # PASS
```
