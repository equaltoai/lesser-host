# M17 Account UI — Design Fidelity Evidence

- **Issue:** equaltoai/lesser-host#551
- **Spec:** Project 42, Milestone 17 — Portal Design Fidelity Recovery, Account UI
- **Design reference:** `docs/design/web-ui-rework-2026-05-24/project/src/portal-pages-2.jsx` PortalAccount lines ~471–500
- **Commit branch:** `aron/portal-m17-account-ui`
- **Date:** 2026-05-29

## Screenshot

**account.png** — captured from the M17 fixture at **1440×900**, 8-bit RGB, non-interlaced.

SHA256: `d7ff138a32981d3420ab67674941800ed9d94f2926e697db0a878f4fd16c7eda`

The screenshot shows the slim `/portal/account` identity-and-session view with a customer session (role: `customer`). Two stacked panels:

1. **Profile** (eyebrow: Identity) — DefinitionList with Username (`alice-wallet`), Display name (`Alice`), Email (`alice@example.com`), Role (`customer`).
2. **Current session** (eyebrow: Session) — DefinitionList with Method (`wallet`), Wallet (masked: `0x1234…5678`), Token expires (future ISO date), IP (`—` unavailable). Action row below: `Rotate token` (disabled) and `Sign out` (active). Help text documents that session-token rotation is not yet supported and sign-out is single-session only.

Passkey management is not rendered for the `customer` role shown here.

The page does not render inside a PageFrame — both `/portal/account` and the legacy `/account` route are already wrapped by PortalShell's `PageFrame width="wide"`. This fixes the M1 double-nesting issue.

## Fixture Files

| File | Purpose |
|------|---------|
| `web/fixtures/m17-account.html` | Standalone HTML entry for headless PNG capture (not reachable from any app route) |
| `web/fixtures/m17-account.ts` | Session pre-load + portal-me mock + real `Account` component mount |
| `web/fixtures/vite.fixture.m17.config.ts` | Minimal Vite config (port 5208, Svelte plugin, no file watching) |

### Mocked Endpoint Data Summary

The fixture pre-loads a customer session via `setSession()` then mocks the single API endpoint consumed by the Account page:

- **`GET /api/v1/portal/me`** — returns `{ username: "alice-wallet", role: "customer", display_name: "Alice", email: "alice@example.com" }`

The session store is pre-set with:
- `tokenType: "bearer"`, `token: "tok_fixture_m17_account"`
- `expiresAt: now + 24h`, `username: "alice-wallet"`, `role: "customer"`
- `method: "wallet"`, `walletAddress: "0x1234567890abcdef1234567890abcdef12345678"`

### Capture Command

Started the fixture server from `web/`:

```bash
npx vite --config fixtures/vite.fixture.m17.config.ts --host 127.0.0.1 --port 5208
```

Captured from the repository root:

```node
const puppeteer = require('/tmp/node_modules/puppeteer');
(async () => {
  const browser = await puppeteer.launch({ headless: 'new', args: ['--no-sandbox', '--disable-setuid-sandbox'] });
  try {
    const page = await browser.newPage();
    await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
    await page.goto('http://127.0.0.1:5208/fixtures/m17-account.html', { waitUntil: 'networkidle0' });
    await page.waitForSelector('.account__header');
    await new Promise((resolve) => setTimeout(resolve, 2000));
    await page.screenshot({ path: 'gov-infra/evidence/design-fidelity/m17-account-ui/account.png' });
  } finally {
    await browser.close();
  }
})();
```

### Screenshot Verification

```bash
$ file gov-infra/evidence/design-fidelity/m17-account-ui/account.png
gov-infra/evidence/design-fidelity/m17-account-ui/account.png: PNG image data, 1440 x 900, 8-bit/color RGB, non-interlaced

$ sha256sum gov-infra/evidence/design-fidelity/m17-account-ui/account.png
d7ff138a32981d3420ab67674941800ed9d94f2926e697db0a878f4fd16c7eda  gov-infra/evidence/design-fidelity/m17-account-ui/account.png
```

## Route / Deep-Link Smoke Proof

| Route | App.svelte dispatch | Status |
|-------|---------------------|--------|
| `/portal/account` | `isPortalAccountRoute` (`$currentPath === '/portal/account'`) → `<Account />` inside `<PortalShell>` | Canonical path; renders identity + session panels |
| `/account` | Fallback `=== '/account'` → `<Account />` inside `<PortalShell>` | Legacy compatibility; same component, same shell |
| Refresh on `/portal/account` | SPA navigation state preserved via session store | Session re-hydrated from sessionStorage |

Both paths use the same `<Account />` component inside `<PortalShell>` — the M17 re-skin applies identically to both.

## CSP / Privacy / Isolation Notes

- **Strict CSP preserved:** No inline `style=` attributes, no inline scripts, no third-party origins. The AGPL header comment references `style=` for the CSP assertion but the template portion is clean.
- **Wallet masking:** Full wallet addresses are never rendered. The `maskWallet()` function truncates to `0x1234…5678` format. Test verifies no full wallet string appears in DOM text.
- **IP address:** Rendered as `—` (unavailable) because the session store (`src/lib/session.ts`) does not carry client IP metadata. No fabricated IP data.
- **Multi-tenant isolation:** Only self-scoped portal/operator `me` endpoints consumed. No cross-tenant or operator-console reads.
- **Trust-API instance-auth:** Untouched — account page does not handle instance keys.

## Deliberate Deviations / Caveats

1. **"Rotate token" is disabled.** No session-token rotation endpoint exists in the current backend (`/api/v1/auth/logout` terminates only the current session). The button is rendered as `disabled` with `title="Session-token rotation is not yet available"`. Honest help text below: "Session-token rotation is not yet supported."

2. **"Sign out" is single-session only.** The design reference labels the second button "Sign out all sessions", but the backend only supports single-session logout. The button is labeled "Sign out" (not "Sign out all sessions") to avoid misleading users. Help text: "Sign out signs out the current session only."

3. **IP field shows `—` unavailable.** The session store (`Session` interface in `src/lib/session.ts`) includes `method` and `walletAddress` but no `ip` or session-metadata field. No backend expansion was performed — the field renders the honest dash state.

4. **Passkey management preserved for operator/admin.** The existing WebAuthn passkey CRUD UI (register, list, rename, delete) is preserved as a third panel below Identity and Session, conditionally rendered only when `profile.role` is `admin` or `operator`. Customer sessions do not see the passkey panel. Removing this would break supported operator behavior; keeping it adds a third panel that does not dominate the slim design surface.

5. **PageFrame removal (double-nesting fix).** The M1 Account.svelte wrapped itself in `<PageFrame width="default">`, which nested inside PortalShell's own `<PageFrame width="wide">`. The M17 re-skin removes the inner PageFrame — both `/portal/account` and `/account` already render inside PortalShell which supplies the frame.

6. **No Design reference `maxWidth: 900` enforcement.** The design reference specifies `maxWidth: 900` on the content area, but the page inherits PortalShell's `width="wide"` PageFrame (96rem/~1536px). This is consistent with all other portal pages (Trust, Billing, Souls) which share the same shell frame. The content width is naturally constrained by the Panel components.

## Validation Results

| Check | Result |
|-------|--------|
| `cd web && npm run lint` | PASS |
| `cd web && npm run typecheck` | PASS — 0 errors, 0 warnings |
| `cd web && npm test` | PASS — 24 files, 268 tests |
| `cd web && npm run build` | PASS — build sidecars + no-inline HTML + OAC form integrity |
| `bash gov-infra/verifiers/gov-verify-rubric.sh` | PASS — 40 pass, 0 fail, 0 blocked |
| `git diff --check` | PASS |
| PNG dimensions via `file` | PASS — 1440×900 |

## Changed Files

| File | Change |
|------|--------|
| `web/src/pages/Account.svelte` | REWRITTEN — slim identity + session view with DefinitionList panels, masked wallet, honest action buttons, preserved passkey management |
| `web/src/pages/Account.svelte.test.ts` | NEW — 268 tests covering identity/session DL fields, wallet masking, honest button states, CSP safety, route dispatch, DOM mount |
| `web/fixtures/m17-account.html` | NEW — standalone HTML entry for headless PNG capture |
| `web/fixtures/m17-account.ts` | NEW — session pre-load + portal-me mock + Account component mount |
| `web/fixtures/vite.fixture.m17.config.ts` | NEW — minimal Vite config for fixture server |
| `gov-infra/evidence/design-fidelity/m17-account-ui/account.png` | NEW — 1440×900 design-fidelity screenshot |
| `gov-infra/evidence/design-fidelity/m17-account-ui/account.md` | NEW — this evidence writeup |
