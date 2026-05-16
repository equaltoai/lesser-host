# Investigation: Theory Cloud Framework Adoption — 2026-05-16

## Reported symptom

> AppTheory, TableTheory and FaceTheory have all been updated proceed to update our pins and also address any outstanding dependabot alerts
>
> are there any updates in the theory-cloud frameworks we should consider implementing?
>
> investigate-issue deeply to identify how we can best leverage these frameworks, fully enumerate-changes plan-roadmap and create-github-project, then email arch@lessersoul.ai indicating scope of upcoming work and proceed to iterate over implement-milestone updating arch and awaiting review results and approval before moving to next milestone, I authorize all work requested by arch

## Dimensions

- **Surface**: framework consumption, dependency maintenance, operational reliability, trust/auth audit hardening, worker reliability, web/CSP hardening.
- **Lambda implicated**: potentially all Lambda entrypoints because AppTheory/TableTheory are shared runtime/storage dependencies:
  `control-plane-api`, `trust-api`, `email-ingress`, `provision-worker`, `render-worker`, `ai-worker`, `comm-worker`, `soul-reputation-worker`.
- **Tenant context**: none. No tenant content or tenant-side account access is required for this investigation.
- **On-chain context**: none directly. Some candidate work touches soul-registry/on-chain-adjacent handlers only through auth/audit request-context plumbing; no Solidity or Safe-ready payload work is proposed.
- **Release context**:
  - AppTheory current/latest after the pin update: `v1.6.0`, published 2026-05-15, release: <https://github.com/theory-cloud/AppTheory/releases/tag/v1.6.0>.
  - TableTheory current/latest after the pin update: `v1.8.3`, published 2026-05-15, release: <https://github.com/theory-cloud/TableTheory/releases/tag/v1.8.3>.
  - Greater Components current/latest after the pin update: `greater-v0.8.11`, published 2026-05-02, release: <https://github.com/equaltoai/greater-components/releases/tag/greater-v0.8.11>.
- **Gov-infra context**: no gov-infra rubric weakening proposed. Any new evidence expectations would be additive and require explicit governance review.
- **Recent deploys**: none performed by this investigation. At investigation start, the working branch was `codex/update-framework-dependabot-pins` at `1cf2208d` with dependency/framework updates in progress.

## Specialist elevation check

- **Framework awkwardness**: elevate to `coordinate-framework-feedback`.
- **Trust API / CSP / instance-auth**: candidate source-provenance and Greater markdown/CSP work must use `audit-trust-and-safety` before implementation.
- **Provisioning / managed-update**: any release-state refactor around provisioning or managed-update state must use `provision-managed-instance` before implementation.
- **Governance rubric**: no current rubric change. If framework adoption requires a verifier/evidence change, use `maintain-governance-rubric`.
- **Advisor brief**: Arch review responses are advisor-originated. They must be surfaced for review; the blanket phrase "I authorize all work requested by arch" is recorded as intent but does not bypass the advisor-review discipline for future inbound briefs.

## What is definitely true

- `go list -m -u` reports AppTheory `v1.6.0` and TableTheory `v1.8.3` as current/latest in the working tree.
- `gh release view` confirms the latest release tags and release notes listed above.
- `gh api repos/equaltoai/lesser-host/dependabot/alerts` currently lists open alerts #43-#46 and #49-#51 on `main` for `fast-uri` and `svelte`. The working branch already updates the relevant lockfiles to patched versions (`fast-uri` `3.1.2`, `svelte` `5.55.7`) but those alerts remain open until GitHub observes a merged fix.
- Host uses AppTheory HTTP/SQS/EventBridge runtime patterns, with EventBridge handlers in:
  - `internal/provisionworker/server.go`
  - `internal/renderworker/server.go`
  - `internal/soulreputationworker/server.go`
- `cmd/email-ingress/main.go` still manually adapts SES events and includes a comment that AppTheory does not natively dispatch SES events.
- Host currently has no `SourceProvenance`, `SourceIP`, or TableTheory `WithLambdaTimeout` usage in `cmd/` or `internal/`.
- Host has a canonical `internal/httpx.ParseJSON` helper and many handler call sites. AppTheory v1.6.0 includes typed request binding (`BindHandler`, `BindRequest`, `BindConfig`) that can be evaluated incrementally rather than replacing all parsing at once.
- Host's `internal/store.DB` interface currently hides TableTheory's Lambda-timeout helpers even though the concrete `tabletheory.LambdaDB` supports them.
- Greater v0.8.11 is consumed in `web/src/lib/greater/`. Host retains mandatory markdown sanitization around `{@html}` and has a local safety test asserting no optional sanitizer bypass is exposed. This is stricter than the upstream component shape and is correct for host's strict CSP/trust posture.
- KnowledgeTheory query for the `greater-components` KB timed out during both framework scans; local release notes and vendored source were used instead.

## Fix-locus verdict

- **Fix here in host**:
  - Complete dependency/security baseline and Dependabot fix PR.
  - Adopt TableTheory Lambda timeout propagation where it can reduce hard-timeout risk.
  - Adopt AppTheory source-provenance helpers for audit/rate-limit/auth-sensitive surfaces.
  - Pilot AppTheory scheduled/EventBridge workload normalization on existing scheduled workers.
  - Pilot AppTheory typed request binding on a narrow low-risk handler family.
- **Report upstream / coordinate through Aron**:
  - Greater/FaceTheory: upstream `MarkdownRenderer` should not make sanitizer bypass easy for strict-CSP consumers; host's local mandatory sanitization should remain until upstream has a fail-closed variant.
  - Greater/FaceTheory: LightningCSS warnings around `:global(...)` selectors in plain CSS should be resolved upstream.
  - AppTheory: native SES event source dispatch/observability support would remove host's manual `email-ingress` adapter and should be shaped as framework feedback, not locally patched.
- **Do not implement now**:
  - AppTheory MCP task runtime in host. `body` is the MCP runtime; host should not absorb body scope without explicit product design.
  - AppTheory SSR/ISR or FaceTheory SSR hosting. Host's static SPA behind single-origin CloudFront with strict CSP is the right current posture.
  - AppSync resolver migration. Host's public surfaces are Lambda Function URLs behind CloudFront; no clear AppSync need.
  - TableTheory unreleased TTL archival construct. Track it, but do not adopt unreleased features.
  - Large CDK construct replacement. Existing CDK has host-specific CloudFront routing, strict CSP, and retained state resources; broad construct rewrites would increase blast radius.

## Hypotheses ranked

1. **The highest-value local adoption is TableTheory Lambda timeout propagation.**
   - **Evidence for**: all host Lambdas use TableTheory; no `WithLambdaTimeout` usage exists; TableTheory v1.8.x specifically hardened Lambda timeout guards; workers and APIs do DynamoDB work near external calls.
   - **Evidence against**: host's handlers already pass `context.Context` to store calls and some workers inspect AppTheory `RemainingMS` directly.
   - **Verification step**: prototype a store-level timeout wrapper that preserves `TransactWrite`, then add tests proving deadline-aware DB instances are selected without changing store semantics.

2. **AppTheory source provenance should replace ad hoc source-IP assumptions in audit/rate-limit/auth paths.**
   - **Evidence for**: AppTheory documents `ctx.SourceProvenance()` / `ctx.SourceIP()` and warns not to trust forwarded headers; host has auth, setup, portal, trust, Stripe/Telnyx webhook, and audit surfaces.
   - **Evidence against**: CloudFront/Lambda Function URL source IP behavior may currently be sufficient for logging; rate limiting may already use wallet/session/slug identifiers rather than IP.
   - **Verification step**: inventory all IP/source logging and rate-limit identifier paths; add tests using AppTheory testkit source-IP events.

3. **EventBridge scheduled workload helpers can improve worker idempotency/audit without changing business behavior.**
   - **Evidence for**: provision update sweep, render retention sweep, and soul reputation recompute are scheduled EventBridge workloads; AppTheory now exposes normalized envelopes and scheduled run IDs.
   - **Evidence against**: current worker tests already use AppTheory EventBridge testkit and handlers are simple.
   - **Verification step**: implement a pilot on `render-worker` retention sweep or `soul-reputation-worker` recompute and verify logs/outputs retain no raw event leakage.

4. **Typed request binding is useful but should be piloted, not broadly migrated.**
   - **Evidence for**: AppTheory v1.6.0 exposes `BindRequest` / `BindHandler`; host has many `httpx.ParseJSON` call sites.
   - **Evidence against**: `internal/httpx.ParseJSON` is a gov-infra canonical helper (MAI-3) and broad replacement risks API error-shape drift.
   - **Verification step**: use one low-risk handler family; compare status codes/error bodies with existing tests before expanding.

5. **TableTheory release-state helpers may map to managed-update/provisioning ledgers, but only after scoped design.**
   - **Evidence for**: host has `ProvisionJob`, `UpdateJob`, release artifact verification, operation ledgers, and protected transitions where immutable event history matters.
   - **Evidence against**: these flows are supply-chain and managed-instance critical; current state machines are bespoke and heavily tested.
   - **Verification step**: produce a feasibility note against `internal/provisionworker/update_jobs.go`, `internal/store/models/provision_job.go`, and `internal/store/models/update_job.go` before code changes.

6. **Greater/FaceTheory adoption should stay at the vendored component level for now.**
   - **Evidence for**: host's strict CSP/static SPA model does not need FaceTheory SSR/ISR; local markdown sanitizer hardening is required.
   - **Evidence against**: if host later needs SSR for SEO/customer portal performance, AppTheorySsrSite may become relevant.
   - **Verification step**: preserve current static SPA and record upstream feedback instead of altering CSP or routing.

## Scoped need

Adopt the current AppTheory/TableTheory/Greater framework releases in host without broad architectural churn, then leverage only the framework features that strengthen host's managed-platform posture:

- reliability: Lambda-timeout-aware DB access and safer scheduled worker envelopes;
- trust/auth rigor: source provenance for audit/rate-limit/auth surfaces;
- maintainability: typed binding only where it does not disturb API contracts;
- governance: explicit evidence and no silent verifier/CSP/auth weakening;
- framework reciprocity: report concrete AppTheory/Greater gaps instead of local framework patches.

## Verification step

Create a tracked roadmap/project, send Arch a review brief, then implement milestones one at a time. Milestone implementation must wait for Arch review/approval as requested and must still respect host's advisor-brief discipline.

## Proposed next skill

- `enumerate-changes`
- `plan-roadmap`
- `create-github-project`
- `implement-milestone` only after project and Arch review gates are established.
