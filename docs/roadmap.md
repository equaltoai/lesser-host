# lesser.host roadmap (AppTheory)

This roadmap describes how to build the `lesser-host` repository to operate `lesser.host` as a **hosting + trust/safety
control plane** using **AppTheory** (`github.com/theory-cloud/apptheory`), aligned with the tip/host governance and
trust/safety planning.

## References (source of truth)

- Bootstrap plan: `app-theory/init.md`
- Deployment contract (consumed by `theory app up/down`): `app-theory/app.json`
- Frontend roadmap (portal + operator console): `docs/frontend-roadmap.md`
- Prototype instance frontend roadmap: `docs/instance-frontend-roadmap.md`
- Simulacrum client runbook: `docs/runbook-simulacrum-client.md`
- AI services subroadmap: `docs/ai-services-roadmap.md`
- Lesser release contract (consumed for managed instance deploys): `docs/lesser-release-contract.md`
- Managed provisioning notes: `docs/managed-instance-provisioning.md`
- Moderation provider notes: `docs/moderation-provider.md`
- Tip registry notes: `docs/tip-registry.md`
- Pinned frameworks (from `app-theory/app.json`):
  - AppTheory: `github.com/theory-cloud/apptheory@v0.8.0`
  - TableTheory: `github.com/theory-cloud/tabletheory@v1.3.0`

## Scope (v1)

`lesser.host` provides:

1. **Hosting control plane**
   - Provision and manage hosted Lesser instances.
   - Hosted instances default to `*.greater.website` (slug-based), with optional vanity domains.

2. **Host registration governance (tips)**
   - Gate and execute on-chain host registry operations (TipSplitter `registerHost/updateHost/setHostActive`, token allowlist management).
   - Register both `slug.greater.website` and any approved vanity domain to the same host wallet/fee.
   - Baseline supported tokens: ETH, USDC, USDT, EURC, XAUt (via allowlist).
   - Host fee is token-agnostic and capped at `MAX_HOST_FEE_BPS = 500`.
   - Assumes non-upgradeable contracts, “events-only” tip history, and no on-chain refunds.

3. **Trust & safety services (opt-in per instance)**
   - Link previews default to being fetched/rendered by `lesser.host` (privacy + SSRF-hardening + shared cache).
   - Public, cacheable **attestations** (link safety; optional claim checks) signed by `lesser.host`, bound to `(actorUri, objectUri, contentHash)` so quote posts do not inherit.
   - Private, admin-only scanning (moderation signals) as a hosted AI provider for Lesser’s existing moderation hooks.

## Architectural baseline (AppTheory)

**Language:** Go for services (aligns with Lesser). CDK lives in `cdk/` and is deployed via `npx cdk` per
`app-theory/app.json`.

**AppTheory usage:**
- HTTP APIs implemented with `github.com/theory-cloud/apptheory/runtime`.
- Deterministic unit tests with `github.com/theory-cloud/apptheory/testkit`.
- Event-driven workers via AppTheory + AWS-native triggers (SQS, EventBridge).
- Infra via AppTheory CDK patterns (REST API router, SQS consumer, media CDN / proxy).

**High-level components:**
- `control-plane-api` (AppTheory HTTP): instance registry, billing/budgets, config, admin UX API.
- `trust-api` (AppTheory HTTP): request preview/scan/attestation; fetch public attestations.
- `workers/*` (SQS consumers): fetch/render, safety scanning, claim verification, moderation scans, retention sweeps.
- `artifact-store` (S3 + CloudFront): preview images and bounded evidence packs.
- `state-store` (DynamoDB): instances, domains, budgets, jobs, attestations, audit log.

## Milestones

### M0 — Repo + runtime bootstrap (AppTheory + CDK skeleton)

Deliverables:
- Go module + AppTheory app skeleton for `control-plane-api` and `trust-api`.
- CDK app skeleton using `apptheorycdk` (one dev stack) that deploys:
  - an HTTP entrypoint (APIGW v2 or Lambda Function URL)
  - a DynamoDB table for state
  - an S3 bucket for artifacts (no public access)
  - basic SQS queues for async work
- Baseline observability: structured logs, request IDs, metrics stubs.
- CI basics: `go test ./...`, `golangci-lint`, `cdk synth` (or equivalent).

Acceptance criteria:
- `go test ./...` passes and includes at least one AppTheory testkit test for each API.
- `cdk synth` succeeds with no context prompts.
- `/healthz` responds `200` for both APIs in local invocation (testkit) and in a deployed dev stack.

---

### M1 — Instance registry + authentication + budgets (core control plane)

Deliverables:
- Data model:
  - instances (slug, owner, status)
  - domains (primary `slug.greater.website`, vanity domains, verification status)
  - per-instance service configuration (what scans/previews are enabled)
  - budgets (monthly included credits, overage policy, usage ledger)
- Auth:
  - instance authentication (registered instance key) for machine-to-machine calls
  - operator/admin auth for `lesser.host` portal (wallet auth, RBAC) *or* stubbed interface if portal is deferred
- Billing primitives:
  - metered usage, monthly included budget, overage charging hooks (provider-agnostic)
  - cache-hit accounting rules (only charge on cache miss)
  - author discounts: discounted pricing only for “author at initial publish” flows from registered instances

Acceptance criteria:
- An instance can be created, assigned a slug, and receives credentials for API calls.
- Budgets enforce “metered + included”:
  - calls decrement included credits
  - over-budget calls return a structured “budget exceeded” response (without blocking publish flows)
- All write endpoints are authenticated and produce audit log entries.

---

### M2 — Link preview service (default to lesser.host)

Deliverables:
- URL normalization (host extraction, IDNA/UTS#46 to ASCII, redirect-safe canonicalization).
- Hardened fetcher:
  - SSRF protections (block private IPs, metadata ranges, internal hostnames)
  - strict time/byte/redirect limits
  - no cookies/auth headers
- Preview artifacts:
  - resolved URL + redirect chain
  - title/description/OG data when available
  - preview image proxy (store/proxy via `lesser.host` CDN)
- Instance-level toggle: “use lesser.host previews for posts with links”.
- Caching:
  - cache key includes normalized URL + `policyVersion`
  - TTL appropriate for previews

Acceptance criteria:
- A Lesser instance can request a preview for a URL and get a deterministic preview response.
- Preview images are served from `lesser.host` (not the origin), and no instance code needs to fetch from the origin.
- SSRF regression tests exist (private IP URL, redirect to private IP, etc. are blocked).

---

### M3 — Link safety scanning (basic) + budgets + on-publish integration contract

Deliverables:
- `link_safety_basic` module (cheap deterministic checks, no render):
  - suspicious scheme/ports, shorteners, redirect patterns, punycode/homograph flags
  - invalid/broken link detection where possible without full fetch
- API contract for “publish triggered jobs”:
  - accepts `(actorUri, objectUri, contentHash)` + extracted links + module options
  - returns job id + budget decision + cached/known results when available
- Budget gating:
  - always return a “not checked due to budget” state rather than failing publishes

Acceptance criteria:
- For a post with links, the instance can request `link_safety_basic` and receive per-link signals and a post-level summary.
- Over-budget behavior is stable and does not block the caller.
- Results are cacheable and dedupe across instances when inputs match.

---

### M4 — Render pipeline + enhanced preview/safety (budgeted)

Deliverables:
- `link_preview_render` and `link_safety_render` capabilities:
  - headless render (screenshot thumbnail + bounded text snapshot)
  - summary generation option (separate module, budgeted)
  - reuse render artifacts across preview + safety
- Auto-escalation policy (instance-configurable):
  - render only for “suspicious” links, or always for any link
- Retention:
  - benign render artifacts retained 30 days
  - flagged/suspicious evidence packs retained 180 days

Acceptance criteria:
- Render jobs respect time/byte limits and cannot access internal networks.
- Retention sweeper proves deletion happens on schedule (testable via integration test in dev stack).
- Render artifacts can be used to produce a richer preview card and safety output.

---

### M5 — Public, cacheable attestations (signing + distribution)

Deliverables:
- Attestation schema (JSON) and signature:
  - binds `(actorUri, objectUri, contentHash, module, policyVersion, modelSet, createdAt, expiresAt)`
  - includes evidence references/hashes where applicable
- Key management:
  - signing keys stored securely (KMS or equivalent)
  - public keys published at a `.well-known` endpoint with rotation support
- Public retrieval:
  - `GET /attestations/{id}`
  - lookup by `(actorUri, objectUri, contentHash, module, policyVersion)`

Acceptance criteria:
- A third-party client can verify an attestation offline using published public keys.
- Attestations do not apply to quote posts unless `(actorUri, objectUri, contentHash)` matches exactly.
- Cached attestations are served without re-running scans (cache hit path).

---

### M6 — Moderation scanning provider (instance-private, admin-only)

Deliverables:
- Implement `lesser.host` as a provider for Lesser’s existing AI moderation handles:
  - endpoints for text/media scanning
  - structured outputs (categories + confidence + highlights)
- Instance configuration:
  - default trigger: on reports (when enabled)
  - optional triggers: always, links/media only, virality thresholds
- Audit:
  - every scan request is logged with requester identity, policy version, and outputs retained per policy

Acceptance criteria:
- A registered instance can request a moderation scan and receive a deterministic, schema-valid response.
- No moderation results are published as federation-wide attestations by default.
- “On reports” trigger path is supported end-to-end (API + queue + worker + result persistence).

---

### M7 — Claim verification (optional, expensive)

Deliverables:
- Evidence policy v1 (to be co-designed):
  - retrieval constraints, source ranking, citation requirements
- Claim extraction + classification (checkable vs opinion)
- Multi-model option support (model set configured per job)
- Public attestation output includes per-claim verdicts with citations and confidence

Reference:
- Evidence policy v1: `docs/evidence-policy-v1.md`

Acceptance criteria:
- Claim checks produce structured outputs with citations for each claim.
- Costs are clearly estimated up-front and charged only on cache miss.
- Results are marked “inconclusive” when evidence is insufficient (no forced certainty).

---

### M8 — Tip host registry administration (on-chain integration)

Deliverables:
- Tip system smart contract (non-upgradeable):
  - `contracts/contracts/TipSplitter.sol` (host registry + token allowlist + pull-payment tip splitting)
  - Events-only tip history (no on-chain tip storage)
- Host Admin workflows:
  - external registration: wallet signature + DNS TXT/HTTPS well-known proof
  - higher assurance updates: require both DNS + HTTPS proof for wallet/fee increases
- Domain normalization:
  - normalize domains for on-chain `hostId` hashing in one canonical way (shared with Lesser and `lesser.host` portal)
  - store both the raw user input and normalized form; never depend on DNS case/Unicode quirks
- On-chain operations:
  - propose and execute `registerHost/updateHost/setHostActive` (Safe-first)
  - register both `slug.greater.website` and vanity domain hostIds
  - manage token allowlist for supported tokens
- Audit log + reconciliation:
  - store tx hashes, receipts, and host registry state snapshots

Acceptance criteria:
- External domain registration cannot proceed without required proofs.
- For a hosted instance, auto-registration of `slug.greater.website` works idempotently.
- A Safe-based flow exists where `lesser.host` proposes tx payloads and operators execute them.

---

### M9 — Hosted instance provisioning (greater.website “wordpress.com” experience)

Deliverables:
- Define the **Lesser release contract**:
  - GitHub Release asset requirements + manifest
  - stable deployment receipt schema used by `lesser.host`
- Provisioning pipeline (async):
  - create instance record + slug reservation
  - provision/allocate a dedicated AWS account in the org
  - create delegated Route53 hosted zone for `slug.greater.website` (instance account)
  - deploy instance infra (Lesser release) into the instance account
  - configure default domain `slug.greater.website` (via NS delegation under `greater.website`)
  - bootstrap instance credentials + register in `lesser.host`
- Vanity domains:
  - DNS proof required before activation
  - support both:
    - user-managed DNS (copy/paste proof + routing records)
    - Route53-managed DNS (optional) where `lesser.host` can UPSERT proof + routing records automatically
  - once activated, register vanity hostId on-chain and route vanity to instance

Acceptance criteria:
- A new instance can be provisioned end-to-end into a dedicated AWS account with a working `slug.greater.website` endpoint.
- Vanity domain activation requires DNS/HTTPS proof and cannot be flipped without re-proofing.
- Provisioning is observable (job status, logs, and failure recovery path).

---

### M10 — Portal UX + payments (self-serve + operator console)

Deliverables:
- Self-serve portal:
  - create/claim instance slug(s)
  - manage domains (view proof instructions, submit proofs, rotate/disable)
  - configure instance features (previews/safety/attestations/moderation provider)
  - view usage, budgets, and cache hit rates
- Payments:
  - buy credits / set overage payment method (provider-agnostic interface; Stripe-first is fine)
  - invoices/receipts export (even if minimal)
  - “author at publish” discount surfaced in UI and applied to usage ledger
- Operator console:
  - review/approve external instance registrations and vanity domain requests
  - propose on-chain tx payloads (Safe) and record execution results

Acceptance criteria:
- A user can self-serve a hosted instance: create slug → provision → configure services → see usage.
- A user can add a payment method and buy credits; usage ledger reflects credits and discounts.
- Operators can approve/deny registrations and see a complete audit trail for every action.

## Cross-cutting acceptance criteria (apply to all milestones)

- **Security:** SSRF protections for all fetch/render; strict egress allow/deny; no private network access.
- **Idempotency:** publish-triggered calls must be idempotent per `(objectUri, module, policyVersion)`.
- **Non-blocking:** out-of-budget or downstream failure never blocks publish; it yields “not checked / pending”.
- **Observability:** request IDs, structured logs, and metrics for latency, cache hit rate, and budget consumption.
- **Cost controls:** hard caps on render time, bytes, links per post, and concurrency.
