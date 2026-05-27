# M0 — P0 correctness bundle

**Branch (arch-specified).** `aron/project42-m0-portal-correctness`
**PR title (arch-specified).** `fix(portal): M0 correctness and immediate chrome hygiene`
**Project / parent.** equaltoai/projects/42 · parent issue `equaltoai/lesser-host#525`
**Source brief.** `arch.equaltoai@theorymcp.ai` delivery
`delivery-d08bce036c8f3243` (2026-05-27 17:48 UTC), corrected for
M0.4 in `delivery-3ef80b6c7ccc6fea` (2026-05-27 18:32 UTC).
**Concern.** Six P0 audit rows + two chrome-hygiene rows, total eight
tasks. This is not a redesign milestone. Seven tasks are UI/chrome
correctness fixes; M0.4 is the corrected backend data-source fix that
wires the existing portal cost route to managed Lesser instance metrics.

Per the bundle rules, this milestone exists separately from design work
so the legacy portal is stable before foundation redesign begins.

## Scope — sub-issues (≤ 8 tasks)

Arch-created sub-issues under parent `#525`:

| # | Sub-issue | Task |
|---|---|---|
| 1 | `#526` M0.1 | Route Trust and Account inside portal chrome |
| 2 | `#527` M0.2 | Correct Billing page heading |
| 3 | `#528` M0.3 | Hide provisioning form for live instances |
| 4 | `#529` M0.4 | Wire portal cost/usage to managed Lesser instance metrics |
| 5 | `#530` M0.5 | Fix Instance Souls bound-agent fetch |
| 6 | `#531` M0.6 | Remove literal slug template leak |
| 7 | `#532` M0.7 | Replace raw sidebar wallet identity preview or prove deferred |
| 8 | `#533` M0.8 | Resolve or defer duplicate Instance Overview Refresh control |

## Corrected outline

1. **Route fix — Trust + Account inside portal namespace.** Move
   `/trust` and `/account` under `/portal/*` for the portal experience;
   preserve legacy links and public attestation behavior.
2. **Billing h1 fix.** Replace the page `<h1>` "Portal Dashboard" with
   "Billing" on `/portal/billing`; avoid relabeling other portal pages.
3. **Hide provisioning form on healthy instances.** Gate the "Not
   started" warning/banner/form behind genuinely unprovisioned or retryable
   provisioning states.
4. **Managed Lesser metrics access.** The existing host portal route
   `GET /api/v1/portal/instances/{slug}/cost` must enforce
   `requireInstanceAccess`, resolve the instance API key server-side from
   `LesserHostInstanceKeySecretARN`, call the managed Lesser instance with
   bearer instance-key auth, and map real Lesser daily usage/cost rows into
   `PortalCostResponse`. This supersedes the original graceful-degradation
   wording; copy-only masking or host-local CloudWatch/Cost Explorer data
   does not close `#529`.
5. **Souls tab fetch fix.** Match bound agents against the canonical managed
   Lesser domain shape so instances with bound souls do not show a false
   empty state.
6. **Slug template literal fix.** Remove the visible literal `{slug}` token
   from customer copy.
7. **Sidebar identity fix (preview only).** Replace the raw wallet hash in
   the sidebar footer with bounded safe identity preview copy; full identity
   chip design remains M2.
8. **Duplicate Refresh control.** Distinguish or remove duplicate Instance
   Overview refresh controls if safe; otherwise document the M6 follow-up.

## Out of scope

- No layout redesign.
- No new metric cards / sparklines / gauges.
- No right-rail panels.
- No new host customer-facing route beyond the existing portal cost route.
- No host-local cost-telemetry producer work for M0.4; the data source is the
  managed Lesser instance metrics contract.
- No `gov-infra/pack.json` bump (no new verifier).
- No primitives.

## Acceptance criteria

- Each P0 row in `../audit-mapping.md` flagged "M0" is reproducible before
  the PR and not reproducible after.
- `#529` proof shows the portal handler reads real managed Lesser metrics via
  instance-key bearer auth; tests use a fake managed Lesser HTTP server and
  prove authorization, non-empty mapping, empty-data behavior, upstream-error
  handling, and no raw-key/browser leakage.
- `web lint`, `web typecheck`, `web test` green.
- `go test ./...` green because M0.4 touches the control-plane backend.
- `cd cdk && npm test` green because M0.4 grants control-plane-api the
  managed instance assume-role path.
- `gov-infra/verifiers/gov-verify-rubric.sh` green at existing pass count.
- Manual smoke after merge/deploy: navigate to `/portal/trust`,
  `/portal/account`, `/portal/billing`, `/portal/instances/{slug}` (Souls tab
  and Cost tab) on a lab-deployed branch; verify each P0 is fixed and the Cost
  tab returns managed Lesser data once the Lesser contract PR is deployed.

## Risks

- The host PR depends on the Lesser-side route contract from
  `equaltoai/lesser#1097`: `GET /api/v1/instance/metrics/daily` with bearer
  instance-key auth. If that contract changes, the host client/tests must be
  updated before M0 can close.
- Trust + Account route changes may regress sidebar nav active-state matching;
  verify the active highlight still tracks correctly on both old and new paths
  during the transition.

## Evidence

- M0 is correctness-only, so no design-fidelity artifact.
- Capture before/after screenshots or written lab evidence in the PR
  description for each P0 row.
- No new gov-infra evidence artifact required.

## Post-implementation findings (2026-05-27)

PR #534 now carries all eight M0 fixes. Commits b09d5e9, cdf04a2,
829a6ea, 8201061, 1dd67da, 4e7d5d2, and 44be898 cover the seven
UI/chrome correctness tasks. The M0.4 follow-up replaces the host-local
`ListCostTelemetryByInstance` source with a managed Lesser metrics client:

1. `requireInstanceAccess` runs before any secret read or upstream call.
2. The upstream origin is derived from `HostedBaseDomain` through the managed
   stage-domain helper, not from `LesserHostBaseURL`.
3. The raw instance key is resolved server-side from
   `LesserHostInstanceKeySecretARN` using the commworker/provisionworker
   cross-account assume-role + Secrets Manager pattern.
4. Host calls `GET /api/v1/instance/metrics/daily?from=...&to=...` on the
   managed Lesser instance with `Authorization: Bearer <instance-key>`.
5. Daily Lesser rows are mapped into the existing `PortalCostResponse` shape;
   empty data remains empty, and upstream failures are surfaced honestly
   without echoing keys or upstream bodies to the browser.

### Standing instruction captured during M0.4 investigation

Aron's directive, recorded verbatim for the steward stack:

> Do not patch over gaps in our data with "graceful degradation" —
> we deal with the missing pipeline honestly or we don't ship the
> masking.

That remains the rule for future cost/usage work. PR #522's host-local
telemetry scaffolding is not the source of truth for managed instance cost
and usage in the portal.

### M0 closure path

M0 closes when PR #534 and the Lesser contract it depends on are merged and
lab smoke confirms the portal cost route returns real managed Lesser metrics.
Until then, do not advance M1 as if PR #522 already solved the instance data
source.
