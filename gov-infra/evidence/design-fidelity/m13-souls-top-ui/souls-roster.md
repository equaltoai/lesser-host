# M13 Souls Top-Level UI — Design Fidelity Evidence

## Source

- **Design fixture:** `/tmp/design-sNM/lesser-host/project/src/portal-pages-2.jsx` lines 171–249 (`PortalSouls`)
- **Design data:** `/tmp/design-sNM/lesser-host/project/src/data.jsx` lines 103–111 (`SOULS`)
- **Design spec summary:** `/home/aron/ai-workspace/codebases/equaltoai/agents/arch/project-40-portal-redesign-recovery/source-host-plan-2026-05-27/design-spec-summary.md` section *Portal — Souls top-level (/portal/souls)*
- **Issue:** [#547](https://github.com/equaltoai/lesser-host/issues/547)

## Implementation

### File changed

`web/src/pages/portal/Souls.svelte` — complete re-skin from legacy fallback framing to the Project 42 design roster.

### Supporting changes

| File | Change |
|------|--------|
| `web/src/lib/ui.ts` | Added `Avatar` and `Tabs` re-exports from `src/lib/greater/primitives` (both were already in the vendored greater-components but not in the host UI barrel) |
| `web/fixtures/m13-souls.html` | Standalone HTML fixture entry (1440px viewport) |
| `web/fixtures/m13-souls.ts` | Mock fetch interceptor with 7 realistic soul agents across all stage variants |
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

- `GET /api/v1/soul/agents/mine` via `soulListMyAgents(token)` — the existing owner-scoped endpoint
- No new backend endpoints, no backend schema changes

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
| Instance | `agent.domain` | Instance domain |
| Stage | Derived (see above) | Rendered as a `Badge` with color: success/green (graduated), warning/amber (in review), error/red (on hold), gray (requested/other) |
| Anchor | `agent.anchor_assurance?.state` or `agent.minted_at` presence | "fresh" (immutable_onchain or minted), "pending" (hosted_offchain or not minted) |
| Model | *Not available* | Renders `—` (deviation, see below) |
| Tips · All time | `reputation.tips_received` (total) | Renders total tips as `$X.XX` or `—` when no reputation data |

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

1. **Model column** — The design fixture shows per-soul model values (`sonnet-4.5`, `haiku-4.5`). The `SoulAgentIdentity` API type does not carry a model field; no model information is stored per soul in the current data model. Renders `—`.

2. **Tips · May vs. Tips · All time** — The design fixture shows `Tips · May` (current month tips). The `SoulAgentReputation` type provides `tips_received` as an all-time total — no monthly breakdown is available. The column header reads `Tips · All time` and the value is `$X.XX` from the total. This is a deliberate deviation; fabricating monthly values would misrepresent the data.

3. **Simulacrum button disabled** — The design shows an "Open Simulacrum" button with an arrow icon. No safe same-origin Simulacrum URL exists in the host SPA context. The button is rendered as `disabled` with explanatory copy rather than navigating to a dead or unsafe route.

4. **Column header "Tips · May" → "Tips · All time"** — per deviation 2 above.

5. **Tabs are single-filter** — The design fixture uses `Tabs` with All / Graduated / In review values. The implementation uses the same tab IDs with reactive counts but does not implement a multi-select or combined filter since the derived stage function covers the mapping from real API data.

6. **No "Filter" button** — The design fixture shows a `Filter` ghost button next to the tabs. Omitted because the tabs themselves serve as the filtering mechanism and there is no additional filter criteria to expose.

## Screenshot

**Path:** `gov-infra/evidence/design-fidelity/m13-souls-top-ui/souls-roster.png`

**Capture command:**
```bash
cd web && npx vite --config fixtures/vite.fixture.m13.config.ts --port 5205 &
node -e "
  const puppeteer = require('/tmp/node_modules/puppeteer');
  const browser = await puppeteer.launch({ headless: 'new' });
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.goto('http://localhost:5205/fixtures/m13-souls.html', { waitUntil: 'networkidle0' });
  await page.waitForSelector('.souls-table tbody tr');
  await page.screenshot({ path: 'gov-infra/evidence/design-fidelity/m13-souls-top-ui/souls-roster.png' });
  await browser.close();
"
```

**Resolution:** 1440 × 900, PNG, 8-bit RGB, ~273 KB.

## Validation

| Check | Result |
|-------|--------|
| `npm run typecheck` (web/) | PASS — 0 errors, 0 warnings |
| `npm run lint` (web/) | PASS — clean |
| `npm test` (web/) | PASS — 21 files, 193 tests |
| `npm run build` (web/) | PASS — builds successfully |
| `go test ./internal/controlplane/ -count=1` | PASS — ok (Go unchanged; ran for confidence) |
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
