# M6 Instance Detail Overview UI — Design Fidelity Evidence

**Milestone:** M6 (Project 42 — `aron/portal-m6-instance-overview-ui`)
**Issue:** [#541](https://github.com/equaltoai/lesser-host/issues/541)
**Viewport:** 1440 × 900
**Evidence file:** `gov-infra/evidence/design-fidelity/m6-instance-overview-ui/overview.png`

## Scope delivered

| # | Item | Status |
|---|---|---|
| 1 | Four metric cards (MTD spend, Active users 30d, Posts 24h, Sig failures) | ✅ Layout present; values honest-unavailable |
| 2 | Stack card (Lesser / Body / MCP wiring with drift state) | ✅ Inline with M7 drift fields |
| 3 | 7-day Activity panel (posts + spend sparklines) | ✅ Layout present; no-activity state |
| 4 | Souls preview (top-5 bound souls) | ✅ Bound-souls fetch with M0.5 domain fix |
| 5 | Right rail Snapshot (Instance ID, Region, Created, Domain, Anchor) | ✅ Human dates / relative times |
| 6 | Right rail Operate (Refresh anchor, Export config, Open config) | ✅ Routed to existing safe behaviours |
| 7 | Right rail Owners (avatar, handle, role) | ✅ M7 owner enrichment surfaced |

## Deliberate data deviations

These are driven by the M5 fleet-data gap: the M5 milestone (PR #555) added
`active_users_30d`, `posts_24h`, `sig_fails_24h`, `spark_activity`, and
`spark_cost` to the Fleet list response but those fields are **not present**
on the instance detail response consumed by M6. The M5 PR is closed and its
commits are not ancestors of the M7 base branch.

Per the Project 42 guardrail: "If the route can meet the design using
existing main+M7 APIs and honest unavailable/empty states, proceed and
document any deliberate visual/data deviations."

| # | Surface | Design expectation | M6 rendering | Rationale |
|---|---|---|---|---|
| D1 | MTD spend metric | Real dollar value with budget context | `—` / "Not available" | M5 `mtd_spend` field not on instance detail response |
| D2 | Active users metric | 30-day peak daily count | `—` / "30-day peak daily" | M5 `active_users_30d` field not on instance detail response |
| D3 | Posts 24h metric | Last 24h federated post count | `—` / "Last 24 hours" | M5 `posts_24h` field not on instance detail response |
| D4 | Sig failures metric | Last 24h HTTP-sig failure count | `—` / "Last 24 hours" | M5 `sig_fails_24h` field not on instance detail response |
| D5 | Activity sparklines | 7-day spark_activity + spark_cost SVGs | Empty sparklines + "No activity data available" | M5 spark fields not on instance detail response |
| D6 | Operate actions | Enabled buttons for refresh anchor + export config | Disabled with honest tooltip explanations | Anchor refresh and config export require operator-approval workflows not yet built; no safe behaviour to route to |

## Deliberate visual deviations

| # | Surface | Design expectation | M6 rendering | Rationale |
|---|---|---|---|---|
| V1 | Stack card icons | Design shows per-component icons (server, bot, plug) | Icons omitted | Icons would require Greater icon component additions; scoped to M1-level primitive work or a future milestone |
| V2 | Owner avatar | Design shows avatar images | Initial letter in a circle | No avatar storage exists (`owner_avatar_hash` is intentionally empty per M7 handler: "no avatar storage exists"); consistent with M7 contract |
| V3 | Soul preview style | Design shows avatar + stage badge per soul | Shows local_id, status badge, domain path | Full per-soul styling (avatar variant images, stage badges) deferred to M10 (Instance Souls tab UI); preview is intentionally compact |
| V4 | Right rail sticky | Design shows sticky right rail | `position: sticky` implemented | Matches design; degrades to static on narrow viewports below 960px |

## Architectural decisions

| # | Decision | Reasoning |
|---|---|---|
| A1 | Stack panel inline in InstanceOverview, not separate StackCard component | The design's Stack card is part of the Overview layout. Using M7 drift fields from the instance DTO (lesser_drift, lesser_body_drift, mcp_drift) avoids a second HTTP call to the stack endpoint. The separate StackCard component remains available for standalone use. |
| A2 | Souls preview fetches via soulListMyAgents, not new endpoint | Per scope: "UI only. No new endpoints." Reuses existing M0.5-fixed bound-souls pattern (matched on {hosted_base_domain, managed_lesser_domain}). |
| A3 | Right rail is CSS grid, not PageFrame aside slot | The InstanceDetailShell already provides PageFrame without an aside slot. Adding an aside to the shell would affect all tabs (Cost, Config, Domains, Keys, Souls); the Overview right rail is Overview-specific. CSS grid within the tab content preserves isolation. |
| A4 | Operate actions are disabled with tooltip explanations | Scope requires "actions routed only to existing safe behaviours. Do not fake unsupported mutations; disable or explain unavailable actions." Anchor refresh and config export require operator-approval workflows not yet built. |

## Fixture ↔ runtime alignment

The fixture component `M6InstanceOverviewFixture.svelte` passes a complete mock
`InstanceResponse` to the real `InstanceOverview` component. The fixture
exercises:
- Drift state: Lesser=ok, Body=ok, MCP=wire-stale with drift_summary
- Owner enrichment: handle, role
- Soul anchor: anchored state with relative time
- All metric cards in honest-unavailable state
- Empty activity sparklines
- Bound souls (2 mock souls via aliased `soulListMyAgents` in fixture Vite config)

The fixture Vite config aliases `src/lib/api/soul` to a fixture mock that
returns 2 bound souls, so the Souls preview renders the design state in the
fixture capture rather than a fetch error.

## CSP, isolation, and governance

- ✅ Strict single-origin CSP preserved (no inline scripts, no inline styles, no new origins)
- ✅ Multi-tenant isolation preserved (all data sources enforce per-owner / per-slug ownership server-side)
- ✅ Trust-API instance-auth untouched
- ✅ All new Svelte files carry AGPL-3.0-only headers
- ✅ Gov-infra rubric: 40/40 verifiers pass
- ✅ Web: lint PASS, typecheck 0/0, 165 tests PASS, build PASS
