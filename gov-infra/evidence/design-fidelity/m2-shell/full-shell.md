# M2 Shell — Full-Shell Fidelity Evidence

**Milestone:** M2 — PortalShell redesign
**Issue:** https://github.com/equaltoai/lesser-host/issues/536
**Branch:** `aron/portal-m2-shell`
**Date:** 2026-05-27

## Capture Method

Captured via Google Chrome headless against the M2 fixture page
(`web/fixtures/m2-shell.html`), which mounts PortalShell in isolation with
pre-seeded session data and mock API responses at 1440×900 viewport.

The fixture entrypoint (`web/fixtures/m2-shell.ts`) imports the full
Greater Shell CSS bundle (`src/lib/styles/greater/shell.css` plus
host-platform.css, operator-chrome.css, and app.css) so the captured
evidence reflects the actual Greater shell layout — topbar full-width,
sidebar as a left column with grouped nav links, content in the main area.

```bash
# Serve fixture in one terminal
cd web && npx vite --config fixtures/vite.fixture.m2.config.ts --port 5200

# Capture in another
/usr/bin/google-chrome --headless --disable-gpu \
  --window-size=1440,900 \
  --screenshot=gov-infra/evidence/design-fidelity/m2-shell/full-shell.png \
  http://localhost:5200/fixtures/m2-shell.html
```

Result: `full-shell.png`, 1440×900 RGB PNG (208,879 bytes), non-interlaced.

## Full-Shell Layout (1440 × 900 viewport)

The shell uses the Greater `Shell` component grid:

```
┌──────────────────────────────────────────────────────────┐
│ Topbar (sticky, auto height)                             │
│ [Breadcrumbs]  [⌘K Search trigger…]  [bell]             │
├────────────────┬─────────────────────────────────────────┤
│ Sidebar (md)   │ Main Content Area                       │
│                │                                         │
│ [BrandLockup]  │ PageFrame (wide)                        │
│                │   ┌─────────────────────────────────┐   │
│ OVERVIEW       │   │ Content surfaces render here     │   │
│  🏠 Fleet      │   │ (PortalFleet, InstanceDetail,   │   │
│  💲 Cost &     │   │  Trust, Account, Billing, etc.)  │   │
│    billing     │   │                                  │   │
│  ✓  Trust      │   │                                  │   │
│                │   │                                  │   │
│ INSTANCES      │   │                                  │   │
│  ● alpha       │   │                                  │   │
│  ● beta  ⚠    │   │                                  │   │
│                │   │                                  │   │
│ AGENTS         │   └─────────────────────────────────┘   │
│  👥 Souls      │                                         │
│                │                                         │
│ SETTINGS       │                                         │
│  ⚙  Account    │                                         │
│                │                                         │
│ ───────────────│                                         │
│ [🛡 Operator]  │  (admin/operator only)                  │
│ [Avatar] Alice │                                         │
│ customer·wallet│                                         │
│ [🚪 sign out]  │                                         │
└────────────────┴─────────────────────────────────────────┘
```

## Component Hierarchy

```
App.svelte
 └─ PortalShell.svelte
     ├─ Shell (mainLabel="Customer portal", sidebarPlacement="left", sidebarWidth="md")
     │   ├─ Topbar (variant="default", sticky)
     │   │   ├─ start: Breadcrumb (items={breadcrumbs})
     │   │   ├─ center: button.portal-shell__cmdk-trigger
     │   │   │   ├─ span.portal-shell__cmdk-icon > SearchIcon
     │   │   │   ├─ span: "Search instances, souls, jobs…"
     │   │   │   └─ span.portal-shell__kbd: "⌘K"
     │   │   └─ end: div.portal-shell__bell-wrapper
     │   │       ├─ button.portal-shell__bell-btn > BellIcon
     │   │       └─ div.portal-shell__bell-popover (conditional)
     │   ├─ Sidebar (label="Primary navigation")
     │   │   ├─ header: BrandLockup (edition="Customer portal")
     │   │   ├─ body: div.portal-shell__nav
     │   │   │   ├─ div.portal-shell__nav-section > Eyebrow("Overview")
     │   │   │   ├─ Link(to="/portal") > span.icon > HomeIcon + "Fleet"
     │   │   │   ├─ Link(to="/portal/billing") > DollarSignIcon + "Cost & billing"
     │   │   │   ├─ Link(to="/portal/trust") > CheckCircleIcon + "Trust"
     │   │   │   ├─ div.portal-shell__nav-section > Eyebrow("Instances")
     │   │   │   ├─ [#if loading] skeleton rows [else] instance entries
     │   │   │   ├─ div.portal-shell__nav-section > Eyebrow("Agents")
     │   │   │   ├─ Link(to="/portal/souls") > UsersIcon + "Souls"
     │   │   │   ├─ div.portal-shell__nav-section > Eyebrow("Settings")
     │   │   │   └─ Link(to="/portal/account") > SettingsIcon + "Account"
     │   │   └─ footer: div.portal-shell__sidebar-footer
     │   │       ├─ Button (Operator console, conditional; above user chip)
     │   │       └─ div.portal-shell__user-chip
     │   │           ├─ span.portal-shell__avatar (initials)
     │   │           ├─ span.portal-shell__user-copy
     │   │           │   ├─ Text(displayHandle)
     │   │           │   └─ Text(roleSubtext)
     │   │           └─ button.portal-shell__logout-btn > LogOutIcon
     │   └─ main: PageFrame(width="wide")
     │       └─ {@render children()}  ← content surfaces
     └─ CommandPalette (legacy, preserved for M2)
     └─ div.portal-shell__bell-backdrop (click-away)
```

## Design Deviations Summary

| Category | Design Fixture | Implementation | Reason |
|---|---|---|---|
| Icons | Custom SVG icons | Greater Feather icon set | Feather is the established icon set for host web/ |
| Avatar | User photo | Initials with gradient | No avatar upload feature exists |
| Command palette | Fully wired ⌘K overlay | Inert trigger button + legacy palette | M3 owns the new palette; M2 is chrome-only |
| Notifications | Bell with count badge | Bell placeholder popover | No notification backend exists |
| Breadcrumb separator | Custom arrow | Greater default (chevron) | Uses existing component |
| Sidebar width | Design-specific | `md` preset (~256px) | Uses Greater Shell token system |
| Status dot colors | Design hex values | `--ds-{success,warning,error,accent}-500` tokens | Uses host token system |
| Souls numeric badge | Badge count next to Souls | Deferred to M13/M14 | No real `requested`/`in_review` count in M2; faking it would be dishonest |
| Provisioning status | Amber/warning dot | Gold/accent dot | Provisioning is a transient operational state, not a warning |

## Route Preservation (M0 non-regression)

All portal namespace routes continue to work:
- `/portal` → PortalFleet
- `/portal/fleet` → PortalFleet (alias)
- `/portal/instances/{slug}` → InstanceDetail
- `/portal/instances/{slug}/cost` → InstanceCost
- `/portal/instances/{slug}/config` → InstanceConfig
- `/portal/instances/{slug}/budgets` → InstanceBudgets
- `/portal/instances/{slug}/usage` → InstanceUsage
- `/portal/instances/{slug}/domains` → InstanceDomains
- `/portal/instances/{slug}/keys` → InstanceKeys
- `/portal/instances/{slug}/souls` → InstanceSouls
- `/portal/trust`, `/portal/trust/*` → Trust
- `/portal/account` → Account
- `/portal/billing` → Billing
- `/portal/souls` → Souls

Legacy paths (`/trust`, `/account`) still route to the same components.

## Validation

- Web lint: PASS (0 errors)
- Web typecheck: PASS (0 errors, 0 warnings)
- Web test: PASS (151 tests across 20 files)
- Web build: PASS (CSP integrity checks pass)
- Gov rubric: PASS (40/0/0)
