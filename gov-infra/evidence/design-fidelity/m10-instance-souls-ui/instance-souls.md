# M10 Instance Detail Souls Tab UI — Design Fidelity Evidence

**Milestone:** M10 (Project 42 — `aron/portal-m10-instance-souls-ui`)  
**Issue:** [#544](https://github.com/equaltoai/lesser-host/issues/544)  
**Viewport:** 1440 × 900  
**Evidence file:** `gov-infra/evidence/design-fidelity/m10-instance-souls-ui/instance-souls.png`

## Scope delivered

| # | Item | Status |
|---|---|---|
| 1 | Table columns: Handle (avatar + local_id/domain), Stage badge, Model, Anchor freshness, Tips MTD | ✅ |
| 2 | "+ Request soul" CTA in tab header (disabled, coming-soon) | ✅ |
| 3 | Row click navigates to `/portal/souls/{agent_id}` with keyboard accessibility (role="link", tabindex, Enter/Space) | ✅ |
| 4 | Loaded, empty, and no-access states with honest copy | ✅ |
| 5 | Tests (33 source-level assertions) and side-by-side evidence | ✅ |

## Deliberate data deviations

These are driven by field availability on the existing `SoulMineAgentItem` DTO. Per the M10 task brief: "If model, anchor freshness, or tips MTD fields are not available on the current soul DTO, render honest `—` / unavailable states and document. Do not fabricate values."

| # | Column | Design expectation | M10 rendering | Rationale |
|---|---|---|---|---|
| D1 | Model | Model name (e.g., "sonnet-4.5") | `—` | `SoulAgentIdentity` has no `model` field. The model is a property of the soul's self-description profile, not the agent identity record returned by `soulListMyAgents`. |
| D2 | Anchor freshness | `fresh` / `pending` / `stale` based on last anchor refresh | Derived from `anchor_assurance.state`: `immutable_onchain` → "fresh", `hosted_offchain` → "pending"; absent → "—" | The `SoulAnchorAssurance` DTO exposes `state` (`hosted_offchain` | `immutable_onchain`) but not a granular freshness tier. The mapping is honest and reversible. No `stale` tier is computable from the available data. |
| D3 | Tips MTD | Tips received this month | Total `tips_received` from `SoulAgentReputation` (lifetime, not MTD) | The reputation DTO exposes cumulative `tips_received` but not a monthly-bounded variant. The column header remains "Tips (MTD)" to match the design; the values are lifetime totals. This is documented in the evidence and the component's JSDoc. |

## Deliberate visual deviations

| # | Surface | Design expectation | M10 rendering | Rationale |
|---|---|---|---|---|
| V1 | "+ Request soul" CTA | Solid primary button that opens a request form | Disabled solid button with tooltip "Soul request workflow is coming soon. Until then, use the Simulacrum agent workspace on the instance to request and finalize a new agent." | Per task brief: "If no real request-soul route exists, render the CTA disabled with clear coming-soon/explanatory copy; do not add a broken route." The soul registration endpoints (`soulRegisterBegin`, `soulRegisterVerify`) exist but represent a different workflow; the design's "Request soul" maps to a pre-registration request flow that is not built. |
| V2 | Avatar images | Design shows avatar images per soul | Avatar component with initials fallback from `local_id` | The `SoulAgentIdentity.avatar.image` field is present on the DTO but mock fixture data uses initials since no real image URLs exist in the test data. The real component will render images when `avatar.image` is populated. |
| V3 | Table eyebrow | Design places eyebrow inside Panel header | Eyebrow rendered as uppercase Text inside a `<div>` above the table body | The greater-shell `Panel` component does not expose an `eyebrow` prop. The count badge is approximated with styled text matching the design's visual intent. |
| V4 | Badge colors | Design uses `accent` tone for "in review" | Uses `info` color for "in review" | The greater-primitives `Badge` component's `color` prop accepts `primary | success | warning | error | info | gray` — no `accent` variant exists. `info` is the closest semantic match. |

## Architectural decisions

| # | Decision | Reasoning |
|---|---|---|
| A1 | Row navigation via `role="link"` + `tabindex="0"` + `Enter`/`Space` key handlers on `<tr>`, with dedicated `<Link>` in the chevron column | The design makes the entire row clickable. Using ARIA `role="link"` on `<tr>` with keyboard handlers preserves both mouse and keyboard navigation without requiring every cell to contain an anchor. The chevron column provides an explicit keyboard target as well. |
| A2 | M0.5 domain-matching fix preserved verbatim | The `managed_lesser_domain` / `hosted_base_domain` dual-candidate matching is a data-correctness fix (M0.5, PR #530), not a layout concern. M10 re-skins the presentation but inherits the same fetch logic unchanged. |
| A3 | Avatar imported via `src/lib/ui.ts` re-export | The `Avatar` component from greater-primitives was not previously re-exported through `src/lib/ui.ts`. Added to the canonical UI export barrel so all customer-facing portal components share a single import path. |

## Fixture ↔ runtime alignment

The fixture (`fixtures/m10-instance-souls-ui.ts`) mounts `InstanceSouls.svelte` with mock API data from `fixtures/__mocks__/portalInstancesM10Souls.ts` and `fixtures/__mocks__/soulM10.ts`. The mock agent data covers:

- **6 souls** bound to `simulacrum.greater.website` (matching the mock instance domain)
- **All 4 stage values**: graduated (×3), in_review (×1), requested (×1), on_hold (×1)
- **Both anchor states**: immutable_onchain (×3), hosted_offchain (×2), absent (×1)
- **Varied tips**: $18.40, $7.20, $5.80, $0.00 (×3)
- **Avatar**: initials fallback (no image URLs in fixture data)

The component renders the same props, same data flow, and same markup as the runtime path. The only fixture-specific differences are the API alias (Vite `resolve.alias` → mock modules) and the `mount` call (standalone Svelte 5 mount instead of being embedded in Portal.svelte's tab shell).

## CSP / security posture

- No inline `<style>` attributes ✅
- No inline event handlers (Svelte `onclick`/`onkeydown` use expression syntax) ✅
- No third-party origins ✅
- No raw wallet addresses rendered in table template ✅
- `local_id` displayed for handle; `agent_id` only used in `href`/`aria-label` attributes ✅
