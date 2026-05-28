# M2 Shell — Sidebar Fidelity Evidence

**Milestone:** M2 — PortalShell redesign
**Issue:** https://github.com/equaltoai/lesser-host/issues/536
**Branch:** `aron/portal-m2-shell`
**Date:** 2026-05-27

## Capture Method

`sidebar.png` was cropped from the full-shell capture (`full-shell.png`,
1440×900) using Python/PIL to isolate the full-height sidebar region.

```bash
python3 -c "
from PIL import Image
i = Image.open('gov-infra/evidence/design-fidelity/m2-shell/full-shell.png')
# Sidebar: left 420px (md width + right border), full 900px height
sidebar = i.crop((0, 0, 420, 900))
sidebar.save('gov-infra/evidence/design-fidelity/m2-shell/sidebar.png')
"
```

The source `full-shell.png` was captured via Google Chrome headless against
`web/fixtures/m2-shell.html` at 1440×900 viewport (see `full-shell.md`).

Result: `sidebar.png`, 420×900 RGB PNG.

## Sidebar Structure

The sidebar is rendered by `<Sidebar label="Primary navigation">` with:

### Header
- `BrandLockup edition="Customer portal"` — purple "&lt;" mark + "&lt;esser.host" wordmark + "CUSTOMER PORTAL" tracked edition label

### Navigation Body (`.portal-shell__nav`)

Four grouped sections with `<Eyebrow>` labels (M1 primitive, uppercase tracked):

1. **Overview** — Fleet link with HomeIcon
2. **Instances** — Per-instance dynamic entries:
   - Loading state: 3 skeleton rows (pulsing dot + text bar animation)
   - Empty state: "No instances yet" secondary text
   - Loaded: one `<Link>` per instance with status dot, slug label, optional AlertCircleIcon badge
   - Status dots use `data-status` attribute: ok (green), warning (amber), error (red), accent (gold)
3. **Agents** — Souls link with UsersIcon
4. **Settings** — Trust (CheckCircleIcon), Account (SettingsIcon), Billing (DollarSignIcon)

### Footer (`.portal-shell__sidebar-footer`)

- **User chip** (`.portal-shell__user-chip`):
  - Avatar initials (gradient background, white text, 32×32 circle)
  - Display name from `GET /api/v1/portal/me` `display_name` field
  - Role · auth-method subtext
  - LogOutIcon button (28×28, red on hover)
- **Operator console button** (conditional):
  - Visible only when `session.role` is `admin` or `operator`
  - ShieldIcon + "Operator console" label
  - Outlined variant, full width

## Design Deviations (Deliberate)

1. **Icons** use the Greater Feather icon set rather than the design fixture's specific icon SVGs. The semantic mapping is: Home (Fleet), Users (Souls/Agents), CheckCircle (Trust), Settings (Account), DollarSign (Billing), Shield (Operator console), LogOut (sign-out), Bell (notifications), Search (⌘K trigger).
2. **Status dot colors** use host's `--ds-success-500` / `--ds-warning-500` / `--ds-error-500` / `--ds-accent-500` tokens, matching the design's colour intent.
3. **Avatar** uses the existing gradient (purple → amber) from the M0/M1 design, not a user-uploaded image. The design shows an avatar with a photo; host has no avatar upload yet.
4. **Font sizes and spacing** consume host's `--ds-*` and `--gr-*` token system, which may differ by 1–2 px from the design fixture's static values.
5. **Sidebar width** is `md` (matching the Greater Shell default), approximately 256px. The design fixture may use a slightly different width.

## Validation

- Web lint: PASS (0 errors)
- Web typecheck: PASS (0 errors, 0 warnings)
- Web test: PASS (148 tests, 20 files)
- Web build: PASS (CSP integrity checks pass)
- Gov rubric: PASS (40/0/0)

## Per-instance entry behavior

- Async loading: `portalListInstances` is called in `onMount` → `loadSidebarData()`
- During loading: `instancesLoading === true` → 3 skeleton rows rendered
- After loading: skeleton removed, instance entries or "No instances yet" shown
- Status color mapping: `ok/active/running/provisioned` → ok (green), `warning/provisioning` → warning (amber), `error/failed` → error (red), `pending/in_progress/creating` → accent (gold)
- Alert badge: AlertCircleIcon shown when `status === 'warning' || status === 'error'`
- Click: navigates to `/portal/instances/{slug}`

## Component: PortalShell.svelte

Full source: `web/src/lib/components/PortalShell.svelte` (963 lines)
Tests: `web/src/lib/components/PortalShell.svelte.test.ts` (17 tests)
Vitest config: `web/vitest.config.ts` (updated with Greater aliases)
