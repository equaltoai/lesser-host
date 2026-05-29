# M13 Souls Top-Level UI — Design Fidelity Evidence

## Source

- **Design fixture:** `/tmp/design-sNM/lesser-host/project/src/portal-pages-2.jsx` lines 171–249 (`PortalSouls`)
- **Design data:** `/tmp/design-sNM/lesser-host/project/src/data.jsx` lines 103–111 (`SOULS`)
- **Design spec summary:** `/home/aron/ai-workspace/codebases/equaltoai/agents/arch/project-40-portal-redesign-recovery/source-host-plan-2026-05-27/design-spec-summary.md` section *Portal — Souls top-level (/portal/souls)*
- **Issue:** [#547](https://github.com/equaltoai/lesser-host/issues/547)

## Implementation

### File changed

`web/src/pages/portal/Souls.svelte` — complete re-skin from legacy fallback framing to the Project 42 design roster, backed by the owner-scoped fleet roster bridge.

### Supporting changes

| File | Change |
|------|--------|
| `web/src/lib/ui.ts` | Added `Avatar` and `Tabs` re-exports from `src/lib/greater/primitives` (both were already in the vendored greater-components but not in the host UI barrel) |
| `internal/controlplane/handlers_portal_souls_roster.go` | Added `GET /api/v1/portal/souls/roster`, an owner-scoped read-only roster bridge that joins lesser-host soul registry rows with safe Lesser agent metadata |
| `internal/controlplane/handlers_portal_souls_roster_internal_test.go` | Regression coverage for owner-scoped roster enrichment, Lesser metadata failure degradation, all-time tip-event semantics, deduping, and not-found handling |
| `web/src/lib/api/soul.ts` | Added typed `soulListPortalRoster(token)` client and DTOs |
| `web/fixtures/m13-souls.html` | Standalone HTML fixture entry (1440px viewport) |
| `web/fixtures/m13-souls.ts` | Mock fetch interceptor with 7 realistic roster rows across all stage variants |
| `web/fixtures/vite.fixture.m13.config.ts` | Vite config for fixture page (port 5205, watch disabled) |
| `gov-infra/evidence/design-fidelity/m13-souls-top-ui/souls-roster.png` | 1440×900 screenshot of the rendered surface |
| `gov-infra/evidence/design-fidelity/m13-souls-top-ui/souls-roster.md` | This document |

### What was removed (legacy framing)

- `PageTitle` eyebrow: `"Souls"` → `"Agents · Souls"`
- `PageTitle` title: `"Legacy soul tools"` → `"Soul roster."`
- `PageTitle` description: `"Secondary lesser-host portal surface..."` → `"Soul-bound AI agents tied to your instances..."`
- Actions: `"Open legacy registration"` / `"Refresh"` → `"+ Request a soul"`
- `Callout` warning: `"Agent-first flow lives in Simulacrum"` → *removed entirely*
- Legacy card-based agent list → roster table with tabs
- `"No legacy portal agents found"` empty state → `"No soul-bound agents found for your instances."`

### Data sources

- Browser calls `GET /api/v1/portal/souls/roster` via `soulListPortalRoster(token)`.
- The control-plane handler enforces portal owner scope before reading instances/domains/soul registry rows.
- The handler enriches each row from the managed Lesser instance using `GET /api/v1/agents/{username}` for `agent_type`, `agent_version`, and `display_name`.
- Raw instance credentials are never returned to the browser. The Lesser metadata read is server-side and read-only.

### Stage derivation

The design uses stages (`graduated`, `in_review`, `requested`, `on_hold`) that don't map 1:1 to the API status vocabulary (`active`, `pending`, `suspended`, etc.). The implementation derives stages as follows:

| Design stage | Derivation |
|---|---|
| `graduated` | `self_description_version > 0` (profile published) |
| `in_review` | `status === 'active'` but `self_description_version` is 0 or absent |
| `requested` | `status === 'pending'` |
| `on_hold` | `status === 'suspended'` or `'self_suspended'` |

### Data columns

| Column | Source | Notes |
|--------|--------|-------|
| Soul | `agent.local_id` (display name), `agent.avatar?.image` (avatar src), `agent.domain` (handle) | Avatar is the Greater `Avatar` component; falls back to initials when no image |
| Instance | `instance.slug`, `instance.domain` | Owner-scoped instance resolved server-side from host instance/domain ownership |
| Stage | Derived (see above) | Rendered as a `Badge` with color: success/green (graduated), warning/amber (in review), error/red (on hold), gray (requested/other) |
| Anchor | `agent.anchor_assurance?.state` or `agent.minted_at` presence | "fresh" (immutable_onchain or minted), "pending" (hosted_offchain or not minted) |
| Model | `lesser_agent.agent_version` from Lesser `GET /api/v1/agents/{username}` | Falls back to explicit source status (`Unavailable`, `Not configured`, `Not found`) instead of a placeholder |
| Tip events · All time | `tips.received` from persisted `SoulAgentReputation.TipsReceived` | Renders the real all-time tip-event count; no fake currency/monthly value |

### Tabs

Three tabs mirroring the design: All, Graduated, In review. Each shows a count derived from the same `rows` array that populates the table — counts and table always agree.

### Right rail

1. **Roster status panel** — `DefinitionList` with counts (Total, Graduated, In review, Requested, On hold), all derived from the same `rows` array.
2. **Soul minting guidance panel** — Explanatory copy about Simulacrum. The "Open Simulacrum" button is **disabled** because no safe same-origin Simulacrum URL exists (documented deviation).

### States covered

| State | Behavior |
|-------|----------|
| Loading | `Spinner` + "Loading souls…" text |
| Error (non-401) | `Alert variant="error"` with message |
| 401 | `logout()` + `navigate('/login')` |
| Empty (0 agents) | `Panel` + "No soul-bound agents found" + "+ Request a soul" button |
| Filtered empty | "No souls match the selected filter." |
| Loaded (filtered table) | Roster table with rows, clickable to `/portal/souls/{agent_id}` |

### CSP posture

- No inline `style` attributes anywhere in the Svelte template
- No inline scripts
- No new third-party origins
- Styling through CSS classes and CSS custom properties (`--ds-*` tokens)
- SVG chevron uses `fill`/`stroke` presentation attributes only
- The `data-accent` attribute is used for CSS selectors (no inline values)

## Deviations from the design fixture

1. **Tips · May vs. tip events · all time** — The design fixture shows `Tips · May` as a current-month dollar amount. The current persisted reputation contract exposes `tips_received` as an all-time tip-event count; it does not expose a current-month dollar aggregate. The column is therefore labeled `Tip events · All time` and renders the real count. Fabricating monthly dollars would misrepresent the fleet data.

2. **Simulacrum button disabled** — The design shows an "Open Simulacrum" button with an arrow icon. No safe same-origin Simulacrum URL exists in the host SPA context. The button is rendered as `disabled` with explanatory copy rather than navigating to a dead or unsafe route.

3. **Tabs are single-filter** — The design fixture uses `Tabs` with All / Graduated / In review values. The implementation uses the same tab IDs with reactive counts but does not implement a multi-select or combined filter since the derived stage function covers the mapping from real API data.

4. **No "Filter" button** — The design fixture shows a `Filter` ghost button next to the tabs. Omitted because the tabs themselves serve as the filtering mechanism and there is no additional filter criteria to expose.

## Screenshot

**Path:** `gov-infra/evidence/design-fidelity/m13-souls-top-ui/souls-roster.png`

**Capture command:**
```bash
cd web && npx vite --config fixtures/vite.fixture.m13.config.ts --host 127.0.0.1 --port 5205 &
node -e "
  const puppeteer = require('/tmp/node_modules/puppeteer');
  const browser = await puppeteer.launch({ headless: 'new' });
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.goto('http://127.0.0.1:5205/fixtures/m13-souls.html', { waitUntil: 'networkidle0' });
  await page.waitForSelector('.souls-table tbody tr');
  await page.screenshot({ path: 'gov-infra/evidence/design-fidelity/m13-souls-top-ui/souls-roster.png' });
  await browser.close();
"
```

**Resolution:** 1440 × 900, PNG, 8-bit RGB, ~280 KB.

## Validation

| Check | Result |
|-------|--------|
| `npm run typecheck` (web/) | PASS — 0 errors, 0 warnings |
| `npm run lint` (web/) | PASS — clean |
| `npm test` (web/) | PASS — 21 files, 193 tests |
| `npm run build` (web/) | PASS — builds successfully |
| `go test ./internal/controlplane -count=1` | PASS — ok |
| No legacy framing (grep) | PASS — only legacy reference is the AGPL header comment documenting the re-skin |
| CSP compliance | PASS — no inline styles, no inline scripts, no third-party origins |
| OpenCode config tracked | PASS — `opencode.json`, `.opencode/`, `.codex/` all tracked (see PR body) |

## OpenCode / Codex configuration tracking

The branch includes the existing opencode/codex config that was already tracked on `origin/main`:

```
opencode.json
.opencode/.gitignore
.opencode/agents/steward.md
.opencode/skills/** (14 skill files)
.codex/build.sh
.codex/config.toml
.codex/skills/** (14 skill files)
.codex/stack/** (6 stack files)
.codex/steward.md
```

These files are unchanged from `origin/main`. `.opencode/package*.json` and `.opencode/node_modules/` are gitignored by `.opencode/.gitignore` and are not committed.
