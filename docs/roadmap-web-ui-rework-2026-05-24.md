# Roadmap: web/ UI rework on FaceTheory v3.3.0 + greater-components

Output of the `plan-roadmap` walk for the 2026-05-24 web/ UI rework. Sequences the enumerated change list (`docs/enumerated-changes-web-ui-rework-2026-05-24.md`, commit `3ccd705`) into phases with dependencies, risks, and rollout discipline. The live-launch window is still TBD (Open Question #1 from the scoped-need); phasing is **date-agnostic, in calendar-week-relative terms** ("week-of-planning-PR-merge", "week-of-M0-lab"), with explicit assumptions Aron can override.

## Goal

Deliver a re-imagined `lesser.host` UI on FaceTheory v3.3.0 + greater-components, with customer-readable Stack card on Portal Instance Detail, two-channel release timeline + Stack Matrix + drift detection + Wire-all remediation on Operator Console, and (deferrable) per-instance real-time cost telemetry — all under strict single-origin CSP via JSON-sidecar hydration, with ten additive governance verifiers locking the new posture in CI. Sequenced for the upcoming live release of `lesser.host` to general customers.

## Classification

UI / UX rework + framework adoption + provisioning evolution + observability addition + governance evolution + AGPL-tracked + framework-feedback. Multi-classification; the bundle reflects deliberately accepted scope.

## Surfaces affected

(Per the enumerated change list cross-section.)

- `web/` SPA: full rebuild on FaceTheory Svelte SSR
- `cdk/`: `AppTheorySsrSite` adoption + `/_facetheory/data/*` S3 behavior; CSP unchanged; existing API/trust origins preserved
- `internal/controlplane/`: 5 new endpoints (1 customer-facing, 4 operator-facing across M1+M2) + deferrable cost-telemetry surface
- `internal/costtelemetry/` (M3 deferrable): new package
- `cmd/cost-telemetry-worker/` (M3 deferrable): new Lambda
- `internal/store/models/cost_telemetry.go` (M3 deferrable): new TableTheory model
- `gov-infra/`: 10 new verifiers across SEC/CON/MAI; pack.json bumps `2026.05.14-m7.2` → `2026.05.24-web.0` → `-web.1` → `-web.2` → optional `-web.3`; threat-model + controls-matrix entries
- `docs/`: ADR 0006, supersession of `docs/frontend-roadmap.md`, refresh of `AGENTS.md` web stack mention

## Sibling-repo coordination

| Repo | Required | What |
| --- | --- | --- |
| lesser | **No** for code changes. Sim canary requires sim to be on a current lesser release; Aron coordinates that out-of-band. | The `POST /mcp/{actor}` contract host's wire-mcp runner depends on stays stable; no PR needed. |
| body | **No** for code changes. | Body release-contract unchanged; no PR needed. |
| soul | **No**. | Namespace contract unchanged. |
| greater (Greater Components) | **Yes** via Aron, contingent on Signal A resolution. Parallel coordination track. | Either accepts shell primitives (Shell/Sidebar/Topbar/Panel/StatCard/SummaryStrip) + additive hosted-platform components (CommandPalette/FleetCard/CostGauge/ActivitySparkline + later ProvisioningTimeline/ReleaseTimeline/StackMatrix), or declines in favor of FaceTheory Stitch + host-bespoke remainder. |
| sim | **Yes** as canary candidate. Coordination begins at M0 lab soak; sim steward confirms canary readiness when M2/M3 lab deploys are in soak. | Sim consumes host's public surfaces (`/.well-known/*`, `/attestations/*`, operator-side surfaces via UI walk-throughs). Reskinned surfaces should be sim-validated before live cutover. |

## Framework coordination

| Framework | Required | Signal |
| --- | --- | --- |
| AppTheory v1.7.0 | **Yes** via Aron. Signal C from framework-feedback walk. | Mixed-auth CloudFront composition under `AppTheorySsrSite`: host carries workaround (hand-wired `addBehavior` for bearer-auth Lambda origins); steward shapes the supported pattern over 1–2 future releases. Additive verifier SEC-8 locks the expected shape. |
| TableTheory v1.8.3 | **No** for M0–M2. **Conditional** for M3 cost-telemetry firehose if `CostTelemetry` model patterns reveal awkwardness; would route through `coordinate-framework-feedback` then. | FaceTheoryIsrMetaStore is available but unused (ISR deferred — see Signal B recategorization). |
| FaceTheory v3.3.0 | **Yes** via Aron. Signals A and C from framework-feedback walk (Signal B withdrawn 2026-05-24 per Arch review; recategorized as host trust-surface conservatism note). | Signal A (Stitch shell vs Greater shell ownership) blocks M0.6–M0.10 until resolved; Signal C (mixed-auth composition) tracked via Signal C above. ISR on trust surfaces deferred to a post-launch separate scope per the conservatism note. |

## External-vendor coordination

| Vendor | Required | What |
| --- | --- | --- |
| Stripe / billing | **No**. | No payments flow change in M0–M2. M3 cost-telemetry firehose touches Cost Explorer but not Stripe. |
| SES / email vendors | **No**. | Email ingress unchanged. |
| AI providers | **No**. | AI worker unchanged. |
| eth_rpc provider | **No**. | No on-chain change. Tip registry UI re-skins existing Safe-ready surfaces unchanged. |
| Safe multisig signers | **No**. | No on-chain mutation; no Safe payload prepared. |
| AWS Cost Explorer + CloudWatch metrics | **Yes** for M3.7–M3.13 (deferrable). | New consumer; cross-account IAM for tenant-account Cost Explorer reads needs to be confirmed available (likely needs per-tenant assume-role grant). Defer-flag this for M3 audit-trust-and-safety re-run. |

## Phases

The rework is structured as **four lab-deployed milestones (M0 → M1 → M2 → M3)** with explicit soak between each. Within each milestone, sub-phases organize the enumerated commits by surface and dependency order. Verifier additions land alongside the code they protect.

### Phase M0.0 — Coordination track (executed 2026-05-24, ongoing)

- **Executed 2026-05-24**:
  - **Signal A resolved** by Aron-direct product decision: Greater Components owns UI primitives across all equaltoai products. M0.6 un-gated.
  - **Signal D request sent** to Greater steward via host_lab MCP email (delivery `delivery-f3c1b7a6f664bb27`, recipient `greater.equaltoai@theorymcp.ai`). Aron coordinates triage with Greater steward through implementation.
  - **Signal C framework issues opened**: [theory-cloud/AppTheory#593](https://github.com/theory-cloud/AppTheory/issues/593) + [theory-cloud/FaceTheory#248](https://github.com/theory-cloud/FaceTheory/issues/248). Per Aron's direction, host waits for upstream resolution before M0.12 CDK adoption.
- **Ongoing**:
  - Signal D triage cadence: Greater steward responds per-component; Aron coordinates.
  - Signal C resolution cadence: theory-cloud stewards respond on the framework issues; M0 lab deploy gates on this.
- **Risk if upstream framework issues take longer than expected**: M0 lab deploy slips; rest of M0 work that doesn't depend on M0.12 can still merge to main on the planning branch (Phase M0.1, M0.3, M0.5, M0.6 contingent on Signal D triage, M0.21–M0.23 change-lock verifiers).

### Phase M0.1 — Foundation (no shell adoption yet)

- **Items**: M0.1 (FaceTheory dep), M0.2 (design tokens), M0.3 (FaceTheory bootstrap), M0.4 (port routes), M0.5 (OAC transport bootstrap)
- **Dependencies**: M0.1 before M0.3; M0.2 before any component re-use; M0.3 before M0.4
- **Parallelizable**: M0.2 alongside M0.1–M0.3
- **Risks**: FaceTheory v3.3.0 build path edge cases for Svelte 5 SSR (mitigation: M0 lab soak; rollback by reverting M0.3–M0.4 if needed); strict-CSP-via-JSON-sidecar regression in browser devtools (mitigation: web build-time test in M0.15)

### Phase M0.2 — CDK + tests (parallel with Phase M0.1)

- **Items**: M0.12 (CDK AppTheorySsrSite), M0.13 (CDK composition test), M0.14 (CDK CSP test), M0.15 (web HTML inline absence test), M0.16 (web OAC form test)
- **Dependencies**: M0.12 before M0.13, M0.14
- **Parallelizable**: M0.15, M0.16 alongside M0.12
- **Risks**: AppTheorySsrSite composition with existing bearer-auth Lambda origins (Signal C) may require iteration if `addBehavior` patterns don't compose cleanly (mitigation: SEC-8 verifier locks the expected shape; iterate in M0.12 commits as needed)

### Phase M0.3 — Gated UI components (Signal A + D)

- **Items**: M0.6 (shell adoption), M0.7 (CommandPalette), M0.8 (FleetCard), M0.9 (CostGauge), M0.10 (ActivitySparkline)
- **Dependencies**: M0.6 before M0.7–M0.10 (component layout uses shell primitives); M0.7–M0.10 parallelizable amongst themselves
- **Gates**: Signal A blocks M0.6; Signal D shapes M0.7–M0.10 (Greater additive or host bespoke)
- **Risks**: Signal A response delay beyond Week 4 → adopt Stitch provisionally per enumerated change gating note; revisit when upstream clarifies. Signal D decline → host carries bespoke components (acceptable per the framework-feedback walk's workaround posture)

### Phase M0.4 — Portal Fleet wire (proof-of-concept page)

- **Items**: M0.11 (Portal Fleet page)
- **Dependencies**: M0.3 (shell adopted) — depends on Phase M0.3 completion
- **Risks**: page rendering issues caught by lab soak (mitigation: dedicated soak time before M1 begins)

### Phase M0.5 — Verifier additions

- **Items**: M0.17 (SEC-5), M0.18 (SEC-6), M0.19 (SEC-7), M0.20 (SEC-8), M0.21 (SEC-9), M0.22 (SEC-10), M0.23 (CON-4)
- **Dependencies**: M0.12 + tests (Phase M0.2) for SEC-5/6/7/8; M0.4 (Phase M0.1) for SEC-9 / SEC-10 / CON-4 (change-locks; no new code, just diff assertion)
- **Parallelizable**: all 7 verifier additions can run in parallel after their dependencies
- **Risks**: verifier flakiness (mitigation: each verifier reads in-repo state only; no external API; verifier-version pins per existing convention)

### Phase M0.6 — Governance + docs

- **Items**: M0.24 (pack.json bump), M0.25 (threat-model), M0.26 (controls-matrix), M0.27 (ADR 0006), M0.28 (supersede frontend-roadmap), M0.29 (AGENTS.md refresh)
- **Dependencies**: M0.24 after all M0 verifiers (M0.17–M0.23) land; M0.25/M0.26 alongside M0.24; M0.27/M0.28/M0.29 parallelizable
- **Risks**: none material

### Phase M0.7 — Lab deploy + soak

- **Command**: `AWS_PROFILE=Lesser theory app up --stage lab --execute`
- **Soak duration**: minimum 5 calendar days (per host's existing soak convention from memory: 2026-05-18 / 2026-05-22 / 2026-05-23 lab deploys are followed by managed-update canary; here soak focuses on UI surfaces)
- **Soak criteria**:
  - `https://lab.lesser.host` returns HTTP 200 with strict CSP byte-string preserved
  - No CSP violations in browser devtools across all ported routes
  - JSON sidecars at `/_facetheory/data/*` serve from S3 with expected cache headers
  - `/api/*`, `/auth/*`, `/setup/*`, `/.well-known/*`, `/attestations/*` continue to serve from existing bearer-auth Lambdas with identical behavior to pre-rework
  - Portal Fleet page renders with shell + new components; ⌘K palette opens; cost gauge displays budget data
  - Gov-infra verifiers all PASS (36 = existing 29 + new 7)
  - Lab CloudWatch error rate per Lambda steady-state at pre-rework baseline

### Phase M1.1 — Customer hero pages

- **Items**: M1.1 (Account), M1.2 (Souls + Soul Detail), M1.3 (Trust), M1.4 (Billing), M1.5 (Instance Detail shell), M1.6 (Overview tab + Stack card), M1.7 (Config tab), M1.8 (Domains tab), M1.9 (Keys tab), M1.10 (Souls tab)
- **Dependencies**: M0.7 (M0 lab soak complete); M1.5 before M1.6–M1.10 (tabs need shell)
- **Parallelizable**: M1.1–M1.4 amongst themselves; M1.6–M1.10 amongst themselves after M1.5
- **Risks**: Keys tab re-skin must preserve one-time-reveal contract (mitigation: SEC-9 verifier ongoing; visual review of Keys tab against the prototype's `portal-pages-2.jsx` to confirm copy-once-with-warnings shape)

### Phase M1.2 — Backend + Cost & usage tab

- **Items**: M1.11 (stack-state endpoint + tests), M1.12 (cost-and-usage minimum API), M1.13 (Cost & usage tab)
- **Dependencies**: M1.11 before M1.6 (Overview consumes); M1.12 before M1.13
- **Parallelizable**: M1.11 and M1.12 amongst themselves; M1.13 depends on M1.12 but not M1.11
- **Risks**: stack-state aggregation correctness across edge cases (no update yet, body not installed, MCP not wired — covered by tests in M1.11)

### Phase M1.3 — Governance + pack bump

- **Items**: M1.14 (SEC-11), M1.15 (threat-model T-TENANT-LEAK-001), M1.16 (controls-matrix C-TENANT-SCOPE), M1.17 (pack 2026.05.24-web.1)
- **Dependencies**: M1.11 (handler exists) before M1.14; M1.14 before M1.17
- **Risks**: none material

### Phase M1.4 — Lab deploy + soak

- **Command**: `AWS_PROFILE=Lesser theory app up --stage lab --execute`
- **Soak duration**: minimum 3 calendar days
- **Soak criteria**:
  - Portal hero pages render correctly on lab; tabs navigable
  - Stack card consumes stack-state endpoint; drift warning fires when MCP-wire-stale (test against an intentionally-drift instance in lab)
  - Customer-side ownership check fails closed when requesting another slug (smoke test)
  - Gov-infra verifiers PASS (37 = M0 36 + SEC-11)

### Phase M2.1 — Operator shell + Provisioning evolution

- **Items**: M2.1 (Operator dark chrome shell), M2.2 (Dashboard re-skin), M2.3 (Provisioning list + kind labels), M2.4 (Provisioning job detail + timeline), M2.13 (timeline refinement for four job kinds)
- **Dependencies**: M1.4 (M1 lab soak complete); M2.1 before M2.2–M2.4
- **Parallelizable**: M2.2 alongside M2.3 + M2.4 + M2.13
- **Open question** (Open Question #5 from scoped-need): Operator dark chrome — does Aron want explicit visual sign-off before M2.1 ships? Default: accept the prototype's warm-charcoal + amber-on-coffee resolution. If Aron requests review, M2.1 sits as draft until approved.

### Phase M2.2 — Operator backend: aggregation + drift + remediation

- **Items**: M2.8 (releases aggregation handler), M2.9 (drift detection handler), M2.10 (wire-all remediation handler)
- **Dependencies**: none on each other; M2.10 depends on the drift computation helper introduced in M2.9 (so M2.9 first or land them in PR with the helper extracted)
- **Risks**: drift computation correctness (covered by M2.9 tests including wire-stale case); idempotency of wire-all (covered by MAI-5 verifier in M2.15); cross-tenant isolation of operator aggregation (covered by SEC-12 verifier in M2.14)

### Phase M2.3 — Operator UI: timeline + Stack Matrix + wire-all

- **Items**: M2.5 (releases page), M2.6 (Stack Matrix component), M2.7 (release timeline component), M2.11 (wire Stack Matrix to drift), M2.12 (wire all CTA)
- **Dependencies**: M2.1 (Operator shell) + M2.6/M2.7 components first; M2.5 composes them; M2.11/M2.12 depend on M2.5 + M2.8/M2.9/M2.10 backend
- **Parallelizable**: M2.6, M2.7 amongst themselves

### Phase M2.4 — Governance + pack bump

- **Items**: M2.14 (SEC-12), M2.15 (MAI-5), M2.16 (threat-model 3 entries), M2.17 (controls-matrix 2 entries), M2.18 (pack 2026.05.24-web.2)
- **Dependencies**: M2.8/M2.9/M2.10 (operator handlers) before M2.14; M2.10 (wire-all) before M2.15

### Phase M2.5 — Lab deploy + soak

- **Soak duration**: minimum 5 calendar days (longer due to operator-side scope + drift remediation flow)
- **Soak criteria**:
  - Operator dark-chrome chrome visually distinct from Portal (operator login as Aron, verify)
  - `/operator/releases` page renders two-channel timeline with adoption bars sourced from aggregation endpoint
  - Stack Matrix populates with current fleet state; drift warnings fire on intentionally-drift instance
  - Wire-all CTA triggers MCP-only UpdateJob per affected slug; idempotent on re-click (no duplicate job)
  - Gov-infra verifiers PASS (39 = M1 37 + SEC-12 + MAI-5)

### Phase M3.1 — Operator Console balance (always lands)

- **Items**: M3.1 (Approvals), M3.2 (Audit explorer), M3.3 (Operator Instances), M3.4 (Tip registry), M3.5 (Soul registry), M3.6 (attestation inspector re-skin)
- **Dependencies**: M2.5 (M2 lab soak complete)
- **Parallelizable**: all 6 amongst themselves

### Phase M3.2 — Cost-telemetry firehose (deferrable)

- **Items**: M3.7 (worker scaffolding), M3.8 (CloudWatch collection), M3.9 (Cost Explorer integration), M3.10 (DynamoDB cache), M3.11 (cost endpoint), M3.12 (UI wire), M3.13 (SEC-13 verifier), M3.14 (pack bump if M3.13 lands)
- **Dependencies**: M3.7 first; M3.8/M3.9 parallelizable after M3.7; M3.10 after M3.8/M3.9; M3.11 after M3.10; M3.12 after M3.11; M3.13 after M3.11; M3.14 last
- **Defer condition**: if Aron decides live launch is tight, skip Phase M3.2 entirely; ship M3 as Phase M3.1 only and treat Cost-telemetry firehose as a post-launch separate scope
- **Risks**: cross-account Cost Explorer + CloudWatch read IAM may need per-tenant assume-role grants (mitigation: prototype against a single lab-tenant before fanning out); cost-telemetry redaction (SEC-13 audit-trust-and-safety re-run gates the verifier shape; coordinate with audit-trust-and-safety walk re-run at M3 start)

### Phase M3.3 — Lab deploy + soak

- **Soak duration**: minimum 3 calendar days (longer if Phase M3.2 lands, +2 days for cost-telemetry firehose validation)
- **Soak criteria**:
  - Remaining operator surfaces render; attestation inspector renders attestations correctly with new skin and existing MarkdownRenderer mandatory-sanitization preserved
  - If Phase M3.2 landed: cost endpoint returns per-instance per-day breakdown for an authorized customer; cost-telemetry-worker runs scheduled and produces evidence
  - Gov-infra verifiers PASS (39 or 40, depending on whether SEC-13 lands)

### Phase R.1 — Sim canary (post-M3 lab soak)

- **Coordinate with sim steward** via Aron. Sim performs walk-through against host's `lab` environment:
  - Customer login flow + Portal hero pages
  - Operator login flow + Operator Console
  - Stack Matrix + wire-all flow
  - Attestation inspector
  - Any sim-specific UI integration paths
- **Sim feedback** triggers any final touch-up PRs (small, scoped, lab-redeployed); rinse and repeat until sim signs off

### Phase R.2 — Live cutover

- **Authorization**: Aron explicitly approves; sim canary signed off; all four milestones merged + soaked
- **Command**: `AWS_PROFILE=Lesser theory app up --stage live --execute`
- **Cutover window**: out-of-business-hours; CloudFront cache invalidation timed; SSR Lambda alias swap for fast rollback if used
- **Post-deploy monitoring** (per gov-infra and host operations convention):
  - CloudWatch error rate per Lambda (SSR Lambda, control-plane-api, trust-api, workers)
  - CloudFront 4xx / 5xx rates per surface
  - `https://lesser.host` returns HTTP 200; CSP byte-string matches expected
  - JSON sidecars served from S3 under expected cache headers
  - Existing API behaviors unchanged: bearer-auth flows work, instance-auth flows work, attestation endpoints work
  - Operator login + Portal customer login both succeed
  - Stack Matrix populates (real fleet data, not lab-test data)
  - Wire-all flow idempotency verified live (intentional re-click of any visible MCP-drift item)
  - Sim integration continues to work (no integration regression)
  - Gov-infra evidence freshness (no stale artifacts)
- **Rollback path** (see Rollback plan below)

## Stage rollout plan (host's own service)

### Lab

| Milestone | Command | Soak | Criteria |
| --- | --- | --- | --- |
| M0 | `theory app up --stage lab --execute` | ≥5 days | CSP preserved; Portal Fleet POC works; 7 new verifiers PASS |
| M1 | `theory app up --stage lab --execute` | ≥3 days | Hero pages + Stack card; SEC-11 PASS |
| M2 | `theory app up --stage lab --execute` | ≥5 days | Operator console + dark chrome + wire-all; SEC-12 + MAI-5 PASS |
| M3 | `theory app up --stage lab --execute` | ≥3 days (+2 if M3.2 lands) | Operator balance + (optional) cost-telemetry firehose |

### Live

- **Command**: `theory app up --stage live --execute`
- **Authorization**: explicit Aron approval; sim canary signed off; all four milestones soaked
- **Post-deploy monitoring**: enumerated in Phase R.2 above

## On-chain rollout plan

**Not applicable.** No contract changes in this rework. TipSplitter and soul-registry contracts untouched. No Sepolia or mainnet deploy.

## Managed-instance rollout plan

**Not applicable for the rework itself.** No provisioning-pipeline mutation (additive endpoints only). The wire-all remediation creates `UpdateJob{MCPOnly: true}` per affected slug; these go through the existing managed-update flow with its existing recovery + idempotency. Operators have always been able to trigger MCP-only updates via direct API; the rework adds UI ergonomics, not new managed-update mechanics.

## Release artifact plan

- **GitHub Release tags**: per host's existing version-tag convention (`v0.5.x` series likely; Aron sets the version when M0 lands). Each milestone merge to `main` may produce a tag (M0 → v0.5.0; M1 → v0.6.0; M2 → v0.7.0; M3 → v0.8.0) or one tag at live cutover (`v1.0.0` for the full rework). Default: per-milestone tags for fast rollback.
- **Release notes**: each tag includes the milestone's scope summary, the verifier additions, the pack.json bump, and a "no breaking API change" assertion (the rework is additive at the API layer).
- **No managed-consumer impact**: host doesn't publish artifacts consumed by sibling repos for this rework (lesser/body release ingestion is unchanged).

## Rollback plan

### Per-milestone (before live cutover)

- **Revert** the milestone merge commit on `main`
- **Redeploy lab** via `theory app up --stage lab --execute`
- **No customer impact** because live hasn't cut over yet
- **pack.json**: revert the pack.json bump alongside; verifier count returns to pre-milestone

### Post-live cutover

- **SSR Lambda alias swap** (if used): swap CloudFront default-origin Lambda alias back to the prior version; CloudFront cache invalidation; fast revert path (minutes)
- **CDK revert + redeploy live** (if alias swap insufficient): `git revert` the live cutover commit; `theory app up --stage live --execute`; CloudFront cache invalidation. Slower path (CDK deploy + CloudFront propagation).
- **Verifier rollback**: pack.json revert; rarely advisable (governance event; document the rationale)

### Per-slug rollback

- **Not applicable** for the rework itself (no provisioning-pipeline change). For wire-all remediation: if a per-slug MCP-only UpdateJob fails, the existing per-slug recovery flow applies; no rework-introduced rollback path.

### Rollback edge case: SEC-9 / SEC-10 change-locks

- These verifiers lock specific files against semantic diff. If a milestone PR inadvertently breaks them, the PR doesn't merge. If a verifier itself fails post-merge (false-positive), it's a verifier bug, not a code bug; investigate-issue + maintain-governance-rubric walk produces the fix.

## AGPL posture

- No proprietary blobs introduced.
- Dependency license vetting: `@theory-cloud/facetheory` (AGPL-compatible, Theory Cloud stack); greater-components additive (AGPL via greater steward); no new third-party deps for OAC form transport (in-FaceTheory).
- Standard contributor-origin transparency per repo convention.

## Advisor-brief authorization

**Not applicable.** Aron-direct, not advisor-dispatched.

## Risk register

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| ~~Signal A (Stitch vs Greater shell ownership) unresolved~~ | n/a | n/a | **Resolved 2026-05-24** Aron-direct: Greater owns. Risk closed. |
| FaceTheory v3.3.0 build path edge case for Svelte 5 SSR | low | medium (could delay M0 lab soak) | Lab soak catches; rollback by reverting M0.3–M0.4 if needed; consult Theory Cloud KB / FaceTheory steward (FaceTheory#248 also asks for strict-CSP Svelte adapter confirmation) |
| **AppTheorySsrSite composition with bearer-auth Lambda origins (Signal C) — upstream resolution wait** | medium | **high** (M0 lab deploy gates on it) | Framework issues opened at theory-cloud/AppTheory#593 + theory-cloud/FaceTheory#248 (2026-05-24); Aron follows up. host waits per Aron's direction; rest of M0 work that doesn't depend on M0.12 can merge to main on planning branch in the meantime. Mitigation if wait extends materially: Aron decides whether to ship a temporary M0 workaround (hand-wired addBehavior with SEC-8 verifier locking) ahead of upstream resolution. |
| Greater steward declines part of Signal D expanded list (shell primitives + hosted-platform components) | medium (list is larger than originally scoped) | medium (host carries bespoke for declined items) | Host carries bespoke per framework-feedback workaround posture; acceptable; revisit on future Greater release. Per-component bespoke fallback contract: same import-surface as the Greater equivalent so future swap is mechanical. |
| Sim canary readiness delay (sim not on current lesser release) | medium | medium (delays live cutover) | Aron coordinates sim release upgrade alongside M2 lab; if sim isn't ready by R.1, pick alternate canary slug (or accept reduced canary coverage) |
| Operator dark-chrome visual sign-off feedback (Open Question #5) | low | low (one revision cycle) | Accept prototype resolution as default; Aron reviews at M2.1 commit if desired |
| Live launch window unknown (Open Question #1) | high | varies | Roadmap is week-relative; Aron sets calendar dates at any time; M3.2 deferrable for tight launches |
| Cost-telemetry per-tenant cross-account IAM not pre-arranged | medium | medium (M3.2 delay if landed) | Prototype against single lab tenant first; defer M3.2 entirely if IAM grants aren't pre-arranged |
| SEC-9 / SEC-10 false-positive on legitimate non-semantic diff (e.g., import reorder by gofmt) | low | low (one PR cycle to refine verifier) | Verifier treats whitespace + import-order as non-semantic per design; if it fires on legitimate non-change, refine via investigate-issue + maintain-governance-rubric walk |
| Multi-tenant cost-telemetry redaction regression (M3.2) | low | high (cross-tenant data leak) | Audit-trust-and-safety re-run at M3 start; SEC-13 verifier; integration test asserts no cross-tenant field in customer cost response |
| CSP regression from FaceTheory adoption | low | high (XSS exposure) | SEC-5/6 verifiers; lab soak browser-devtools check; CSP byte-string locked at M0.14 test |
| Wire-all flood (operator triggers repeatedly) | low | medium (CodeBuild + cost spike) | MAI-5 verifier (idempotency via GSI2 = UPDATE_ACTIVE); operator audit emits per-action; rate-limit later if needed |

## Open questions

1. **Live release window / target date** — still TBD from scope-need. Roadmap is calendar-week-relative; once Aron sets a date, the roadmap maps `theory app up --stage live --execute` (Phase R.2) backwards from it.
2. ~~**Signal A resolution** — Stitch or Greater?~~ — **Resolved 2026-05-24** Aron-direct: Greater owns UI primitives across all equaltoai products.
3. **Signal D triage** — Greater steward triages the expanded additive list (shell primitives + hosted-platform components); Aron coordinates through implementation.
4. **Operator dark-chrome visual sign-off** — accept prototype resolution, or Aron reviews at M2.1? Default: accept.
5. **M3 cost-telemetry firehose: land or defer?** — depends on live launch window tightness. Default: include if launch is ≥10 weeks out; defer if tighter.
6. **Sim canary readiness** — sim must be on a current lesser release before Phase R.1; Aron coordinates with sim steward.
7. **Release tag cadence** — per-milestone tags (v0.5/v0.6/v0.7/v0.8) or single tag at live cutover (v1.0)? Default: per-milestone for fast rollback.

## Calendar-week-relative phasing (assumption-explicit)

Assuming a 5-week-per-milestone cadence with overlapping coordination tracks:

| Week | Activity |
| --- | --- |
| Week 0 | Planning PR opens (this PR) |
| Week 1 | Planning PR merged. Aron sends Signal A + Signal D to Greater + FaceTheory stewards. M0.1–M0.5 begin merging. CDK adoption (M0.12) starts. |
| Week 2 | M0 ungated commits (M0.1–M0.5, M0.12–M0.16, M0.27–M0.29) merge to main. Verifier scaffolding M0.17–M0.23 begins. |
| Week 3 | M0 verifiers + pack bump + threat-model + controls-matrix land. M0.6–M0.10 land if Signal A resolved; otherwise provisional Stitch by end-of-week. M0.11 (Portal Fleet) lands when Phase M0.3 complete. **M0 lab deploy.** |
| Week 4 | M0 lab soak (5 days). M1 PRs prepared in parallel. |
| Week 5 | M1.1–M1.10 (Portal hero pages) merge. M1.11–M1.12 (backend) parallel. |
| Week 6 | M1.13 (Cost & usage tab), M1.14–M1.17 (verifier + pack bump). **M1 lab deploy.** M1 lab soak (3 days). |
| Week 7 | M2 PRs begin. M2.1–M2.4 (Operator shell + Provisioning evolution). M2.8–M2.10 (backend) parallel. |
| Week 8 | M2.5–M2.7 (operator UI components), M2.11–M2.13 (wire-up). |
| Week 9 | M2.14–M2.18 (verifier + pack bump). **M2 lab deploy.** M2 lab soak (5 days). |
| Week 10 | M3.1–M3.6 (Operator balance + attestation re-skin). Decision: include M3.2 cost-telemetry firehose? |
| Week 11 | If M3.2 included: M3.7–M3.14. Else: skip directly to M3 lab deploy. |
| Week 12 | **M3 lab deploy.** M3 lab soak (3–5 days). |
| Week 13 | Sim canary coordination (Phase R.1). Aron + sim steward walk-through. |
| Week 14 | Sim feedback touch-ups + any final fixes. |
| Week 15 | **Live cutover** (Phase R.2). |

**Aggressive variant** (8 weeks total): compress soak periods to minimum; defer M3.2 entirely; sim canary in week 7; live cutover in week 8. Acceptable risk only if Aron explicitly authorizes.

**Conservative variant** (20+ weeks): extend each soak period; longer sim canary; multiple canary slugs.

The 15-week pacing is the default recommendation. Aron sets the actual calendar.

## Proposed next skill

If approved, invoke `create-github-project` to translate this roadmap into the equaltoai-org-level Projects v2 kanban.

If the roadmap surfaces coordination not yet happening (Signal A/D unsent to stewards, sim steward not yet briefed on canary expectation, live launch date still TBD), pause and surface first.

If the roadmap reveals scope growth that didn't surface earlier, revisit `scope-need`.
