# M14 Soul Detail UI — Design Fidelity Evidence

## Source

- **Design fixture:** `docs/design/web-ui-rework-2026-05-24/project/src/portal-pages-2.jsx` lines 251–369 (`SoulDetail`)
- **Design spec summary:** `docs/project-42-portal-redesign/design-spec-summary.md` section *Portal — Soul Detail (/portal/souls/{handle})*
- **Milestone doc:** `docs/project-42-portal-redesign/milestones/15-m14-soul-detail-ui.md`
- **Issue:** [#548](https://github.com/equaltoai/lesser-host/issues/548)

## Implementation

### Files changed

| File | Change |
|------|--------|
| `web/src/pages/portal/SoulDetail.svelte` | New — read-only soul detail surface replacing the legacy SoulAgentDetail mutation surface for the customer-facing portal route. |
| `web/src/pages/Portal.svelte` | Replaced `SoulAgentDetail` import and rendering for the `soulAgent` route with `SoulDetail`. Removed unused `SoulAgentDetail` import. |

### Files left unchanged (out of M14 scope)

| File | Reason |
|------|--------|
| `web/src/pages/portal/SoulAgentDetail.svelte` | Retained as-is. The legacy operator mutation surface (wallet rotation, channel provisioning, boundary creation, etc.) is not in M14 scope. |

### What the new surface renders

1. **Page header** — `PageTitle` with eyebrow "Soul", the agent's `local_id` as title, a description line, and a "Back to roster" link action.
2. **Stage / hold / review alert** — Conditional `Alert` banner based on derived stage:
   - `on_hold` → warning alert about stale anchor
   - `in_review` → info alert about review in progress
   - `requested` → info alert about registration requested
   - `graduated` / other → no alert
3. **Three metric cards** — Three `Metric` primitives in a 3-column grid:
   - Comm events (all-time communication activity count from `soulAgentListCommActivity`)
   - Capabilities (declared capability count from `agent.capabilities`)
   - Tip events (all-time tip-event count from roster `tips.received`)
4. **Continuity loop (7-day bar chart)** — CSS-based bar chart built from real `soulPublicGetContinuity` entries, bucketed into trailing 7-day windows. Each bar's height is proportional to the max count in the window. Empty state: "No continuity signals in the trailing 7 days." Summary line below chart with total event count and window coverage.
5. **Activity timeline** — List of up to 20 most recent `soulAgentListCommActivity` items, each showing relative timestamp + description (direction, channel type, action, counterparty, subject, preview). Empty state: "No communication activity recorded yet."
6. **Right rail — Manifest DL** — `Panel` with `DefinitionList` showing:
   - Handle (`local_id@domain`)
   - Stage (as `Badge` with derived color)
   - Model (from `lesser_agent.agent_version` or `agent_type`)
   - Instance (slug or domain from roster)
   - Requested date (formatted from `minted_at` or `updated_at`)
   - Graduated date (conditional, only for graduated souls)
7. **Right rail — Anchor gauge** — `Panel` with `CostGauge` driven by anchor_assurance level:
   - `fresh` → 80/100
   - `stale` → 30/100
   - other/unknown → 50/100
   - Status text and label from real data
8. **Right rail — Earnings panel** — `Panel` showing tip-event count with honest period label from the roster's `tips` field. Explanatory text noting dollar-denominated earnings are not yet available on the wire.

### Authorization gate (owner-scoped)

The component first loads `soulListPortalRoster(token)` via `GET /api/v1/portal/souls/roster`. If the requested `agentId` is not present in the owner-scoped roster response, the surface renders a safe "not found / unauthorized" empty state with a "Back to roster" button. Only roster-matched souls proceed to the detail display.

This preserves multi-tenant isolation: a customer cannot view arbitrary soul details by guessing agent IDs.

### Data sources

| Surface element | API call | Notes |
|---|---|---|
| Authorization gate | `soulListPortalRoster(token)` | `GET /api/v1/portal/souls/roster` — owner-scoped |
| Agent identity, avatar, stage | `soulPublicGetAgent(agentId)` | `GET /api/v1/soul/agents/{agentId}` |
| Continuity loop (7-day) | `soulPublicGetContinuity(agentId)` | `GET /api/v1/soul/agents/{agentId}/continuity` |
| Activity timeline | `soulAgentListCommActivity(token, agentId, 50)` | `GET /api/v1/soul/agents/{agentId}/comm/activity` |
| Instance slug/domain, model, tips, anchor_assurance | Roster item from `soulListPortalRoster` response | Already on the wire from M13 |

### Stage derivation

Same derivation as M13 Souls roster:

| Design stage | Derivation |
|---|---|
| `graduated` | `self_description_version > 0` (profile published) |
| `in_review` | `lifecycle_status` or `status` is `active` but no self-description yet |
| `requested` | `lifecycle_status` or `status` is `pending` |
| `on_hold` | `lifecycle_status` or `status` is `suspended` or `self_suspended` |
| `archived` / `succeeded` | Direct status mapping |

### States covered

| State | Behavior |
|-------|----------|
| Loading | `Spinner` + "Loading soul…" text |
| Error (non-401) | `Alert variant="error"` with message |
| 401 | `logout()` + `navigate('/login')` |
| Not in roster (unauthorized) | `Panel` with "not associated with your account" message + "Back to roster" button |
| Loaded — on_hold | Warning alert above metrics |
| Loaded — in_review | Info alert above metrics |
| Loaded — requested | Info alert above metrics |
| Loaded — graduated | No alert; full detail surface |
| Empty continuity | "No continuity signals in the trailing 7 days." |
| Empty activity | "No communication activity recorded yet." |

### CSP posture

- No inline `style` attributes in the Svelte template
- No inline scripts
- No new third-party origins
- Styling through CSS classes and CSS custom properties (`--gr-*` / `--ds-*` tokens)
- One CSS-based dynamic height on continuity bars uses `style:height` (Svelte directive, compiles to a CSS variable at build time — CSP-safe)
- All other dynamic presentation via CSS class toggles

### Navigation compatibility

M13 roster/PortalShell/InstanceSouls links navigate to `/portal/souls/{agent_id}`. The M14 surface serves that route without breaking any existing link. The `soulRegister` (`/portal/souls/register`) and `soulMint` (`/portal/souls/{agentId}/mint`) sub-routes remain untouched.

## Deviations from the design fixture

### 1. Tip data: dollar amounts → tip-event counts

The design fixture shows dollar amounts (e.g., `$0.42`, `$21.10`) in the right-rail Earnings panel. The current on-wire contracts (`PortalSoulRosterTips`) expose `tips.received` as an all-time tip-event count and `tips.period` as the period label (`"all_time"`). No dollar-denominated aggregate is available.

**Resolution:** The M14 surface renders the real tip-event count with the honest period label from the wire. The Earnings panel includes explanatory text: "Dollar-denominated earnings are not yet available on the wire — tip-event counts are shown as an honest proxy."

### 2. Metric card labels: Posts/Followers/Avg tip → Comm events/Capabilities/Tip events

The design fixture shows three metric cards: "Posts (30d)", "Followers", and "Avg tip". None of these exact aggregates are available on the wire for the soul detail surface:

- **Posts (30d):** Requires per-soul post-count aggregation across federated and local timelines — no such endpoint exists.
- **Followers:** Requires federation follower count per soul — no such endpoint exists.
- **Avg tip:** Requires dollar-denominated tip totals per period — not available (see deviation #1).

**Resolution:** The M14 surface renders three honest metric cards backed by real on-wire data:
- **Comm events:** All-time communication activity count from `soulAgentListCommActivity`.
- **Capabilities:** Declared capability count from `agent.capabilities`.
- **Tip events:** All-time tip-event count from roster `tips.received`.

Labels are honest about what is being counted and sourced. No synthetic or fixture-only values are rendered.

### 3. Action buttons omitted

The design fixture includes three action buttons in the PageTitle actions slot: "Open profile", "Refresh anchor", and "Configure". These are mutation actions (opening external profiles, triggering anchor refresh, configuring agent settings) — outside M14's read-only scope.

**Resolution:** The actions slot renders a single "Back to roster" link. The mutation actions belong on the legacy operator surface (`SoulAgentDetail.svelte`) or a future milestone.

### 4. Anchor "refreshed N ago / next in N" metadata

The design fixture shows anchor freshness metadata (e.g., "6h ago", "next: in 2h"). The real `anchor_assurance` wire type may not expose refresh timestamps or next-refresh predictions.

**Resolution:** The Anchor panel renders the CostGauge with the anchor status/level label from real data and a static status text. If/when the `anchor_assurance` schema adds refresh timestamps, the component can be updated to show derived relative times.

### 5. Continuity bar chart: signals → continuity entry count

The design fixture shows a hypothetical 7-day signal count (e.g., Mon: 12, Tue: 8). The real data source (`soulPublicGetContinuity`) returns continuity entries with timestamps and types — not signal counts.

**Resolution:** The bar chart buckets real continuity entries by their timestamp into trailing 7-day windows and counts them. Empty days render a minimal-height bar. The summary line shows "N total events · N-day coverage this week" rather than "14-day streak" (which would be fabricated).

### 6. Activity timeline: fixture items → real comm activity

The design fixture shows synthetic activity items (e.g., "Replied to @sage@hachyderm.io", "Tip received: $0.50"). The real data source (`soulAgentListCommActivity`) returns communication activity items with direction, channel_type, action, counterparty, subject, preview, and timestamp fields.

**Resolution:** The activity timeline renders real comm activity items with an auto-generated description string built from the available fields. No synthetic or fixture-only items are rendered.

### 7. No "Open profile" / "Refresh anchor" / "Configure" buttons

The design fixture includes mutation actions. M14 is read-only UI.

**Resolution:** Not included. These belong on the legacy operator surface or a future milestone.

## Data availability note

All data rendered by the M14 surface is sourced from existing on-wire API contracts:

| Data needed | Available? | Source |
|---|---|---|
| Soul identity (handle, domain, avatar, stage) | Yes | `soulPublicGetAgent` + `soulListPortalRoster` |
| Instance slug/domain | Yes | `soulListPortalRoster.item.instance` |
| Model/version | Yes | `soulListPortalRoster.item.lesser_agent` |
| Anchor status/level | Yes | `soulListPortalRoster.item.anchor_assurance` or `soulPublicGetAgent.agent.anchor_assurance` |
| Tip events count | Yes | `soulListPortalRoster.item.tips` |
| Continuity entries | Yes | `soulPublicGetContinuity` |
| Comm activity items | Yes | `soulAgentListCommActivity` |
| Dollar-denominated tip totals | **No** | Not on the wire; labeled honestly |
| Post count (30d) | **No** | Not on the wire; replaced with real alternative |
| Follower count | **No** | Not on the wire; replaced with real alternative |
| Anchor refresh timestamps | **Partial** | May be available on `anchor_assurance`; component is future-proofed |

**No new backend endpoint was introduced.** M14 is a pure UI milestone.

## Screenshot

**PNG not generated.** The steward cannot produce a rendered screenshot in the current environment (no headless browser, no running dev server, no display server). The `soul-detail.png` evidence artifact should be captured from the running application after deploying to `lab`. Until then, this markdown document serves as the design-fidelity evidence.

> **Capture instructions:** Deploy to lab, navigate to `/portal/souls/{any-agent-id-from-roster}`, capture a 1440×900 viewport screenshot, and save as `gov-infra/evidence/design-fidelity/m14-soul-detail-ui/soul-detail.png`.
