# M2 Shell — Topbar Fidelity Evidence

**Milestone:** M2 — PortalShell redesign
**Issue:** https://github.com/equaltoai/lesser-host/issues/536
**Branch:** `aron/portal-m2-shell`
**Date:** 2026-05-27

## Capture Method

`topbar.png` was cropped from the full-shell capture (`full-shell.png`,
1440×900) using Python/PIL to isolate the full-width topbar region.

```bash
python3 -c "
from PIL import Image
i = Image.open('gov-infra/evidence/design-fidelity/m2-shell/full-shell.png')
# Topbar: full 1440px width, top 96px (chrome + separator)
topbar = i.crop((0, 0, 1440, 96))
topbar.save('gov-infra/evidence/design-fidelity/m2-shell/topbar.png')
"
```

The source `full-shell.png` was captured via Google Chrome headless against
`web/fixtures/m2-shell.html` at 1440×900 viewport (see `full-shell.md`).

Result: `topbar.png`, 1440×96 RGB PNG.

## Topbar Structure

The topbar is rendered by `<Topbar variant="default" sticky>` with three slots:

### Start slot — Breadcrumbs

- Uses Greater `Breadcrumb` component with `items={breadcrumbs}` derived from `$currentPath`
- Breadcrumb mapping:
  - `/portal` → Portal (current)
  - `/portal/fleet` → Portal (current)
  - `/portal/instances` → Portal / Instances (current)
  - `/portal/instances/{slug}` → Portal / Instances / {slug} (current)
  - `/portal/instances/{slug}/overview` → Portal / Instances / {slug} (current)
  - `/portal/instances/{slug}/cost` → Portal / Instances / {slug} / cost (current)
  - `/portal/billing` → Portal / Billing (current)
  - `/portal/souls` → Portal / Souls (current)
  - `/portal/trust` → Portal / Trust (current)
  - `/portal/account` → Portal / Account (current)

### Center slot — ⌘K Trigger

- Pill-shaped button (`.portal-shell__cmdk-trigger`)
- Contains: SearchIcon (16×16) + "Search instances, souls, jobs…" label + ⌘K keyboard chip
- Click dispatches `lesserhost:cmd-k-trigger` CustomEvent on `window` (bubbles: true)
- The button is intentionally inert in M2 — M3 will wire the command palette overlay
- Hidden below 58rem viewport width
- The existing ⌘K keybinding (in onMount) still opens the legacy CommandPalette (`paletteOpen` toggle)

### End slot — Notification Bell

- Bell button (`.portal-shell__bell-btn`, 36×36) with BellIcon
- Toggles a popover (`.portal-shell__bell-popover`, 18rem wide)
- Popover content: "Notifications coming soon." in secondary text
- Click-away backdrop (`.portal-shell__bell-backdrop`) dismisses the popover
- Popover has `role="dialog"`, `aria-label="Notifications"`, `tabindex="-1"`
- Escape key closes the popover (via onkeydown handler)
- Placeholder only — no notification backend wiring exists

## Design Deviations (Deliberate)

1. **Breadcrumb separator** uses the Greater `Breadcrumb` component's default separator (chevron or slash), not the design fixture's specific arrow.
2. **⌘K trigger styling** matches the design's pill shape with search icon + text + ⌘K chip. Colors use `--ds-*` tokens.
3. **Bell icon** is the Feather `bell.svg` from the Greater icon set. The design uses a custom bell SVG; the semantics are identical.
4. **No notification count badge** — the bell is a bare icon. A count badge would require backend wiring (future milestone).
5. **Popover width** is 18rem; the design may use a slightly different width.

## CSP Posture

- No inline styles, no inline event handlers, no third-party origins
- All event handlers are Svelte `onclick` / `onkeydown` directives
- The ⌘K trigger dispatches a CustomEvent (no eval, no inline JS)
- Bell popover uses Svelte state (`bellOpen`) + CSS visibility

## Validation

- Web lint: PASS (0 errors)
- Web typecheck: PASS (0 errors, 0 warnings)
- Web test: PASS (148 tests, 20 files)
- Web build: PASS (CSP integrity checks pass)
- Gov rubric: PASS (40/0/0)
