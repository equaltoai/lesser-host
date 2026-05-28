# M9 Instance Keys UI — Design Fidelity Evidence

**Milestone:** M9 (Project 42 — `aron/portal-m9-instance-config-keys-ui`)
**Issue:** [#543](https://github.com/equaltoai/lesser-host/issues/543)
**Viewport:** 1440 × 900
**Evidence file:** `gov-infra/evidence/design-fidelity/m9-instance-config-keys-ui/keys.png`

## Scope delivered

| # | Item | Status |
|---|---|---|
| 1 | Table layout with Token / Scopes / Created / Last used columns | ✅ HTML table with `.keys__table` class |
| 2 | Formatted/masked token IDs (prefix + "..." + suffix) | ✅ `maskKeyId()`: first 8 chars + "..." + last 4 chars |
| 3 | Copy chip for non-secret IDs (CopyButton copies full key_id) | ✅ `CopyButton` per row; copies `k.id` (full token identifier, never raw key) |
| 4 | Scopes column rendered | ✅ `—` for all keys (scope metadata not available on `InstanceKeyListItem`) |
| 5 | Created / Last used dates formatted | ✅ `toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })`; zero-dates render as `—` |
| 6 | Revoke action per row (non-revoked keys) | ✅ Ghost button per row; requires `window.confirm` before calling `portalRevokeInstanceKey` |
| 7 | "Issue key" action in panel header | ✅ Outline button in Panel `actions` slot; one-time raw key display in Alert below |
| 8 | Active count eyebrow | ✅ `{activeCount()} active` in eyebrow div; counts keys without `revoked_at` |

## Raw key safety

| # | Check | Status |
|---|---|---|
| SK1 | Raw key never rendered in the keys table | ✅ Table iterates `k.id` only; `k.key` does not exist on `InstanceKeyListItem` |
| SK2 | Raw key only shown once at creation | ✅ One-time Alert with `created.key` appears only after successful `portalCreateInstanceKey`; Dismiss button clears it |
| SK3 | Copy button in table copies safe identifier | ✅ `CopyButton text={k.id}` — copies the full key ID (non-secret); raw key not accessible from the table row |
| SK4 | Copy button on creation alert copies raw key | ✅ `Button onclick={copy(created?.key)}` — intentional; raw key shown only once with explicit warning |
| SK5 | Revoke confirmation uses masked ID | ✅ `window.confirm(`Revoke key ${maskKeyId(keyId)}?`)` — never reveals full ID in confirmation dialog UI |

## Deliberate data deviations

| # | Surface | Design expectation | M9 rendering | Rationale |
|---|---|---|---|---|
| D1 | Label column | `{k.label}` (e.g. "Production API", "CI / build") | Omitted (no label column) | `InstanceKeyListItem` has no `label` field; token ID masking serves as the primary identifier |
| D2 | Scopes column | `inbox.write, follow.write`, `admin.read`, etc. | `—` per row | `InstanceKeyListItem` has no `scopes` field; scope metadata is not exposed by the portal keys API |
| D3 | Masked token format | `sk_live_4f29...e1c4` (design shows Stripe-style) | First 8 + "..." + last 4 (e.g. `sk_live_...7b10`) | Generic masking works for any ID format; falls back to full ID for strings ≤ 14 chars |

## Deliberate visual deviations

| # | Surface | Design expectation | M9 rendering | Rationale |
|---|---|---|---|---|
| V1 | Panel eyebrow | Design's Panel has `eyebrow={`${keys.length} active`}` prop | Uppercase Text inside a `<div>` wrapper | Greater-shell Panel does not expose an `eyebrow` prop |
| V2 | CopyChip component | Design uses `<CopyChip value={k.id} />` (chip-style inline copy) | `CopyButton` (icon variant) next to masked text | Greater-shell does not ship a `CopyChip` component; `CopyButton` variant="icon" provides equivalent functionality |
| V3 | Table style | Design uses `gtable` CSS class | Custom `.keys__table` class with manual styling | Consistent with M8 Cost table pattern; no `gtable` primitive in greater-shell Svelte components |
| V4 | Revoked key opacity | Design might show a badge | Row gets `opacity: 0.5` via `.keys__row--revoked` class; "Revoked" text replaces Revoke button | Minimal visual treatment; conveys state without a Badge dependency |

## CSP, isolation, and governance

- ✅ Strict single-origin CSP preserved (no inline scripts, no inline styles, no new origins)
- ✅ Trust-API instance-auth posture preserved: raw keys never stored, never returned on re-read, never logged
- ✅ Multi-tenant isolation: API calls scoped to instance slug; ownership checks at the API layer
- ✅ All new Svelte/test files carry AGPL-3.0-only headers
- ✅ Gov-infra rubric: 40/40 verifiers pass
- ✅ Web: lint PASS, typecheck 0/0, 205 tests PASS, build PASS (CSP clean, OAC form integrity)
