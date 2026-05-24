# Enumerated changes — web/ UI rework on FaceTheory v3.3.0 — 2026-05-24

Output of the `enumerate-changes` walk for the 2026-05-24 web/ UI rework. Consolidates the four specialist walks into a flat ordered list of discrete commits, M0 → M3. Each item is scoped to one commit; each commit builds cleanly in isolation; verifier additions interleave at the correct milestone boundaries per the governance walk's pack.json bump plan.

Inputs (all on `aron/web-ui-rework-planning`):
- `docs/scoped-need-web-ui-rework-2026-05-24.md` (22cbd22)
- `docs/framework-feedback-facetheory-v3.3.0-2026-05-24.md` (5579350)
- `docs/trust-and-safety-web-ui-rework-2026-05-24.md` (24dcd86)
- `docs/provisioning-web-ui-rework-2026-05-24.md` (e1aa36a)
- `docs/governance-rubric-web-ui-rework-2026-05-24.md` (b4b38c5)

## Gating notes (updated 2026-05-24 with Signal A/C/D resolutions)

- ~~**Signal A gate (Stitch vs greater-components shell ownership)**~~ — **Resolved 2026-05-24** by Aron-direct product decision: Greater Components owns UI primitives across all equaltoai products. M0.6 un-gated (now reads: adopt shell primitives from `@equaltoai/greater-components/shell` once they ship from Greater per Signal D triage). M0.7–M0.10 gating note narrows to "Signal D triage only" (Greater steward acceptance per-component).
- **Signal C gate (AppTheory + FaceTheory composition under one CloudFront distribution)**: framework issues opened 2026-05-24 at [theory-cloud/AppTheory#593](https://github.com/theory-cloud/AppTheory/issues/593) + [theory-cloud/FaceTheory#248](https://github.com/theory-cloud/FaceTheory/issues/248). Per Aron's direction, **host waits for upstream resolution before moving forward with M0.12 (CDK adoption of `AppTheorySsrSite`)**. M0.13 + M0.14 + SEC-8 verifier all depend on M0.12 and inherit the wait. **M0 lab deploy (Phase M0.7) gates on the framework issues' resolution.** M0 work that doesn't depend on the composition pattern may proceed unblocked (M0.1–M0.5 FaceTheory bootstrap + route port + OAC transport bootstrap; M0.15–M0.16 web build-time tests; M0.17 SEC-5 + M0.18 SEC-6 + M0.19 SEC-7 + M0.21 SEC-9 + M0.22 SEC-10 + M0.23 CON-4 verifiers; M0.24 pack bump can defer until SEC-8 is ready; M0.25–M0.29 governance + docs).
- **Signal D gate (Greater-components additive request triage)**: request sent 2026-05-24 via host_lab MCP `email_send` (delivery `delivery-f3c1b7a6f664bb27`) to `greater.equaltoai@theorymcp.ai`. Aron coordinates triage with Greater steward through implementation. M0.6–M0.10 land as Greater accepts components (vendored via `greater add ...`) or as host-bespoke fallback (per the workaround posture in the framework-feedback walk).
- **Live-launch window unknown**: M3 cost-telemetry firehose (M3.7–M3.13) is marked "deferrable" — if live launch is tight, M3 ships as Operator Console balance only and the cost-telemetry firehose lands post-launch as a separate scope.
- **Wire-mcp coordination**: resolved as host-internal in the provisioning walk; no lesser/body steward PR coordination needed.

## Cross-cutting validation contract (applies to every item below)

Every commit passes locally:

- `go test ./...` PASS, `go vet ./...` clean, `gofmt -l .` empty
- `cd cdk && npm test && npm run synth` PASS for both `lab` and `live` stage contexts where touched
- `cd web && npm run lint && npm run typecheck && npm test && npm run build` PASS where touched
- `bash gov-infra/verifiers/gov-verify-rubric.sh` PASS at expected verifier count
- No raw API keys, wallet private keys, mint-signer keys, partner credentials, or `.env` files committed
- No CSP loosening, no instance-auth contract change, no release-verification skip, no multi-tenant boundary traverse

Each commit's body explains the *why*, ends with `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`, and references the relevant scope-need / specialist walk doc.

---

## M0 — FaceTheory foundation + design system + shell + Portal Fleet POC

**Goal**: prove end-to-end that FaceTheory v3.3.0 + Svelte adapter + strict-CSP JSON-sidecar hydration + new shell renders Portal Fleet correctly in `lab` behind CloudFront with the unchanged CSP byte-string. M0 lands the seven foundation verifiers (SEC-5/6/7/8/9/10 + CON-4) and bumps `pack.json` to `2026.05.24-web.0`. Shell-primitive commits (M0.6–M0.10) gate on Signal D triage by Greater steward (Signal A resolved 2026-05-24 — Greater owns shell). CDK adoption commit M0.12 (and dependent SEC-8) gate on Signal C resolution via [theory-cloud/AppTheory#593](https://github.com/theory-cloud/AppTheory/issues/593) + [theory-cloud/FaceTheory#248](https://github.com/theory-cloud/FaceTheory/issues/248) — M0 lab deploy gates on those framework issues per Aron's wait-on-upstream direction.

### M0.1. Add FaceTheory v3.3.0 dependency

- **Paths**: `web/package.json`, `web/package-lock.json`
- **Surface**: web / deps
- **Classification**: framework-feedback (idiomatic adoption); dependency-maintenance
- **Governance-rubric impact**: none (verifiers land later in M0)
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none (dependency add only)
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic (pinned to v3.3.0; peer `svelte@^5.55.7` already satisfied)
- **Acceptance**: `npm ci && npm run typecheck && npm test && npm run build` PASS; `@theory-cloud/facetheory` resolves; no other dependency change.
- **Validation**: web build pipeline; gov rubric (no new verifiers expected to assert yet); package-lock determinism.
- **Conventional Commit subject**: `chore(web): pin @theory-cloud/facetheory v3.3.0`

### M0.2. Add design tokens scaffold

- **Paths**: `web/src/lib/tokens/ds.css`, `web/src/lib/tokens/gr-bridge.css`, `web/src/lib/tokens/index.ts`
- **Surface**: web
- **Classification**: dependency-maintenance / additive UI scaffolding
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none (CSS-only)
- **Framework consumption**: idiomatic
- **Acceptance**: `--ds-*` tokens defined per the Claude Design handoff's `docs/design/web-ui-rework-2026-05-24/project/assets/tokens.css`; `--gr-*` bridge maps to existing greater-components tokens; no consumers yet (added incrementally as components rebuild).
- **Validation**: web lint + build; CSS contains no `:global()` in plain CSS (closes the 2026-05-16 LightningCSS feedback locally).
- **Conventional Commit subject**: `feat(web): add design-system tokens + greater-components bridge`

### M0.3. Bootstrap FaceTheory app + Svelte adapter (no routes yet)

- **Paths**: `web/src/server/face-app.ts`, `web/src/client/face-client.ts`, `web/vite.config.ts`, `web/svelte.config.js`
- **Surface**: web
- **Classification**: framework-feedback (idiomatic adoption)
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none (no behavior change yet)
- **Framework consumption**: idiomatic (`createFaceApp`, `createSvelteFace`, `renderSvelte`)
- **Acceptance**: a minimal FaceModule exists (e.g. `/`) renders SSR-buffered with strict-CSP-via-JSON-sidecar hydration; `npm run build` produces SSR Lambda bundle + S3 sidecar bundle; existing pages still build via existing Vite path until M0.4 ports them.
- **Validation**: `npm run build` PASS; manual smoke (vite dev) renders the minimal FaceModule with no CSP violation in browser devtools.
- **Conventional Commit subject**: `feat(web): bootstrap FaceTheory v3.3.0 Svelte SSR app shell`

### M0.4. Port existing Portal/Operator routes to FaceModule shell with strict-CSP hydration

- **Paths**: `web/src/routes/**`, `web/src/server/face-app.ts`
- **Surface**: web
- **Classification**: framework-feedback (idiomatic adoption); UI-rework foundation
- **Governance-rubric impact**: none (verifiers land later in M0)
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: preserves (no contract change; only delivery-path change)
- **Framework consumption**: idiomatic; uses `readFaceHydrationData` for hydration
- **Acceptance**: every existing page renders via a FaceModule; hydration data lives at `/_facetheory/data/*`; no inline scripts, no inline styles in built HTML; existing fetch-based API calls to `/api/*`, `/auth/*`, `/.well-known/*`, `/attestations/*` work unchanged.
- **Validation**: web lint + build + tests; manual smoke each page in vite dev; built `web/dist/**/*.html` contains no inline `<script>` / `<style>` / event handlers.
- **Conventional Commit subject**: `feat(web): port routes to FaceTheory FaceModule with JSON-sidecar hydration`

### M0.5. Add OAC-safe form transport bootstrap

- **Paths**: `web/src/client/oac-form.ts`, `web/src/client/face-client.ts`
- **Surface**: web
- **Classification**: trust-API / CSP / instance-auth (preserves)
- **Governance-rubric impact**: none (SEC-7 verifier lands later in M0)
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: preserves; sets up OAC-safe path for any future SSR-route form
- **Framework consumption**: idiomatic (`startAwsOacFormTransport()` imported and invoked once at client bootstrap)
- **Acceptance**: client bootstrap loads transport once; no marked OAC forms exist yet (no `data-facetheory-oac-form` in built HTML), but the transport is staged for later.
- **Validation**: web lint + build; transport import present in built bundle (grep `startAwsOacFormTransport` in built JS).
- **Conventional Commit subject**: `feat(web): wire startAwsOacFormTransport at client bootstrap`

### M0.6. **[Signal A resolved; Signal D triage pending]** Adopt greater-components shell primitives

- **Paths**: `web/src/lib/shell/**` (new directory; re-exports from `@equaltoai/greater-components/shell` once those primitives ship from Greater per Signal D triage)
- **Surface**: web
- **Classification**: framework-feedback (Greater-side adoption)
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Framework consumption**: idiomatic (Greater owns shell primitives across all equaltoai products per Aron's 2026-05-24 resolution)
- **Acceptance**: a single import surface at `web/src/lib/shell/` exposes Shell, Sidebar, Topbar, Panel, StatCard, SummaryStrip, Section, PageFrame, PageTitle, Breadcrumb, Callout from `@equaltoai/greater-components/shell` (vendored via `greater add ...`).
- **Validation**: web lint + build + tests; greater-components vendored tree updated with the new primitives.
- **Conventional Commit subject**: `feat(web): adopt greater-components shell primitives`
- **Gating note**: depends on Greater steward acceptance of the shell primitives in Signal D triage. Fallback if any primitive is declined: bespoke implementation under `web/src/lib/shell/` matching the same import-surface contract.

### M0.7. **[Signal D triage]** Command palette (⌘K)

- **Paths**: `web/src/lib/components/CommandPalette.svelte` (host-bespoke) OR vendored from greater-components if accepted
- **Surface**: web
- **Classification**: framework-feedback / additive UI
- **Governance-rubric impact**: none
- **Acceptance**: ⌘K opens a modal palette with fuzzy search, scoped result groups (instances / souls / actions / navigation), keyboard navigation, focus-trap on open, ESC closes.
- **Validation**: web lint + build + tests; component-level a11y test.
- **Conventional Commit subject**: `feat(web): add command palette (⌘K)`

### M0.8. **[Signal D triage]** Fleet card component

- **Paths**: `web/src/lib/components/FleetCard.svelte`
- **Acceptance**: card renders with slug, status pulse dot, cost gauge slot, sparkline slot, metadata line.
- **Validation**: web lint + build + tests; storybook-style snapshot.
- **Conventional Commit subject**: `feat(web): add fleet card component`

### M0.9. **[Signal D triage]** Cost gauge component

- **Paths**: `web/src/lib/components/CostGauge.svelte`
- **Acceptance**: radial gauge with current/limit/currency props; colored arc indicates consumption; ARIA labels for screen readers.
- **Conventional Commit subject**: `feat(web): add cost gauge component`

### M0.10. **[Signal D triage]** Activity sparkline component

- **Paths**: `web/src/lib/components/ActivitySparkline.svelte`
- **Acceptance**: inline sparkline takes numeric series + optional baseline; SVG-rendered for crisp scaling.
- **Conventional Commit subject**: `feat(web): add activity sparkline component`

### M0.11. Wire Portal Fleet page using shell + new components

- **Paths**: `web/src/routes/portal/fleet/+page.svelte`, `web/src/routes/portal/fleet/+page.server.ts` (FaceModule data loader)
- **Surface**: web
- **Classification**: UI-rework
- **Acceptance**: Portal Fleet renders fleet cards consuming existing `GET /api/v1/portal/instances` endpoint; per-card cost gauge currently shows budget-only data from existing budget endpoints (real-time cost telemetry deferred to M3); ⌘K palette accessible from any portal route.
- **Validation**: web lint + build + tests; manual smoke in lab against real backend.
- **Conventional Commit subject**: `feat(web): wire Portal Fleet page on new shell`

### M0.12. **[BLOCKED on Signal C — wait for upstream]** CDK: AppTheorySsrSite + `/_facetheory/data/*` behavior + preserve existing API origins

- **Paths**: `cdk/lib/lesser-host-stack.ts`, `cdk/lib/web-ssr-site.ts` (or inline)
- **Surface**: cdk
- **Classification**: framework-feedback (Signal C composition); CSP-shape change
- **Governance-rubric impact**: none directly (SEC-8 + SEC-5 verifiers land in M0.17-M0.20)
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: preserves; CSP byte-string in `webCsp` / `safeAppCsp` unchanged; new behavior for `/_facetheory/data/*` (S3, no Lambda); existing `/api/*`, `/auth/*`, `/setup/*`, `/.well-known/*`, `/attestations/*` behaviors unchanged
- **Framework consumption**: idiomatic per `AppTheorySsrSite` contract once Signal C is resolved upstream; until then host waits per Aron's 2026-05-24 direction (issues at [theory-cloud/AppTheory#593](https://github.com/theory-cloud/AppTheory/issues/593) + [theory-cloud/FaceTheory#248](https://github.com/theory-cloud/FaceTheory/issues/248)).
- **Acceptance**: `cdk synth --context stage=lab` and `--context stage=live` produce CloudFormation with exactly one OAC-protected default origin (SSR Lambda), S3 origin for `/_facetheory/data/*`, existing Lambda Function URL origins for the four API paths with auth-type NONE.
- **Validation**: `cd cdk && npm test && npm run synth`; manual review of synthesized template diff.
- **Conventional Commit subject**: `feat(cdk): adopt AppTheorySsrSite + JSON-sidecar S3 behavior under preserved CSP`
- **Gating note**: this commit cannot land until the upstream framework issues confirm the supported composition pattern for `AppTheorySsrSite` + mixed-auth co-origins. M0.13 (CDK composition test), M0.14 (CSP test depends on the new behaviors), M0.20 (SEC-8 CloudFront composition verifier) all depend on this and inherit the wait. M0 lab deploy (Phase M0.7) gates on this.

### M0.13. CDK test: distribution composition assertion (SEC-8 input)

- **Paths**: `cdk/test/cloudfront-composition.test.ts`
- **Surface**: cdk
- **Classification**: test-coverage / additive
- **Acceptance**: CDK test asserts the distribution has exactly one OAC default origin, S3 origin for `/_facetheory/data/*`, four expected bearer-auth Lambda behaviors unchanged. Snapshots template behaviors and asserts against the expected shape.
- **Conventional Commit subject**: `test(cdk): assert CloudFront composition shape`

### M0.14. CDK test: CSP integrity assertion (SEC-5 input)

- **Paths**: `cdk/test/csp-integrity.test.ts`
- **Surface**: cdk
- **Classification**: test-coverage
- **Acceptance**: CDK test parses `webCsp` and `safeAppCsp` from `lesser-host-stack.ts` and asserts: `default-src 'none'`; no `'unsafe-inline'`, `'unsafe-eval'`, `nonce-`, `sha256-` script directive, or non-`'self'` script origin; `safeAppCsp.frame-ancestors` matches the Safe-allowlist exception.
- **Conventional Commit subject**: `test(cdk): assert webCsp + safeAppCsp byte-string integrity`

### M0.15. Web build-time test: HTML inline absence (SEC-6 input)

- **Paths**: `web/scripts/verify-no-inline-html.mjs`, `web/package.json` (add script + chain into `npm run build`)
- **Surface**: web
- **Classification**: test-coverage
- **Acceptance**: post-build script walks `web/dist/**/*.html`; fails on any inline `<script>`/`<style>` body, inline event handler attribute, `data:` script src, or non-same-origin script/stylesheet URL.
- **Conventional Commit subject**: `test(web): assert built HTML contains no inline scripts/styles`

### M0.16. Web build-time test: OAC form transport integrity (SEC-7 input)

- **Paths**: `web/scripts/verify-oac-form-integrity.mjs`, `web/package.json`
- **Surface**: web
- **Classification**: test-coverage
- **Acceptance**: script asserts any `<form data-facetheory-oac-form>` has same-origin action + mutating method; asserts `startAwsOacFormTransport` import is present in built bundle.
- **Conventional Commit subject**: `test(web): assert OAC form transport integrity`

### M0.17. gov-infra: add SEC-5 verifier (web CSP byte-string integrity)

- **Paths**: `gov-infra/verifiers/sec/web-csp-integrity.sh`, `gov-infra/verifiers/gov-verify-rubric.sh` (invocation), `gov-infra/evidence/SEC-5-output.log` (initial empty)
- **Surface**: gov-infra
- **Classification**: governance — additive
- **Governance-rubric impact**: additive (tightening); does not weaken
- **Acceptance**: verifier runs in CI; produces evidence; full points on the current `webCsp`/`safeAppCsp` byte-strings; zero on any deviation.
- **Validation**: `bash gov-infra/verifiers/gov-verify-rubric.sh` PASS with new verifier counted.
- **Conventional Commit subject**: `feat(gov-infra): add SEC-5 web CSP byte-string integrity verifier`

### M0.18. gov-infra: add SEC-6 verifier (built HTML inline absence)

- **Paths**: `gov-infra/verifiers/sec/web-html-inline-absence.sh`, `gov-infra/verifiers/gov-verify-rubric.sh`, `gov-infra/evidence/SEC-6-output.log`
- **Acceptance**: verifier shells `npm run build` in `web/` and runs `verify-no-inline-html.mjs`; passes; emits evidence.
- **Conventional Commit subject**: `feat(gov-infra): add SEC-6 built HTML inline absence verifier`

### M0.19. gov-infra: add SEC-7 verifier (OAC form transport integrity)

- **Paths**: `gov-infra/verifiers/sec/oac-form-integrity.sh`, `gov-infra/verifiers/gov-verify-rubric.sh`, `gov-infra/evidence/SEC-7-output.log`
- **Acceptance**: verifier runs `verify-oac-form-integrity.mjs`; passes; emits evidence.
- **Conventional Commit subject**: `feat(gov-infra): add SEC-7 OAC form transport integrity verifier`

### M0.20. gov-infra: add SEC-8 verifier (CloudFront composition)

- **Paths**: `gov-infra/verifiers/sec/cloudfront-composition.sh`, `gov-infra/verifiers/gov-verify-rubric.sh`, `gov-infra/evidence/SEC-8-output.log`
- **Acceptance**: verifier shells `cd cdk && npm run synth`; parses synthesized template; asserts composition shape; passes; emits evidence.
- **Conventional Commit subject**: `feat(gov-infra): add SEC-8 CloudFront composition verifier`

### M0.21. gov-infra: add SEC-9 verifier (trust auth + attestation signing change-lock)

- **Paths**: `gov-infra/verifiers/sec/trust-auth-preservation.sh`, `gov-infra/verifiers/gov-verify-rubric.sh`, `gov-infra/evidence/SEC-9-output.log`
- **Surface**: gov-infra
- **Classification**: governance — additive change-lock
- **Trust-API / CSP / instance-auth impact**: tightens (locks `auth_instance.go`, `kms_service.go`, related files against semantic diff)
- **Acceptance**: verifier diffs against `origin/main` and fails on any semantic change to the listed files; passes on whitespace-only or no-diff.
- **Conventional Commit subject**: `feat(gov-infra): add SEC-9 trust auth + attestation signing change-lock verifier`

### M0.22. gov-infra: add SEC-10 verifier (release-verification change-lock)

- **Paths**: `gov-infra/verifiers/sec/release-verification-preservation.sh`, `gov-infra/verifiers/gov-verify-rubric.sh`, `gov-infra/evidence/SEC-10-output.log`
- **Consumer-release-verification impact**: tightens (locks `release_compatibility*.go`, `release_preflight*.go`, `scripts/managed-release-certification/main.go`, `scripts/managed-release-readiness/**/*.go`)
- **Acceptance**: verifier diffs against `origin/main` and fails on any semantic change to the listed files; passes on whitespace-only or no-diff.
- **Conventional Commit subject**: `feat(gov-infra): add SEC-10 release-verification change-lock verifier`

### M0.23. gov-infra: add CON-4 verifier (wire-MCP route ownership)

- **Paths**: `gov-infra/verifiers/con/wire-mcp-route-ownership.sh`, `gov-infra/verifiers/gov-verify-rubric.sh`, `gov-infra/evidence/CON-4-output.log`
- **Acceptance**: verifier inspects rendered provision-runner buildspec; asserts MCP-wiring step calls only `POST /mcp/{actor}` on tenant lesser API gateway; fails on any other MCP-wiring target.
- **Conventional Commit subject**: `feat(gov-infra): add CON-4 wire-mcp route ownership verifier`

### M0.24. gov-infra: bump pack.json to `2026.05.24-web.0`

- **Paths**: `gov-infra/pack.json`
- **Surface**: gov-infra
- **Classification**: governance — pack.json bump
- **Governance-rubric impact**: additive (registers 7 new verifiers); no loosening
- **Acceptance**: `pack.json.packVersion` updated to `2026.05.24-web.0`; `packDigest` recomputed; commit body lists the 7 new verifiers + their categorical claims + threat-model + control-matrix references; ends with "additive (tightening); no loosening; no exception; no skip".
- **Conventional Commit subject**: `chore(gov-infra): bump pack to 2026.05.24-web.0 (M0 web rework verifiers)`

### M0.25. gov-infra: threat-model additions

- **Paths**: `gov-infra/planning/lesser-host-threat-model.md`
- **Acceptance**: threats T-CSP-001, T-OAC-001, T-COMP-001, T-AUTH-DRIFT-001, T-SUPPLY-001, T-MCP-ROUTE-001 added per the governance walk's table.
- **Conventional Commit subject**: `docs(gov-infra): add M0 threat-model entries for FaceTheory adoption`

### M0.26. gov-infra: controls-matrix additions

- **Paths**: `gov-infra/planning/lesser-host-controls-matrix.md`
- **Acceptance**: controls C-CSP-FT, C-OAC-MUT, C-CDN-COMP, C-AUTH-LOCK, C-SUPPLY-LOCK, C-MCP-ROUTE added; each maps to its threat IDs + verifier IDs + evidence paths.
- **Conventional Commit subject**: `docs(gov-infra): add M0 controls-matrix entries`

### M0.27. ADR: FaceTheory v3.3.0 adoption + CSP shape change

- **Paths**: `docs/adr/0006-facetheory-adoption.md`
- **Acceptance**: ADR records the decision rationale (supersedes the 2026-05-16 deferral verdict), the CSP shape change (delivery-path only; contract preserved), Signal A resolution stub, and the four-walk reference.
- **Conventional Commit subject**: `docs(adr): ADR 0006 FaceTheory v3.3.0 adoption`

### M0.28. docs: supersede `docs/frontend-roadmap.md`

- **Paths**: `docs/frontend-roadmap.md`
- **Acceptance**: top of file adds a "Superseded by 2026-05-24 web/ UI rework planning artifacts" note with links to the four-walk docs + ADR; original content preserved below for historical context.
- **Conventional Commit subject**: `docs: mark frontend-roadmap.md as superseded by 2026-05-24 rework`

### M0.29. docs: refresh AGENTS.md web stack mention

- **Paths**: `AGENTS.md`
- **Acceptance**: stack mention updated from "Vite static SPA" to "FaceTheory v3.3.0 SSR + strict-CSP-via-JSON-sidecar"; CSP byte-string mention preserved unchanged.
- **Conventional Commit subject**: `docs(AGENTS): refresh web stack mention for FaceTheory adoption`

---

## M1 — Portal hero pages + customer-readable Stack card + cost-and-usage minimum

**Goal**: ship the Claude Design handoff's hero customer-facing surfaces (tabbed Instance Detail, Billing, Souls, Trust, Account) + the customer-readable Stack card. Adds SEC-11 verifier and bumps `pack.json` to `2026.05.24-web.1`.

### M1.1. Re-skin Portal Account

- **Paths**: `web/src/routes/portal/account/+page.svelte`
- **Classification**: UI-rework
- **Acceptance**: Account page renders on new shell + design tokens; existing endpoints unchanged.
- **Conventional Commit subject**: `feat(web): re-skin Portal Account on new shell`

### M1.2. Re-skin Portal Souls + Soul Detail

- **Paths**: `web/src/routes/portal/souls/+page.svelte`, `web/src/routes/portal/souls/[id]/+page.svelte`
- **Conventional Commit subject**: `feat(web): re-skin Portal Souls + Soul Detail on new shell`

### M1.3. Re-skin Portal Trust

- **Paths**: `web/src/routes/portal/trust/+page.svelte`
- **Conventional Commit subject**: `feat(web): re-skin Portal Trust on new shell`

### M1.4. Re-skin Portal Billing

- **Paths**: `web/src/routes/portal/billing/+page.svelte`
- **Conventional Commit subject**: `feat(web): re-skin Portal Billing on new shell`

### M1.5. Tabbed Instance Detail shell

- **Paths**: `web/src/routes/portal/instances/[slug]/+layout.svelte`, `web/src/routes/portal/instances/[slug]/+page.svelte`
- **Acceptance**: layout renders six tabs (Overview, Cost & usage, Config, Domains, Keys, Souls) with keyboard navigation, deep-linked tab state in URL.
- **Conventional Commit subject**: `feat(web): tabbed Instance Detail shell`

### M1.6. Overview tab with Stack card

- **Paths**: `web/src/routes/portal/instances/[slug]/overview/+page.svelte`, `web/src/lib/components/StackCard.svelte`
- **Surface**: web
- **Classification**: UI-rework; depends on M1.11
- **Acceptance**: Stack card renders Lesser/Body/MCP rows from the new stack-state endpoint (M1.11); when body is not installed, shows "Add agentic" CTA; when MCP is stale relative to body version, shows drift warning.
- **Conventional Commit subject**: `feat(web): Instance Detail Overview tab with Stack card`

### M1.7. Config tab

- **Paths**: `web/src/routes/portal/instances/[slug]/config/+page.svelte`
- **Acceptance**: existing `PUT /api/v1/portal/instances/{slug}/config` form re-rendered on new shell + design tokens.
- **Conventional Commit subject**: `feat(web): Instance Detail Config tab`

### M1.8. Domains tab

- **Paths**: `web/src/routes/portal/instances/[slug]/domains/+page.svelte`
- **Acceptance**: existing vanity-domain UX preserved (add, verify, lifecycle states); Route53-assist preserved.
- **Conventional Commit subject**: `feat(web): Instance Detail Domains tab`

### M1.9. Keys tab (preserve one-time reveal)

- **Paths**: `web/src/routes/portal/instances/[slug]/keys/+page.svelte`
- **Trust-API / CSP / instance-auth impact**: preserves; one-time-reveal contract enforced (no re-display after creation)
- **Acceptance**: create-key flow returns raw key once, shown with copy-to-clipboard + warnings; never re-displayed; revocation works; SEC-9 verifier continues to pass (no `auth_instance.go` change).
- **Conventional Commit subject**: `feat(web): Instance Detail Keys tab with one-time reveal preserved`

### M1.10. Souls tab

- **Paths**: `web/src/routes/portal/instances/[slug]/souls/+page.svelte`
- **Acceptance**: lists souls bound to this instance via existing endpoints; re-skin on new shell.
- **Conventional Commit subject**: `feat(web): Instance Detail Souls tab`

### M1.11. backend: GET `/api/v1/portal/instances/{slug}/stack` handler + ownership check + tests

- **Paths**: `internal/controlplane/handlers_portal_stack.go`, `internal/controlplane/handlers_portal_stack_internal_test.go`, `internal/controlplane/server.go` (route registration)
- **Surface**: internal/controlplane
- **Classification**: provisioning (additive read); multi-tenant-isolation (preserves)
- **Multi-tenant-isolation impact**: preserves (explicit ownership check before DB read)
- **Acceptance**: handler reads slug from path, calls existing `requirePortalInstanceOwnership` helper, queries latest successful UpdateJob + initial ProvisionJob, returns the JSON shape per the provisioning walk's Change 5.1; fails closed on ownership mismatch (403 or 404 per host convention); tests cover own-slug success, other-slug fail-closed, no-update-yet fallback to provision job, body-not-installed shape.
- **Validation**: `go test ./...` PASS including new tests; `go vet`; gov rubric passes (SEC-11 verifier lands in M1.14).
- **Conventional Commit subject**: `feat(portal): add GET /api/v1/portal/instances/{slug}/stack handler`

### M1.12. backend: surface cost-and-usage minimum data in API

- **Paths**: `internal/controlplane/handlers_portal_usage.go` (if not already present), `internal/controlplane/handlers_portal_budget.go`
- **Acceptance**: existing budget + usage endpoints return data sufficient to render the Cost & usage tab (current-month budget, used credits, remaining, cache hit rate). No new cost telemetry firehose yet (deferred to M3).
- **Conventional Commit subject**: `feat(portal): surface cost-and-usage minimum data for Instance Detail tab`

### M1.13. Cost & usage tab

- **Paths**: `web/src/routes/portal/instances/[slug]/cost/+page.svelte`
- **Acceptance**: tab renders current-month budget + used/remaining + cache hit rate via existing endpoints; "Real-time cost telemetry coming soon" empty state for per-Lambda/Dynamo/egress breakdown.
- **Conventional Commit subject**: `feat(web): Instance Detail Cost & usage tab`

### M1.14. gov-infra: add SEC-11 verifier (portal stack-state endpoint tenant scoping)

- **Paths**: `gov-infra/verifiers/sec/portal-stack-state-tenant-scoping.sh`, `gov-infra/verifiers/gov-verify-rubric.sh`, `gov-infra/evidence/SEC-11-output.log`
- **Acceptance**: verifier reads the new handler source; asserts ownership-check helper invocation precedes any DB read; passes; emits evidence.
- **Conventional Commit subject**: `feat(gov-infra): add SEC-11 portal stack-state tenant-scoping verifier`

### M1.15. gov-infra: threat-model addition T-TENANT-LEAK-001

- **Paths**: `gov-infra/planning/lesser-host-threat-model.md`
- **Conventional Commit subject**: `docs(gov-infra): add M1 threat-model entry for stack-state tenant leak`

### M1.16. gov-infra: controls-matrix addition C-TENANT-SCOPE

- **Paths**: `gov-infra/planning/lesser-host-controls-matrix.md`
- **Conventional Commit subject**: `docs(gov-infra): add M1 controls-matrix entry C-TENANT-SCOPE`

### M1.17. gov-infra: bump pack.json to `2026.05.24-web.1`

- **Paths**: `gov-infra/pack.json`
- **Conventional Commit subject**: `chore(gov-infra): bump pack to 2026.05.24-web.1 (M1 stack-state verifier)`

---

## M2 — Operator Console + dark chrome + Provisioning evolution

**Goal**: ship Operator Console with distinct dark chrome, two-channel release timeline + Stack Matrix, drift detection, "Wire all" remediation. Adds SEC-12 + MAI-5 verifiers; bumps `pack.json` to `2026.05.24-web.2`.

### M2.1. Operator Console shell with dark warm-charcoal chrome

- **Paths**: `web/src/routes/operator/+layout.svelte`, `web/src/lib/tokens/operator-chrome.css`
- **Acceptance**: operator-scoped layout uses dark warm-charcoal palette (per Claude Design handoff) with amber-on-coffee accents; visually unmistakable from Portal; respects shell primitives from M0.6.
- **Conventional Commit subject**: `feat(web): Operator Console shell with dark chrome`

### M2.2. Re-skin Operator Dashboard

- **Paths**: `web/src/routes/operator/+page.svelte`
- **Conventional Commit subject**: `feat(web): re-skin Operator Dashboard on dark chrome`

### M2.3. Operator Provisioning list with UI-derived kind labels

- **Paths**: `web/src/routes/operator/provisioning/+page.svelte`, `web/src/lib/components/JobKindBadge.svelte`
- **Surface**: web
- **Classification**: provisioning (UI-derivation only; backend unchanged)
- **Acceptance**: list shows ProvisionJob + UpdateJob rows; each row carries a colored Kind badge derived per the provisioning walk's table (provision / update-lesser / update-body / wire-mcp); top-of-page banner shows current MCP drift count from M2.9.
- **Conventional Commit subject**: `feat(web): Operator Provisioning list with kind labels`

### M2.4. Operator Provisioning job detail with timeline + live log

- **Paths**: `web/src/routes/operator/provisioning/[id]/+page.svelte`, `web/src/lib/components/ProvisioningTimeline.svelte`
- **Acceptance**: vertical timeline per job step (kind-specific step list per provisioning walk); live CodeBuild log streamed inside the active step node via the existing run URL.
- **Conventional Commit subject**: `feat(web): Operator Provisioning job detail with timeline`

### M2.5. `/operator/releases` page with two release-timeline columns

- **Paths**: `web/src/routes/operator/releases/+page.svelte`, `web/src/lib/components/ReleaseTimeline.svelte`
- **Acceptance**: two side-by-side columns (Lesser + Lesser-body) showing version history per channel with adoption bars sourced from M2.8 endpoint.
- **Conventional Commit subject**: `feat(web): /operator/releases page with two-channel timeline`

### M2.6. Stack Matrix component

- **Paths**: `web/src/lib/components/StackMatrix.svelte`
- **Acceptance**: table component shows each instance's lesser version, body version, MCP wired-against version; per-row drift indicator; per-row CTA ("Update" / "Wire MCP"); sortable; filterable by drift status.
- **Conventional Commit subject**: `feat(web): add Stack Matrix component`

### M2.7. Release-timeline component

- **Paths**: `web/src/lib/components/ReleaseTimeline.svelte`
- **Acceptance**: vertical timeline per channel with version cards: version string, released-at, latest badge, breaking badge, adoption bar.
- **Conventional Commit subject**: `feat(web): add release timeline component`

### M2.8. backend: GET `/api/v1/operators/releases` aggregation handler

- **Paths**: `internal/controlplane/handlers_operator_releases.go`, `..._test.go`, `internal/controlplane/server.go`
- **Surface**: internal/controlplane
- **Classification**: provisioning (read-only aggregation)
- **Multi-tenant-isolation impact**: operator-only; aggregates host-side state only (no tenant content)
- **Acceptance**: handler aggregates per-channel version + adoption count from `Instance` + `ProvisionJob` + `UpdateJob`; operator-JWT required; returns the shape per provisioning walk's Change 5.2; tests cover happy path + unauthorized.
- **Conventional Commit subject**: `feat(operators): add GET /api/v1/operators/releases aggregation handler`

### M2.9. backend: GET `/api/v1/operators/instances/drift` + drift computation

- **Paths**: `internal/controlplane/handlers_operator_drift.go`, `..._test.go`, `internal/controlplane/drift.go` (computation), `internal/controlplane/server.go`
- **Acceptance**: handler computes per-instance drift (lesser vs `MANAGED_LESSER_DEFAULT_VERSION`, body vs `MANAGED_LESSER_BODY_DEFAULT_VERSION`, MCP wired-against vs current body); returns the shape per Change 5.3; operator-JWT required; tests cover wire-stale, lesser-stale, body-stale, all-ok cases.
- **Conventional Commit subject**: `feat(operators): add fleet drift detection endpoint`

### M2.10. backend: POST `/api/v1/operators/instances/remediate-mcp-drift` + idempotency

- **Paths**: `internal/controlplane/handlers_operator_remediate_mcp.go`, `..._test.go`, `internal/controlplane/server.go`
- **Acceptance**: handler reads drift state; for each `mcp.drift == wire-stale` slug, queries `UpdateJob` GSI2 = UPDATE_ACTIVE for existing MCP-only job (skip if present); creates `UpdateJob{MCPOnly: true}` per affected slug; emits operator-audit event with slug list; returns list of created job IDs; tests cover idempotency, multi-slug fanout, operator-auth requirement.
- **Conventional Commit subject**: `feat(operators): add wire-all MCP-drift remediation endpoint`

### M2.11. UI: Stack Matrix wired to drift endpoint

- **Paths**: `web/src/routes/operator/releases/+page.svelte` (composition with Stack Matrix component from M2.6 + drift data from M2.9)
- **Conventional Commit subject**: `feat(web): wire Stack Matrix to fleet drift endpoint`

### M2.12. UI: "Wire all" CTA → remediation endpoint

- **Paths**: `web/src/routes/operator/provisioning/+page.svelte`, `web/src/routes/operator/releases/+page.svelte`
- **Acceptance**: top-of-page MCP-drift alert in both pages; "Wire all" button calls remediation endpoint; success-toast shows count of jobs created; idempotent re-clicks reflected.
- **Conventional Commit subject**: `feat(web): wire "Wire all" CTA to MCP-drift remediation`

### M2.13. Provisioning timeline component refinement

- **Paths**: `web/src/lib/components/ProvisioningTimeline.svelte`
- **Acceptance**: refined per the four job kinds; `update-body` jobs show the auto-queued "Wire MCP in Lesser" gated step at the end with `auto-queued` badge.
- **Conventional Commit subject**: `feat(web): refine provisioning timeline for four job kinds`

### M2.14. gov-infra: add SEC-12 verifier (operator drift endpoints auth gate)

- **Paths**: `gov-infra/verifiers/sec/operator-drift-auth-gate.sh`, `gov-infra/verifiers/gov-verify-rubric.sh`, `gov-infra/evidence/SEC-12-output.log`
- **Conventional Commit subject**: `feat(gov-infra): add SEC-12 operator drift endpoints auth-gate verifier`

### M2.15. gov-infra: add MAI-5 verifier (wire-all idempotency)

- **Paths**: `gov-infra/verifiers/mai/wire-all-idempotency.sh`, `gov-infra/verifiers/gov-verify-rubric.sh`, `gov-infra/evidence/MAI-5-output.log`
- **Conventional Commit subject**: `feat(gov-infra): add MAI-5 wire-all idempotency verifier`

### M2.16. gov-infra: threat-model additions T-OPER-AUTH-001, T-WIRE-FLOOD-001, T-DRIFT-CROSS-TENANT-001

- **Paths**: `gov-infra/planning/lesser-host-threat-model.md`
- **Conventional Commit subject**: `docs(gov-infra): add M2 threat-model entries`

### M2.17. gov-infra: controls-matrix additions C-OPER-AUTH, C-WIRE-IDEMP

- **Paths**: `gov-infra/planning/lesser-host-controls-matrix.md`
- **Conventional Commit subject**: `docs(gov-infra): add M2 controls-matrix entries`

### M2.18. gov-infra: bump pack.json to `2026.05.24-web.2`

- **Paths**: `gov-infra/pack.json`
- **Conventional Commit subject**: `chore(gov-infra): bump pack to 2026.05.24-web.2 (M2 operator-side verifiers)`

---

## M3 — Operator Console balance + public attestation re-skin + cost-telemetry firehose

**Goal**: ship the remaining Operator Console surfaces, re-skin the public attestation inspector, and (potentially) land the full cost-telemetry firehose. Items M3.7–M3.13 are deferrable per live-launch timing — they can ship post-launch as a separate scope. M3 verifier additions are TBD by a M3 audit-trust-and-safety re-run if cost telemetry firehose lands here; pack.json bumps to `2026.05.24-web.3` (or skips if no new verifiers).

### M3.1. Re-skin Operator Approvals (3 tabs)

- **Paths**: `web/src/routes/operator/approvals/+page.svelte`, `web/src/routes/operator/approvals/+layout.svelte`
- **Conventional Commit subject**: `feat(web): re-skin Operator Approvals on dark chrome`

### M3.2. Re-skin Operator Audit explorer

- **Paths**: `web/src/routes/operator/audit/+page.svelte`
- **Conventional Commit subject**: `feat(web): re-skin Operator Audit explorer on dark chrome`

### M3.3. Re-skin Operator Instances (read-only support view)

- **Paths**: `web/src/routes/operator/instances/+page.svelte`, `web/src/routes/operator/instances/[slug]/+page.svelte`
- **Conventional Commit subject**: `feat(web): re-skin Operator Instances on dark chrome`

### M3.4. Re-skin Operator Tip registry

- **Paths**: `web/src/routes/operator/tip-registry/+page.svelte`
- **Trust-API / CSP / instance-auth impact**: preserves; Safe-global frame-ancestors CSP exception unchanged
- **Acceptance**: Safe payload copy-to-clipboard preserved; tx-hash reconciliation flow re-skinned; no Safe contract change.
- **Conventional Commit subject**: `feat(web): re-skin Operator Tip registry on dark chrome`

### M3.5. Re-skin Operator Soul registry

- **Paths**: `web/src/routes/operator/soul-registry/+page.svelte`
- **Conventional Commit subject**: `feat(web): re-skin Operator Soul registry on dark chrome`

### M3.6. Re-skin public attestation inspector (presentational only)

- **Paths**: `web/src/routes/attestations/[id]/+page.svelte`
- **Surface**: web
- **Classification**: trust-API / CSP / instance-auth (preserves; presentational re-skin only)
- **Trust-API / CSP / instance-auth impact**: preserves; no data-layer change; client-rendered (ISR deferred per trust-and-safety walk Dimension 3); MarkdownRenderer mandatory-sanitization path preserved (closes 2026-05-16 markdown sanitizer signal locally)
- **Acceptance**: inspector fetches `/attestations/{id}`, decodes JWS, renders key id, copy verification instructions; new shell + design tokens; no internal-only fields exposed; no client-side cache beyond browser HTTP cache.
- **Conventional Commit subject**: `feat(web): re-skin public attestation inspector`

### M3.7. **[Deferrable]** backend: cost-telemetry-worker Lambda scaffolding

- **Paths**: `cmd/cost-telemetry-worker/main.go`, `internal/costtelemetry/server.go`, `cdk/lib/cost-telemetry-worker-construct.ts`
- **Surface**: cmd / internal/costtelemetry / cdk
- **Classification**: operational-reliability; multi-tenant-isolation (per-tenant cost data only; no cross-tenant aggregation in customer plane)
- **Acceptance**: new Lambda worker scaffolded; AppTheory scheduled-EventBridge entrypoint; no business logic yet (lands in M3.8–M3.10).
- **Conventional Commit subject**: `feat(cmd): scaffold cost-telemetry-worker Lambda`

### M3.8. **[Deferrable]** backend: CloudWatch metric collection for per-instance cost

- **Paths**: `internal/costtelemetry/cloudwatch.go`, `internal/costtelemetry/cloudwatch_test.go`
- **Acceptance**: worker reads CloudWatch metrics scoped per tenant account; produces per-instance per-service cost attribution; no PII; no tenant content.
- **Conventional Commit subject**: `feat(costtelemetry): CloudWatch per-instance metric collection`

### M3.9. **[Deferrable]** backend: Cost Explorer integration

- **Paths**: `internal/costtelemetry/cost_explorer.go`, `internal/costtelemetry/cost_explorer_test.go`
- **Acceptance**: worker queries Cost Explorer for billing-grain cost per tenant account; reconciles with CloudWatch metric data; produces aggregate per-instance per-day cost.
- **Conventional Commit subject**: `feat(costtelemetry): Cost Explorer integration`

### M3.10. **[Deferrable]** backend: DynamoDB cache for cost data

- **Paths**: `internal/store/models/cost_telemetry.go`, `internal/costtelemetry/cache.go`, `..._test.go`
- **Acceptance**: new TableTheory model `CostTelemetry` with per-slug per-day records; worker writes; idempotent on re-run; TTL set per retention policy.
- **Conventional Commit subject**: `feat(costtelemetry): DynamoDB cache model`

### M3.11. **[Deferrable]** backend: GET `/api/v1/portal/instances/{slug}/cost` endpoint

- **Paths**: `internal/controlplane/handlers_portal_cost.go`, `..._test.go`, `internal/controlplane/server.go`
- **Multi-tenant-isolation impact**: preserves (ownership check before read)
- **Acceptance**: handler returns per-instance per-day cost rollup; ownership check enforced; returns past-30-day window by default; tests cover own-slug success, other-slug fail-closed.
- **Conventional Commit subject**: `feat(portal): add per-instance cost telemetry endpoint`

### M3.12. **[Deferrable]** UI: wire Cost & usage tab to real-time data

- **Paths**: `web/src/routes/portal/instances/[slug]/cost/+page.svelte`
- **Acceptance**: tab replaces "coming soon" empty states with real-time per-Lambda/Dynamo/egress breakdown.
- **Conventional Commit subject**: `feat(web): wire Cost & usage tab to real-time telemetry`

### M3.13. **[Deferrable]** gov-infra: cost-telemetry redaction verifier (TBD by audit re-run)

- **Paths**: `gov-infra/verifiers/sec/cost-telemetry-redaction.sh`, `gov-infra/verifiers/gov-verify-rubric.sh`, `gov-infra/evidence/SEC-13-output.log` (or whatever code allocates)
- **Acceptance**: verifier asserts cost-telemetry responses contain no PII, no cross-tenant fields, no tenant content; per the M3 audit-trust-and-safety re-run.
- **Conventional Commit subject**: `feat(gov-infra): add SEC-13 cost-telemetry redaction verifier`

### M3.14. gov-infra: bump pack.json to `2026.05.24-web.3` (if M3.13 lands)

- **Paths**: `gov-infra/pack.json`
- **Acceptance**: bump only if M3.13 verifier lands; otherwise skip this commit and M3 closes without a pack.json bump.
- **Conventional Commit subject**: `chore(gov-infra): bump pack to 2026.05.24-web.3 (M3 cost-telemetry verifier)`

---

## Summary

- **Total commits**: M0 = 29; M1 = 17; M2 = 18; M3 = up to 14 (6 always; 7 deferrable; 1 conditional pack bump). Grand total = 78 if everything lands, 71 if M3 cost-telemetry firehose defers.
- **Gated commits**: M0.6–M0.10 (Signals A + D) — five commits that need steward coordination before landing.
- **Verifier additions**: 10 (SEC-5/6/7/8/9/10/11/12 + CON-4 + MAI-5). Possibly +1 (SEC-13) for cost-telemetry redaction.
- **`pack.json` bumps**: 3 mandatory (M0/M1/M2) + 1 conditional (M3).
- **Change-lock verifiers** (SEC-9 and SEC-10): not new code; enforce non-modification of trust-auth, attestation-signing, and release-verification paths across all milestone PRs.
- **Cross-walk discipline preserved**: every commit's classification matches the four-walk inputs; no commit weakens governance, CSP, instance-auth, release verification, or multi-tenant boundary.

## Self-check

- [x] Every item is in-mission (UI rework + framework adoption + provisioning evolution + observability addition + governance evolution per scope-need Gate 1)
- [x] No item weakens governance rubric (all 10 verifier additions are tightening; no exceptions; no skips)
- [x] No item traverses multi-tenant isolation (Stack/drift state per-tenant scoped; operator aggregations operator-JWT-gated; cost telemetry per-tenant)
- [x] On-chain items: none in this rework (Tip registry UI re-skins existing Safe-ready endpoints unchanged)
- [x] Consumer-release-verification items flagged elevated (M0.22 SEC-10 change-lock; verifier locks existing path)
- [x] No item loosens trust-API instance-auth (M0.21 SEC-9 change-lock enforces this absolutely)
- [x] No item loosens CSP (SEC-5 + SEC-6 + SEC-7 verifiers enforce; M0.12 CDK change adds new behavior under unchanged CSP)
- [x] Framework awkwardness routed to coordinate-framework-feedback (Signals A/B/C/D), not patched locally
- [x] Bug-fix items: none (this is forward-looking rework, not bug-fix)
- [x] Solidity commits: none (no contract changes)
- [x] CDK commits isolated from Go code (M0.12, M0.13, M0.14 isolated)
- [x] `gov-infra/pack.json` bumps in isolated commits (M0.24, M1.17, M2.18, M3.14)
- [x] `web/` changes isolated from backend changes
- [x] Documentation rides with behavior (M0.27 ADR with FaceTheory adoption; M1.15/M1.16 threat-model + controls with SEC-11; M2.16/M2.17 with SEC-12 + MAI-5)
- [x] Every item has validation contract (cross-cutting section at top)
- [x] No item requires a future item to compile (the gated commits M0.6-M0.10 sit as draft until Signal A/D resolves; they don't block other M0 commits)
- [x] No hardcoded secrets, wallet keys, raw instance keys
- [x] No raw key / seed phrase / full signed-transaction / PII logging
- [x] No deletion of Lambda versions, DynamoDB tables, stateful S3, Route53 zones, SSM, Secrets Manager entries
- [x] No AGPL-incompatible dependencies or proprietary blobs

## Handoff

Invoke `plan-roadmap` next to sequence this flat list into phases with dependencies, risks, and a rollout plan across stages (`lab → live`), with a canary plan for live and the live-launch readiness gating. The live-launch window (Open Question #1 from the scoped-need) re-surfaces in plan-roadmap; if known, it sequences M0-M3 cadence accordingly.
