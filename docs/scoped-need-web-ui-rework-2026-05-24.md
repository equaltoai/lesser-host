# Scoped Need: web/ UI rework on FaceTheory v3.3.0 + greater-components, per Claude Design handoff

_Produced 2026-05-24 by the host steward via the `scope-need` skill, on branch `aron/web-ui-rework-planning`._

## Background

host's current `web/` SPA is a Svelte 5 + Vite static build deployed behind a single-origin CloudFront with strict CSP (`script-src 'self'`, `style-src 'self'`, no inline). It consumes `greater-components` (vendored under `web/src/lib/greater/` via the `greater` CLI). It works, but Aron has characterized it as "currently a prototype." A Claude Design session (chat transcript dated 2026-05-24, bundle `cIP4q24786t1QsboA0PZrg`, snapshotted under `docs/design/web-ui-rework-2026-05-24/`) produced an opinionated reimagining: a "cost-first cockpit" three-column shell with ⌘K palette, fleet cards with cost gauges and activity sparklines, a tabbed Instance Detail (replacing today's 1100-line monster), and a distinct dark warm-charcoal chrome for the Operator Console. The design also makes provisioning **updates** first-class: Lesser and Lesser-body as two independent release channels, MCP-wiring as a coupled job that auto-queues after body updates, plus a Stack Matrix and drift warnings.

This work is undertaken in anticipation of host's upcoming `live` release of `lesser.host` to general customers. The rework is the visual + IA + frontend-framework readiness layer of that launch.

## Driver

**Aron-direct, timed to upcoming live release.** Not advisor-dispatched. Not a CVE. Not a customer escalation. The wall-clock pressure is the live launch window (target date TBD — see Open Questions).

## Problem

Three intertwined problems:

1. **The current UI is a CRUD-shaped prototype.** Sidebar + plain tables + definition lists don't read as infrastructure software. The 1100-line monolithic Instance Detail page in particular needs to become a tabbed shell.
2. **The current frontend stack underserves the live-launch target.** The static SPA + strict-CSP-via-CloudFront baseline is sound, but FaceTheory v3.3.0 (released 2026-05-23) now offers strict-CSP via JSON sidecars + tenant-partition-safe ISR + OAC-safe forms — features that are *better aligned* to host's posture than the bare Vite SPA, and that improve public-surface SEO + cold-start UX for `/attestations/*` and `/.well-known/*` ahead of public traffic. The May 16 framework-adoption investigation held off FaceTheory specifically because none of those features then existed; that verdict is now stale.
3. **Provisioning *updates* are not first-class in today's UI.** Lesser, Lesser-body, and the MCP-wiring coupling are visible only in operator-side investigation. For a live customer base, drift detection and one-click "update entire stack / specific release / wire MCP" must be customer-readable.

## Surface affected

- **`web/` SPA** — comprehensive rebuild: app shell, design tokens, primitives, routing, all 10+ pages
- **CSP / CloudFront** — migrate from inline-free Vite SPA to FaceTheory JSON-sidecar hydration; CSP shape changes (still strict, but the shape moves)
- **Control-plane API** — additive endpoints for fleet cost telemetry, release-channel adoption, drift state, MCP-wiring as a distinct job kind
- **Trust API** — public attestation surface re-skinned + considered for ISR
- **Provision worker** — MCP-wiring as a distinct `wire-mcp` job kind (today it is implicit); two-channel release ingestion (`lesser` + `lesser-body`); drift detection feed
- **CDK / IaC** — FaceTheory CDK construct adoption; CloudFront behaviors for JSON sidecars + ISR origin; OAC for any new mutating-form surfaces
- **`greater-components` consumption** — additive: identify and request new components needed by the design (cost gauge, fleet card, command palette, release timeline, stack matrix); existing primitives (Card→Panel, Button, Badge, Alert, Tabs, DefinitionList, Avatar, CopyButton, Switch, Spinner) re-themed
- **Governance rubric** — likely additive verifiers: JSON-sidecar CSP integrity, release-channel adoption evidence, MCP-wiring step recovery evidence, cost-telemetry redaction
- **Cost telemetry pipeline** — new: CloudWatch metric collection + Cost Explorer integration + DynamoDB cache + worker for per-instance fleet cost
- **Docs** — frontend-roadmap.md supersession; new ADRs for FaceTheory adoption, CSP shape change, MCP-wiring job-kind contract, cost-telemetry data model

## Lambda(s) affected

- `cmd/control-plane-api` — new fleet/cost/drift/release endpoints
- `cmd/trust-api` — re-skinned public surface; ISR consideration
- `cmd/provision-worker` — `wire-mcp` job kind; two-channel release ingestion; drift detection emission
- (Potentially new) `cmd/cost-telemetry-worker` — periodic per-instance cost aggregation
- Indirect: `cmd/ai-worker`, `cmd/render-worker`, `cmd/comm-worker`, `cmd/soul-reputation-worker`, `cmd/email-ingress` — emit cost-attributable metrics; otherwise unchanged

## Classification

**Multi-classification:** UI / UX rework + framework adoption + provisioning evolution + observability addition + governance-rubric evolution + AGPL-tracked + framework-feedback. The bundle reflects deliberately accepted scope.

## Narrowest-scope proposal (within each milestone)

Aron selected "phase by surface (recommended)" and "full backend co-scoping." Within that decision, Gate 2 (narrowest scope) still applies *inside* each milestone. Proposed phasing:

**M0 — FaceTheory adoption foundation + design system + shell**
- Scope: FaceTheory v3.3.0 + Svelte adapter wired into `web/`; new app shell (three-column collapsible nav + content + contextual right rail); ⌘K palette; design tokens (`--ds-*` with `--gr-*` bridge intact); re-themed greater-components primitives; CSP shape change (Vite inline-free → JSON-sidecar); CloudFront behaviors for sidecars; **one** representative page wired to validate end-to-end (proposed: Portal Fleet).
- Specialist walks required: `coordinate-framework-feedback`, `audit-trust-and-safety` (CSP shape), `maintain-governance-rubric` (additive CSP-integrity verifier).
- Ships to `lab`; soak; no `live` until all milestones land.

**M1 — Portal hero pages + customer-readable update flows**
- Scope: tabbed Instance Detail (Overview, Cost & usage, Config, Domains, Keys, Souls); Portal Billing; Portal Souls + Soul Detail; Portal Trust; Portal Account.
- Backend additions in this milestone: customer-readable Stack card data (current versions for lesser + body + MCP wiring state) on instance detail; minimum cost-telemetry firehose for the cost-and-usage tab.
- Specialist walk required: `provision-managed-instance` (customer-readable stack state contract).

**M2 — Operator Console + dark chrome + Provisioning evolution**
- Scope: Operator Console shell with dark warm-charcoal chrome and operator-distinction signaling; `/operator/releases` with two side-by-side release timelines + Stack Matrix; provisioning supports four job kinds (`provision`, `update-lesser`, `update-body`, `wire-mcp`); `update-body` jobs auto-queue gated MCP-wiring step; MCP drift detection across the fleet with one-click "Wire all" remediation.
- Backend additions in this milestone: `wire-mcp` job kind in `provision-worker`; release-channel adoption ledger; drift-state computation + emission.
- Specialist walks required: `provision-managed-instance` (job-kind contract, drift detection, two-channel adoption), `maintain-governance-rubric` (additive verifier for MCP-wiring step recovery + release-adoption evidence).

**M3 — Operator Console balance + cost-telemetry firehose completion**
- Scope: Approvals (3 tabs); Audit explorer; Operator Instances; Tip registry; Soul registry; public attestation inspector (re-skinned, with ISR considered); full per-instance real-time cost telemetry across Lambda/Dynamo/egress.
- Backend additions: Cost Explorer integration + CloudWatch metric collection + DynamoDB cache + `cost-telemetry-worker`.
- Specialist walks required: `audit-trust-and-safety` (re-skinned attestation inspector + any new instance-auth surface), `maintain-governance-rubric` (cost-telemetry redaction verifier — no PII in cost data, no per-tenant cost data exposed across tenants).

**Cross-milestone**: each milestone passes gov-infra verifiers in CI; ships to `lab` independently; final `live` cutover happens once all milestones are merged + soaked. Multi-tenant isolation preserved absolutely throughout (cost data per-tenant is keyed by tenant; never aggregated cross-tenant in customer-facing views).

## What this need explicitly does not cover

- **No marketing site / CMS.**
- **No browser-side AWS credentials** (no IAM-in-browser).
- **No full "Lesser instance admin UI"** — that lives inside Lesser itself.
- **No advisor-dispatched scope changes.** If an advisor brief lands mid-flight, route via `review-advisor-brief`.
- **No on-chain contract changes.** Soul-registry contracts, TipSplitter contracts unchanged. Tip registry UI reskins existing endpoints; Safe-ready payload contract is preserved.
- **No multi-tenant boundary relaxation.** Cost telemetry is per-tenant, isolated. No cross-tenant aggregation in customer-facing views; operator-side cross-instance views are operator-authenticated and aggregate only at the operator role.
- **No FaceTheory local patches.** Awkwardness becomes upstream signal via `coordinate-framework-feedback`.
- **No AppTheory/TableTheory bump beyond what FaceTheory v3.3.0 requires.** Routine maintenance bumps stay in their own scope.
- **No `live` cutover before all four milestones are merged + soaked + canaried.**
- **No dark mode for Portal.** Light only per Agent Genesis canon. Dark chrome is Operator-only and is a deliberate operator-distinction signal.
- **No new on-chain anchoring of cost telemetry.** Cost data is off-chain only.
- **No replacement of greater-components.** Greater-components remains the component layer; new components are *requested* via the greater-components steward, not vendored bespoke into host.

## Success criteria

**Observable + testable:**

1. **`lab` deploys for M0–M3 each pass CI** (all gov-infra verifiers PASS 29/0/0 plus any additive verifiers introduced).
2. **CSP audit on each milestone's lab deploy** confirms strict-CSP-via-JSON-sidecar with no `unsafe-inline`, no `unsafe-eval`, no third-party script origins beyond what was explicitly governance-event-approved.
3. **Multi-tenant isolation regression suite** passes for every cost-telemetry, drift-state, and release-channel-adoption surface (no cross-tenant data in any customer-facing view; operator-side views aggregate only after operator-role check).
4. **A customer in `lab` can update the entire stack from the most recent release or a specific release for both Lesser and Lesser-body**, with the MCP-wiring step auto-queued + visibly progressing after body updates, and drift remediated visibly on the Stack Matrix.
5. **Public attestation inspector** renders attestations end-to-end with the new skin; if FaceTheory ISR is adopted there, attestation cache invalidation works on rotation.
6. **No FaceTheory local patches** committed to host; any framework awkwardness encountered is captured as a `coordinate-framework-feedback` signal.
7. **Pixel-fidelity check** against the Claude Design prototype screenshots for each hero page (Fleet, Instance Detail, Operator Releases, Provisioning Job Live timeline).
8. **`live` cutover plan exists** as part of `plan-roadmap` output and is reviewed before execution.

## Specialist routing

- **Governance rubric**: **walk via `maintain-governance-rubric`** — additive verifiers for JSON-sidecar CSP integrity, release-channel adoption evidence, MCP-wiring step recovery evidence, cost-telemetry redaction.
- **Provisioning / managed-update / release verification**: **walk via `provision-managed-instance`** — `wire-mcp` as distinct job kind, two-channel release adoption ingestion + verification (each channel's checksum verification preserved), drift detection, customer-readable stack-state contract.
- **Soul registry**: not touched in this rework. Tip registry UI is a reskin of existing endpoints; Safe-ready payload contract preserved.
- **Trust API / CSP / instance-auth**: **walk via `audit-trust-and-safety`** — CSP shape change to JSON sidecars; OAC posture for any mutating-form surfaces; public attestation re-skin + ISR consideration; instance-auth surfaces unchanged (sha256(raw_key) preserved).
- **Framework consumption**: **walk via `coordinate-framework-feedback`** — host as flagship FaceTheory v3.3.0 consumer in a high-governance, multi-tenant, multi-worker context; new greater-components requests routed through the greater-components steward; AppTheory feedback if FaceTheory adoption surfaces gaps in the AppTheory ↔ FaceTheory boundary.
- **Advisor brief**: n/a.

## Consumer impact

- **Managed-instance customers** — primary consumers. New cost-first cockpit, tabbed Instance Detail, customer-readable update flows. Migration is silent (no API contract breakage; old endpoints remain valid).
- **Operators** (Aron + authorized collaborators) — new Operator Console with distinct dark chrome; new Stack Matrix + MCP-drift remediation; new Audit explorer.
- **Public trust-API readers** — re-skinned public attestation inspector; ISR may shift caching characteristics but doesn't change attestation integrity.
- **Sibling repos**:
  - `lesser` — coordinated only if `wire-mcp` job-kind contract requires a lesser-side change; expected to be host-internal.
  - `body` — coordinated only if `wire-mcp` contract requires a body-side change; expected to be host-internal.
  - `soul` — no impact; namespace contract unchanged.
  - `greater` — additive component requests routed through their steward; releases consumed via `greater` CLI as today.
  - `sim` — sim consumes host's public surfaces; the reskinned attestation inspector + any new public endpoints are sim-facing. Coordinate cutover with sim steward.
- **External vendors** — Stripe / AI providers / comm providers unchanged. Cost Explorer / CloudWatch newly consumed.

## Multi-tenant isolation impact

**Elevated scrutiny required throughout.** Cost telemetry, drift state, release-channel adoption ledger, and any new fleet-aggregating view must be per-tenant in the customer plane and only operator-aggregated in the operator plane. No cross-tenant data leakage in any customer-facing surface. The `maintain-governance-rubric` walk produces an additive verifier for cost-telemetry redaction + cross-tenant isolation specifically. Each milestone's lab deploy includes an explicit isolation test before sign-off.

## On-chain impact

**None directly.** Tip registry UI is a reskin of existing Safe-ready payload endpoints; on-chain mutations preserved. Soul registry contracts and TipSplitter contracts unchanged. No new on-chain anchoring is introduced (cost telemetry is off-chain).

## AGPL posture

**No change.** FaceTheory and greater-components are AGPL-compatible (Theory Cloud and equaltoai stack, AGPL-3.0). New components requested through the greater-components steward inherit AGPL. No proprietary blobs. Standard contributor-origin transparency. Any new dependency introduced by FaceTheory adoption is license-vetted at `coordinate-framework-feedback` time.

## Open questions

1. **Live release window / target date** — needed for `plan-roadmap` to sequence milestones. If aggressive, M3 (cost-telemetry firehose) may need to be split or deferred post-launch.
2. **MCP-wiring job-kind contract with lesser + body stewards** — does `wire-mcp` need any lesser-side or body-side coordination, or is it entirely host-internal (host calls `POST /mcp/{actor}` on the lesser instance after a body update)? `provision-managed-instance` walk will resolve this.
3. ~~**Public attestation inspector ISR adoption**~~ — **Resolved 2026-05-24**: defer ISR adoption on tenant-scoped trust surfaces for this rework. Framework support exists (FaceTheory v3.3.0 provides `tenantKey` / custom `cacheKey` + fail-closed on tenant headers — per Arch's PR #380 review correction); deferral is host-specific trust-surface conservatism pending a post-launch ISR-pilot scope that builds per-tenant cache-isolation + rotation→invalidation verifiers first. See `docs/trust-and-safety-web-ui-rework-2026-05-24.md` Dimension 3 and `docs/framework-feedback-facetheory-v3.3.0-2026-05-24.md` Signal B (recategorized).
4. **Cost telemetry depth** — Lambda + DynamoDB + egress confirmed in the design; should we also include S3, SQS, CloudFront request cost? `provision-managed-instance` + product-decision (Aron) inputs here.
5. **Operator dark chrome** — chat assistant proposed warm-charcoal with amber-on-coffee accents. Does this need explicit visual sign-off from Aron before M2, or accept the prototype's resolution?
6. **Combined-scope acceptance** — surfaced as steward concern: full FaceTheory migration + full backend co-scoping + live-release timing is a substantial bundle. Confirming Aron accepts that this is multi-milestone and not a one-shot ship.
7. **Live cutover canary plan** — once M0–M3 land in `lab`, do we canary to one customer slug (e.g., simulacrum) before opening `live` to general customers? Will be specified in `plan-roadmap`.

---

## Handoff (per scope-need skill)

This scope touches **four specialist surfaces** and needs **all four walks before `enumerate-changes`**:

1. `coordinate-framework-feedback` — FaceTheory v3.3.0 adoption posture, AppTheory ↔ FaceTheory boundary, greater-components additive requests
2. `audit-trust-and-safety` — CSP shape change, OAC posture, public attestation re-skin + ISR consideration
3. `provision-managed-instance` — `wire-mcp` job kind, two-channel release adoption + verification, drift detection, customer-readable stack-state
4. `maintain-governance-rubric` — additive verifiers (JSON-sidecar CSP integrity, release-channel adoption evidence, MCP-wiring step recovery, cost-telemetry redaction)

Recommended walk order: framework first (FaceTheory adoption shapes CSP, which shapes governance), then trust/CSP, then provisioning (independent and can run in parallel after framework), then governance (consolidates additive verifier asks from the prior three). Each walk produces inputs that feed `enumerate-changes`, which then feeds `plan-roadmap`, which then feeds `create-github-project`.
