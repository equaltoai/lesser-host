# M9 Instance Configuration UI — Design Fidelity Evidence

**Milestone:** M9 (Project 42 — `aron/portal-m9-instance-config-keys-ui`)
**Issue:** [#543](https://github.com/equaltoai/lesser-host/issues/543)
**Viewport:** 1440 × 900
**Evidence file:** `gov-infra/evidence/design-fidelity/m9-instance-config-keys-ui/configuration.png`

## Scope delivered

| # | Item | Status |
|---|---|---|
| 1 | Tab label "Config" → "Configuration" (route segment preserved as `/config`) | ✅ Renamed in InstanceDetailShell; route/deep-link compatible |
| 2 | Instance identity section: Display name, Description, Default visibility, Reg open | ✅ Layout present; Display name derived from slug; others "—" (unavailable) |
| 3 | Federation policy section: 5 ConfigToggle rows | ✅ Layout present; all toggles disabled (not supported by current config API) |
| 4 | Rate limits section: Posts/hour, Inbox delivery, Search QPS, Outbound HTTP | ✅ Layout present; all values "—" (unavailable) |
| 5 | Normalise toggle sizing (audit P3.3) | ✅ `.config__toggle` class normalised; Federation ConfigToggle rows use consistent `.config__toggle-row` sizing |
| 6 | Existing Features/Moderation/AI mutation panels preserved | ✅ Below the three design sections; save/refresh/validation/ack intact |

## Deliberate data deviations

| # | Surface | Design expectation | M9 rendering | Rationale |
|---|---|---|---|---|
| D1 | Display name | Instance's display name field | Derived from slug: `slug.replace(/-/g, ' ').titlecase()` | No `display_name` field on `InstanceResponse` |
| D2 | Description, Default visibility, Reg open | Real values from instance config | `—` (secondary-coloured "—") | No corresponding fields on `InstanceResponse` or config API |
| D3 | Federation toggles (5 rows) | Interactive on/off switches | All 5 toggles `disabled={true}`, showing `checked={false}` | No federation-policy fields in `UpdateInstanceConfigRequest`; none of accept-federation, quote-posts, auto-thread-sync, AI-moderation-hint, or public-webfinger are mutable through the current config API |
| D4 | Rate limits (4 rows) | Real numeric values | All 4 values `—` | No rate-limit fields on `InstanceResponse`; rate-limit configuration is not exposed through the portal config API |

## Deliberate visual deviations

| # | Surface | Design expectation | M9 rendering | Rationale |
|---|---|---|---|---|
| V1 | Panel eyebrow ("Identity", "Federation", "Limits") | Design's Panel has an `eyebrow` prop | Rendered as uppercase Text inside a `<div>` wrapper above the Panel title | Greater-shell Panel component does not expose an `eyebrow` prop; visual approximation is close |
| V2 | ConfigToggle design pattern | React-style `<ConfigToggle label sub on>` component | Inline `<div class="config__toggle-row">` with Switch + label + sub Text | Svelte pattern; no need for a separate component given five static rows |
| V3 | Federation toggle states | Some toggles might be "on" in a real instance | All displayed as off (disabled) | Honest reflection of no backend support; avoids misleading "on" states that cannot be changed |

## CSP, isolation, and governance

- ✅ Strict single-origin CSP preserved (no inline scripts, no inline styles, no new origins)
- ✅ Multi-tenant isolation preserved (all data sources enforce per-owner / per-slug ownership server-side)
- ✅ Trust-API instance-auth untouched
- ✅ All new Svelte/test files carry AGPL-3.0-only headers
- ✅ Gov-infra rubric: 40/40 verifiers pass
- ✅ Web: lint PASS, typecheck 0/0, 205 tests PASS, build PASS (CSP clean, OAC form integrity)
