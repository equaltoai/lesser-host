# Theory Cloud framework feedback signals — FaceTheory v3.3.0 adoption — 2026-05-24

This record captures framework-level signals raised by the `coordinate-framework-feedback` walk for host's planned FaceTheory v3.3.0 adoption in `web/`, per the 2026-05-24 scoped-need (`docs/scoped-need-web-ui-rework-2026-05-24.md`). The adoption is anticipated for the upcoming live release of `lesser.host` to general customers.

These are feedback signals only. They do **not** authorize local framework patches, CSP loosening, trust-API auth changes, provisioning changes, or managed release-verification changes. Host's posture remains: consume the framework idiomatically; if it's awkward under host's constraints, surface the signal upstream.

## Adoption verdict (Step 1: is host expressing the concern wrong?)

**Verdict: FaceTheory v3.3.0 is genuinely well-aligned to host's constraints. The May 16 verdict that held off FaceTheory is stale and the adoption should proceed.** The features that motivated the May 16 deferral (strict-CSP via JSON sidecars, tenant-partition-safe ISR, OAC-safe mutating forms) are now first-class in FaceTheory v3.3.0:

- **Strict no-inline CSP via external hydration**: documented at `docs/facetheory/getting-started.md#add_strict_no_inline_csp_hydration` and `docs/facetheory/migration-guide.md#migration_7_move_legacy_inline_hydration_to_strict_csp_external_hydration`. Hydration data moves to same-origin JSON sidecar at `/_facetheory/data/*`; `readFaceHydrationData(document?)` reads `__FACETHEORY_DATA__`. CloudFront `directS3PathPatterns` serves hydration JSON from S3.
- **OAC-safe mutating forms**: `data-facetheory-oac-form` opt-in + `startAwsOacFormTransport()`. Computes SHA256 over URL-encoded body bytes, sets `x-amz-content-sha256` for CloudFront Lambda URL OAC signing. Forces `redirect: "error"` on the mutating fetch so a 307/308 open redirect cannot replay the signed body to another origin. Migration 6 retires app-local form workarounds.
- **AppTheorySsrSite** assumes the stronger FaceTheory contract by default: SSR origin fails closed to `AWS_IAM` + lambda Origin Access Control for all CloudFront-to-Lambda traffic when `ssrUrlAuthType` is omitted.
- **Svelte adapter** at `@theory-cloud/facetheory/svelte` (`renderSvelte`, `createSvelteFace`), peer `svelte@^5.55.7` — host already pins exactly this Svelte version.
- **ISR** uses S3HtmlStore + LeaseManager + DynamoDB metadata. TableTheory exports `FaceTheoryIsrMetaStore` helper; ISR hydration sidecars in same `S3HtmlStore` namespace as HTML with `.hydration.json` derived from HTML pointer.

The May 16 markdown-sanitizer signal (Greater/FaceTheory fail-closed markdown rendering) is unaddressed by FaceTheory v3.3.0 from what the KB exposes; host's local hardening in `web/src/lib/greater/content/MarkdownRenderer.svelte` remains required. The 2026-05-16 signal is still live; it is not re-issued here. The 2026-05-16 LightningCSS-safe global selectors signal was consumed in Greater `greater-v0.8.14` and is closed.

## Routing summary

| Signal | Owner path | Routing status |
| --- | --- | --- |
| Signal A — Stitch shell vs Greater-components shell-primitive boundary | Greater Components steward + FaceTheory steward (joint) via Aron | Prepared below; needs cross-team product decision (Greater + Theory Cloud). |
| Signal B — Tenant-partition-safe ISR canonical pattern | ~~FaceTheory steward via Aron~~ | **Withdrawn 2026-05-24** per Arch review on PR #380: FaceTheory v3.3.0 docs/code do include `tenantKey` / custom `cacheKey` + fail-closed on `x-tenant-id` / `x-facetheory-tenant`. Recategorized as host-side trust-surface conservatism note. |
| Signal C — AppTheory HTTP + FaceTheory SSR + greater-components SPA composition under a single CloudFront distribution | AppTheory steward + FaceTheory steward (joint) via Aron | Prepared below; adoption-guidance gap. |
| Signal D — Greater-components additive components for hosted-platform UIs | Greater Components steward via Aron | Prepared below; additive request list. |

## Framework-feedback signal A: Stitch shell vs Greater-components shell-primitive boundary

### Target framework

FaceTheory v3.3.0 Svelte Stitch shell (`@theory-cloud/facetheory/svelte/stitch-shell`) and Greater Components primitives (`@equaltoai/greater-components/primitives`).

### Framework version in use

- FaceTheory: not yet adopted in host; v3.3.0 (latest) targeted for adoption.
- Greater Components: `greater-v0.8.14` (current host pin) consumed via `greater` CLI under `web/src/lib/greater/`.

### The concern (under host's constraints)

The 2026-05-24 Claude Design handoff specifies a three-column app shell (collapsible left nav with per-instance status dots + content + contextual right rail) with per-instance fleet cards rendering cost gauges, activity sparklines, and status pulses. The design adds a ⌘K command palette as primary navigation. Aron's explicit direction is to "continue using greater-components as much as possible and can request new components as needed."

FaceTheory v3.3.0 ships exactly the shell primitives the design needs:

- `stitch-shell`: **Shell, Sidebar, Topbar (with optional logo / surfaceLabel slots), BrandHeader, PageFrame, PageTitle, Breadcrumb, Section, Panel, StatCard, SummaryStrip, Callout**
- `stitch-admin`: DataTable, DetailPanel, PropertyGrid, FormRow, FormSection, SplitForm, StatusTag, DestructiveConfirm, Tabs, FilterChip
- `stitch-hosted-auth`: AuthPageLayout, AuthCard, AuthFlowStepper, PasskeyCTA, OTPInput, ConsentItem

Greater Components today provides primitives heavy on Fediverse-oriented surfaces (Timeline, Status, Avatar, Card, Button, Badge, Tabs, DefinitionList, content rendering, headless behaviors) but does **not** today expose an opinionated Shell + Sidebar + Topbar + StatCard + SummaryStrip vocabulary at the level the design requires.

This creates a concrete boundary question host cannot resolve unilaterally:

- If the Stitch shell primitives are the Theory Cloud-canonical shell surface, then "use greater-components as much as possible" is in tension with FaceTheory's own primitives. Adopting Stitch in host is the idiomatic FaceTheory path; refusing it forces host to either request additive greater-components or build local shell primitives — both undermining the framework-feedback discipline.
- If Greater Components is the canonical UI vocabulary across the equaltoai stack and FaceTheory Stitch is intended only as a default fallback, then host's correct posture is to request additive shell primitives from Greater (Signal D below) and not adopt Stitch.

Host's strict-CSP, multi-tenant, hosted-platform constraints don't favor one over the other on technical grounds — both could be made to work. The seam is product/ownership, not engineering, and it's exactly the kind of decision a steward must escalate rather than make locally.

### The idiomatic code host would write if the boundary were resolved

**If Greater Components owns the shell:**

```svelte
<script lang="ts">
  import { Shell, Sidebar, Topbar, Panel, StatCard } from '@equaltoai/greater-components/shell';
</script>

<Shell>
  <Sidebar slot="sidebar">…</Sidebar>
  <Topbar slot="topbar">…</Topbar>
  <Panel>
    <StatCard label="Monthly cost" value="$842.13" trend="+12%" />
  </Panel>
</Shell>
```

**If FaceTheory Stitch owns the shell:**

```svelte
<script lang="ts">
  import { Shell, Sidebar, Topbar, Panel, StatCard } from '@theory-cloud/facetheory/svelte/stitch-shell';
</script>

<Shell>
  <Sidebar slot="sidebar">…</Sidebar>
  <Topbar slot="topbar">…</Topbar>
  <Panel>
    <StatCard label="Monthly cost" value="$842.13" trend="+12%" />
  </Panel>
</Shell>
```

Both surface APIs are reasonable. Host doesn't need them to be identical; it needs one designated owner so consumers don't re-fork the contract.

### The current workaround in host (or "blocked")

Pre-adoption. Planned workaround until the boundary is resolved: host would either (a) request additive shell primitives from Greater (Signal D below) and decline Stitch, or (b) adopt Stitch and treat greater-components as the content + iconography + adapters + headless-behaviors layer that composes inside Stitch's shell. Both are workable; either choice creates expectations for sibling repos.

### Cost of the workaround

- Code complexity: depends on resolution. If host adopts Stitch, future Greater shell primitives risk duplication. If host requests additive Greater shell primitives, Greater carries a hosted-platform vocabulary it may not be motivated to support.
- Test burden: minimal in either direction; both libraries are tested upstream.
- Performance impact: none.
- Maintenance drag: high if the seam is left unresolved; every consumer makes a different choice. Cross-repo refactor risk if the canonical owner is decided later and host's pick was wrong.
- Governance-rubric impact: none directly, but consumer-release-verification depends on stable vendored surface. A canonical shell-primitive owner reduces verifier churn.

### Scope of the gap

- Specific to host's constraints: hosted-platform UI vocabulary distinct from Fediverse-app UI vocabulary; strict single-origin CSP; multi-tenant operator + customer surfaces.
- Likely broader: yes; any equaltoai consumer building admin/operator/portal UIs faces this question (sim already does; future consumers will).
- Other known consumers affected: sim (operator-facing surfaces), potentially body's MCP-management UI if one is ever built, any future managed-platform builds on the Theory Cloud stack.

### Host's workaround posture

- Continue workaround while framework evolves: yes, but the workaround is "wait for the boundary to be designated" — host can't ship the rework without a decision.
- Workaround is temporary / awaits framework: yes; this signal blocks Aron's product decision in scope-need M0, not a quarter-long wait.
- Governance-rubric allows the workaround: yes; neither choice loosens governance.

### Proposed next step

Greater Components steward + FaceTheory steward jointly designate ownership of hosted-platform shell primitives (Shell, Sidebar, Topbar, Panel, StatCard, SummaryStrip). Aron's preference for "greater-components as much as possible" is recorded as input. If Greater owns it, Signal D's additive component requests are accepted. If FaceTheory Stitch owns it, host adopts Stitch and greater-components remains the content + iconography + adapters + headless-behaviors layer. Either resolution unblocks host's M0.

## Signal B (recategorized): Tenant-partition-safe ISR — host-side trust-surface conservatism note

> **Update following Arch's 2026-05-24 review on PR #380**: Arch verified that FaceTheory v3.3.0 primary docs/code **do** include the canonical tenant-partition-safe ISR primitives: `tenantKey` / custom `cacheKey` guidance plus fail-closed handling for `x-tenant-id` / `x-facetheory-tenant` headers. The original Signal B drafting (that the canonical pattern wasn't surfaced) reflected an incomplete KB query in this walk, not a genuine framework gap. Signal B is therefore **withdrawn as a framework-feedback signal** and recorded here as a **host-side trust-surface conservatism note** explaining the ISR deferral decision.

### Target framework

FaceTheory v3.3.0 blocking ISR (framework support is present; this section captures the host posture decision).

### Framework version in use

FaceTheory v3.3.0 (targeted for adoption).

### Framework support confirmed

FaceTheory v3.3.0 provides, per the primary docs and code:

- `tenantKey` / custom `cacheKey` for per-tenant ISR partitioning.
- Fail-closed handling for `x-tenant-id` / `x-facetheory-tenant` request headers (rejecting requests that imply tenant scoping a route did not opt into).
- The blocking-ISR storage primitives previously enumerated (S3HtmlStore + DynamoDB metadata + LeaseManager + TableTheory `FaceTheoryIsrMetaStore` helper + idempotency / incident checks / multi-language transaction recipes).

Host could, mechanically, adopt ISR on `/attestations/*` and `/.well-known/jwks.json` using these primitives.

### Host's posture: defer ISR adoption on tenant-scoped trust surfaces for this rework

The deferral is **host-specific trust-surface conservatism**, not a framework limitation:

1. **Trust-surface elevated stakes.** `/attestations/*` and `/.well-known/jwks.json` are the public surfaces third parties rely on to verify managed-instance trust claims. Any cache-layer regression (mis-keyed entry, stale-after-rotation entry, cross-tenant serve) on these surfaces would materially compromise trust posture before host's evidence pipeline could surface the regression.
2. **Rotation interaction is host-specific.** Host rotates instance API key hashes, attestation-signing keys (via KMS), and soul-registry mint-signer keys at independent cadences. Each rotation must invalidate the relevant ISR entries without leaving stale per-tenant entries or accidentally invalidating cross-tenant cache. The framework primitives support this, but the host-side rotation→invalidation wiring is bespoke and warrants extended soak in a non-trust-critical surface first.
3. **Verifier coverage gap.** Host does not yet have a CSI-style verifier asserting "no ISR cache entry serves under another tenant's URL or scope" or "every key rotation invalidates the matching ISR partition." Building such a verifier is in scope for a future rubric evolution; rushing ISR adoption ahead of the verifier weakens the change-lock posture established by SEC-9 + SEC-10.
4. **Cost of deferral is low.** Public attestation reads stay client-rendered with same-origin API call (already strict-CSP-compliant; no SEO or cold-start regression beyond status quo).

### Future ISR adoption path

Treat ISR-on-trust-surfaces as a **post-launch separate scope-need**:

1. Pilot ISR adoption on a low-stakes surface first (e.g., a content-style page that doesn't touch trust claims).
2. Build the per-tenant cache-isolation verifier + the rotation→invalidation wiring verifier as part of that pilot.
3. Once those verifiers are green in lab + live, scope ISR adoption on `/attestations/*` / `/.well-known/jwks.json` as a separate rework, with its own audit-trust-and-safety walk + maintain-governance-rubric walk.

### Status

- Framework feedback to FaceTheory steward: **none required**. Framework support is present; no upstream signal.
- Host posture: deferral recorded; revisit per the "Future ISR adoption path" above.
- Trust-and-safety walk (commit `24dcd86`): the ISR-deferral verdict is preserved; the rationale is reframed to cite this conservatism note rather than a framework gap.

## Framework-feedback signal C: AppTheory HTTP + FaceTheory SSR + greater-components SPA composition under a single CloudFront distribution

### Target framework

AppTheory v1.7.0 HTTP Lambda runtime + FaceTheory v3.3.0 SSR runtime + AppTheory `AppTheorySsrSite` CDK construct.

### Framework version in use

- AppTheory: v1.7.0 (host pins this; the control-plane API, trust API, and all workers are AppTheory HTTP/SQS/EventBridge backed).
- FaceTheory: v3.3.0 (targeted for adoption).
- Existing host CloudFront: single distribution with default origin pointing at the SPA's S3 bucket; path-based routes to the control-plane API Lambda Function URL (`/api/*`, `/auth/*`, `/setup/*`) and the trust API Lambda Function URL (`/.well-known/*`, `/attestations/*`). Strict single-origin CSP.

### The concern (under host's constraints)

Host's existing architecture is:

```
CloudFront (one distribution)
├── default origin: S3 static SPA bucket            (Vite build, strict CSP)
├── /api/* + /auth/* + /setup/* → Lambda Function URL → cmd/control-plane-api (AppTheory HTTP)
└── /.well-known/* + /attestations/* → Lambda Function URL → cmd/trust-api (AppTheory HTTP)
```

FaceTheory adoption replaces the SPA origin with an SSR Lambda + S3 sidecar bucket:

```
CloudFront (one distribution)
├── default origin: AppTheorySsrSite SSR Lambda     (FaceTheory, strict CSP, OAC + AWS_IAM)
├── /_facetheory/data/* + /assets/* → S3            (hydration JSON sidecars + hashed client assets)
├── /api/* + /auth/* + /setup/* → Lambda Function URL → cmd/control-plane-api (AppTheory HTTP, unchanged)
└── /.well-known/* + /attestations/* → Lambda Function URL → cmd/trust-api (AppTheory HTTP, unchanged)
```

The composition question is: **does `AppTheorySsrSite` cleanly accept additional Lambda Function URL origins for AppTheory HTTP Lambdas under the same distribution?** Or does it expect the distribution to be FaceTheory-only?

The FaceTheory CDK docs queried (e.g., `apptheory/cdk/ssr-site.md`) describe `AppTheorySsrSite` as "the stronger FaceTheory deployment contract by default" with omitted `ssrUrlAuthType` failing closed to `AWS_IAM` + Lambda OAC. They don't (in the KB I can query) document the supported pattern for composing additional AppTheory HTTP Function URL origins with mixed auth (host's control-plane API and trust API are bearer-token-auth, not OAC-IAM).

Adjacent concern: **observability composition.** AppTheory observability emits structured records via its observability chain; FaceTheory emits `observability.log/metric` records with `x-facetheory-isr` / `x-facetheory-ssr` headers and a separate `x-request-id` chain. host's evidence pipeline (gov-infra) currently consumes AppTheory's records. If FaceTheory observability emits differently, gov-infra's CSP/audit evidence collection must reconcile both record shapes.

### The idiomatic code host would write if the framework supported it

```ts
// cdk/lib/lesser-host-stack.ts (proposed FaceTheory adoption)
const ssrSite = new AppTheorySsrSite(this, 'WebSsr', {
  faceModule: require.resolve('../../web/dist/server/index.js'),
  staticAssets: { bucket: webAssetsBucket },
  cspPolicy: {
    scriptSrc: ["'self'"],
    styleSrc: ["'self'"],
    inlineScripts: false,  // → forces hydration sidecars
  },
});

ssrSite.distribution.addBehavior('/api/*', new HttpOrigin(controlPlaneFunctionUrl.url, {
  authType: 'bearer',  // ← idealized; AppTheorySsrSite needs to support mixed-auth co-origins
}), {
  allowedMethods: AllowedMethods.ALLOW_ALL,
  cachePolicy: CachePolicy.CACHING_DISABLED,
});

ssrSite.distribution.addBehavior('/attestations/*', new HttpOrigin(trustFunctionUrl.url, {
  authType: 'bearer',
}), { ... });
```

And a unified observability point:

```ts
const obs = observability.New(serviceName);
const apptheoryApp = apptheory.New(apptheory.WithObservability(obs));
const facetheoryFace = createFaceApp({ faces: [...], observability: obs });
// gov-infra evidence consumes obs alone; both AppTheory and FaceTheory records flow through it.
```

### The current workaround in host (or "blocked")

Pre-adoption. Planned workarounds:

1. Two distributions (one for FaceTheory SSR origin, one for AppTheory HTTP Lambdas behind CloudFront) — refused: violates single-origin CSP, breaks cookie scope, breaks the trust-API path-based routing under `lesser.host`.
2. One distribution with manual `addBehavior` calls outside `AppTheorySsrSite`'s contract — workable but undocumented; risks `AppTheorySsrSite` refactors breaking host on bump.
3. Migrate trust API and control-plane API into the FaceTheory app as FaceModule routes that proxy to internal Lambdas — major refactor; conflates HTTP API surface with SSR app, breaks the AppTheory HTTP testkit, breaks gov-infra's per-Lambda evidence emission.

Option 2 is the planned workaround for M0; host carries the risk on AppTheorySsrSite bumps until the supported pattern is documented.

### Cost of the workaround

- Code complexity: moderate — host hand-wires CloudFront behaviors outside the FaceTheory CDK construct contract.
- Test burden: host needs to assert single-distribution + behavior list invariants in CDK tests; would prefer the framework to assert them.
- Performance impact: none; runtime behavior unchanged.
- Maintenance drag: high on framework version bumps if `AppTheorySsrSite` evolves the supported composition pattern.
- Governance-rubric impact: medium — gov-infra evidence about CSP, cache, and origin posture currently asserts the existing single-distribution shape. The new shape needs equivalent verifier coverage.

### Scope of the gap

- Specific to host's constraints: AppTheory HTTP backend + FaceTheory SSR frontend under one CloudFront distribution with strict CSP and mixed auth (bearer vs OAC-IAM).
- Likely broader: yes; any Theory Cloud consumer composing an HTTP API backend with an SSR frontend faces this.
- Other known consumers affected: greater-components consumers that move from SPA to SSR will hit this.

### Host's workaround posture

- Continue workaround while framework evolves: yes.
- Workaround is temporary / awaits framework: yes; we hope for a documented composition pattern within 1–2 FaceTheory releases.
- Governance-rubric allows the workaround: yes, provided host's CDK tests pin the expected single-distribution behavior list and gov-infra adds an additive verifier (consolidated in the `maintain-governance-rubric` walk).

### Proposed next step

AppTheory steward + FaceTheory steward jointly document the supported composition pattern for: (a) `AppTheorySsrSite` plus additional Lambda Function URL origins with mixed auth (bearer in addition to OAC-IAM) under a single CloudFront distribution, and (b) unified observability across an AppTheory HTTP app and a FaceTheory SSR app. Host's planned `audit-trust-and-safety` walk will encode the workaround as an additive verifier so CI catches drift if the framework evolves the supported shape.

## Framework-feedback signal D: Greater-components additive components for hosted-platform UIs

### Target framework

Greater Components (target version `greater-v0.8.14` and forward).

### Framework version in use

`greater-v0.8.14` (current host pin) consumed via `greater` CLI under `web/src/lib/greater/`.

### The concern (under host's constraints)

The 2026-05-24 Claude Design handoff introduces UI vocabulary specific to hosted-platform managed infrastructure that the current Greater Components catalog does not expose:

- **Cost gauge** — a radial / linear gauge rendering a numeric cost ("$842.13 / $1,200") with a colored arc indicating budget consumption. Distinct from a Status badge or Progress bar.
- **Fleet card** — a card with a cost gauge, status pulse dot, activity sparkline, and one-line metadata; the card represents one provisioned instance in the fleet view.
- **Activity sparkline** — a tiny inline chart (request rate, daily cost, message volume) embedded in the fleet card.
- **Command palette (⌘K)** — a modal palette with fuzzy search, scoped result groups (instances / souls / actions / navigation), keyboard-driven navigation. Distinct from a Menu or Autocomplete.
- **Release timeline** — a vertical timeline of release versions per channel (lesser channel + lesser-body channel side-by-side) with adoption bars, breaking/latest badges, and per-version metadata.
- **Stack matrix** — a table showing each instance's lesser version, body version, MCP wiring state, and per-row remediation CTAs ("Update" / "Wire MCP"); distinct from a generic DataTable in that it carries domain knowledge about lesser/body/MCP coupling.
- **Provisioning timeline (per job)** — a vertical timeline of CDK rollout steps with live streaming log output inside the active node; used for `provision`, `update-lesser`, `update-body`, `wire-mcp` job kinds.

Aron's explicit direction is to "continue using greater-components as much as possible and can request new components as needed" — this signal is that request list. The design's prototype shows these as React mockups; host targets Svelte 5.

This signal is contingent on Signal A's resolution: if FaceTheory Stitch is designated the owner of Shell/Sidebar/StatCard/Panel/SummaryStrip, those primitives are **out of scope** for Greater and removed from this list. The components listed here are hosted-platform-specific (not generic shell), so they probably remain a Greater concern in either resolution.

### The idiomatic code host would write if Greater supported these

```svelte
<script lang="ts">
  import { CostGauge, FleetCard, ActivitySparkline, CommandPalette } from '@equaltoai/greater-components/hosted-platform';
  import { ReleaseTimeline, StackMatrix, ProvisioningTimeline } from '@equaltoai/greater-components/managed-platform';
</script>

<FleetCard
  slug="equaltoai"
  status="healthy"
  costGauge={{ value: 842.13, budget: 1200, currency: 'USD' }}
  sparkline={recentActivity}
/>

<CommandPalette
  groups={[
    { id: 'instances', label: 'Instances', items: instanceItems },
    { id: 'souls', label: 'Souls', items: soulItems },
    { id: 'actions', label: 'Actions', items: actionItems },
  ]}
  open={paletteOpen}
  on:select={handlePaletteSelect}
/>

<ReleaseTimeline channels={[lesserChannel, bodyChannel]} adoption={fleetAdoption} />
<StackMatrix instances={fleet} remediation={{ updateAction, wireMcpAction }} />
<ProvisioningTimeline job={activeJob} liveLogs={logStream} />
```

### The current workaround in host (or "blocked")

Pre-adoption. Without these components in Greater, host would either build them bespoke in `web/src/components/` (creating a divergence from greater-components conventions and increasing host's local UI maintenance burden) or commission them via the greater-components steward through this signal.

### Cost of the workaround

- Code complexity: high — each bespoke component carries Svelte 5 implementation, accessibility, theming-token bridge, tests, and ongoing maintenance.
- Test burden: high — host carries unit and accessibility tests for primitives that should be upstream.
- Performance impact: minimal; well-written bespoke components are fine.
- Maintenance drag: high — every future component refresh requires host to re-evaluate against any upstream additions.
- Governance-rubric impact: low directly, but bespoke components increase the surface area gov-infra's CSP, accessibility, and AGPL verifiers must cover. Centralizing them in Greater reduces verifier surface across the equaltoai stack.

### Scope of the gap

- Specific to host's constraints: hosted-platform managed-infrastructure UI vocabulary; multi-tenant fleet management; per-instance cost telemetry.
- Likely broader: yes; sim's operator surfaces and any future Theory Cloud managed-platform consumer face the same need.
- Other known consumers affected: sim (operator surfaces), potentially future equaltoai consumers.

### Host's workaround posture

- Continue workaround while framework evolves: depends on Greater steward response. If the additive components are accepted into Greater's roadmap, host waits and ships M0 + M1 with `provisioning timeline` and `command palette` first-priority. If declined, host builds bespoke.
- Workaround is temporary / awaits framework: yes for accepted components; permanent for declined ones.
- Governance-rubric allows the workaround: yes either way.

### Proposed next step

Greater Components steward triages the additive request list. Priority order from host's M0–M3 phasing:

| Priority | Component | Host milestone |
| --- | --- | --- |
| 1 | Command palette (⌘K) | M0 (primary navigation) |
| 1 | Fleet card | M0 (hero of Portal Fleet) |
| 1 | Cost gauge | M0 (inside fleet card) |
| 2 | Activity sparkline | M0–M1 (inside fleet card and instance overview) |
| 2 | Provisioning timeline | M2 (operator console live job view) |
| 3 | Release timeline | M2 (operator /releases page) |
| 3 | Stack matrix | M2 (operator /releases page, customer instance detail) |

Components accepted into Greater are pulled via `greater add` on Greater release; components declined are scoped as host-bespoke and tracked in M0–M2's enumerated changes.

## Cross-signal interactions

- **Signal A (Stitch vs Greater shell) blocks Signal D (additive components)**: until the shell ownership is designated, the additive Greater request list cannot be finalized. If Stitch owns the shell, the Greater request list is the hosted-platform-specific subset listed in Signal D. If Greater owns the shell, the request list expands to include the shell primitives too.
- **Signal C (CloudFront composition) shapes the `audit-trust-and-safety` walk**: the CSP-shape-change, OAC-form, and observability decisions are inputs to the trust-and-safety walk that runs next.
- **Signal B (recategorized) records the host-side ISR deferral rationale** for `/attestations/*` and `/.well-known/jwks.json`: framework support is present (per Arch's 2026-05-24 correction), but host's strict-CSP + per-tenant rotation + verifier-coverage posture warrants deferral until a separate post-launch ISR-pilot scope can build the per-tenant cache-isolation + rotation→invalidation verifiers first.
- **None of the signals weaken the governance rubric** by themselves; the additive verifiers tracking the workarounds are consolidated in the `maintain-governance-rubric` walk that runs after the other three.

## Outbound coordination record

To be populated when the signal is sent to the framework stewards:

- Greater Components steward (`greater.equaltoai@theorymcp.ai` per the 2026-05-16 record): _signals A and D pending Aron coordination_.
- FaceTheory steward: _signals A and C pending Aron coordination; no contactable Theory Cloud framework mailbox is exposed to this host endpoint. Signal B was withdrawn 2026-05-24 per Arch review (framework support confirmed; recategorized as host posture note)._
- AppTheory steward: _signal C pending Aron coordination_.

Per the 2026-05-16 outbound discipline, signals routed without a contactable framework mailbox are routed through this PR, Arch review (n/a; not an advisor-dispatched scope), and Aron handoff for manual framework-steward delivery.

## Persistence

Memory append after commit: target framework `FaceTheory v3.3.0` + `Greater Components` + `AppTheory v1.7.0`; concern shape `pre-adoption framework-feedback for host web/ UI rework`; date 2026-05-24; routing recorded above; revisit on next AppTheory / FaceTheory / Greater bump.
