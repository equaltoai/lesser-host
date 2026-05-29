---
name: provision-managed-instance
description: Use when a change touches the managed-instance provisioning pipeline, the managed-update flow, or the consumer-release-verification discipline. Walks per-slug AWS account provisioning, DNS delegation, CodeBuild runner coordination, checksum-based release verification, step-level recovery, and tenant-isolation preservation. Provisioning and its release-verification gate are the supply-chain frontier.
---

# Provision a managed instance

host's provisioning worker (`cmd/provision-worker/`) invokes CodeBuild runners that deploy lesser and body into a per-slug AWS account. Before deploy, release artifacts are checksum-verified. After deploy, managed-update flows keep the instance current. This pipeline is the supply-chain frontier — changes here have outsized blast radius, and the release-verification step is non-negotiable.

This skill walks every provisioning / managed-update / release-verification change.

## The pipeline surfaces (memorize)

- **`cmd/provision-worker/`** — SQS-driven orchestrator Lambda
- **`internal/controlplane/provisioning/`** (or equivalent) — provisioning logic
- **Per-slug AWS account setup** — dedicated account under AWS Organizations per slug
- **Delegated Route53 zone** — `slug.greater.website` subdomain per slug
- **CodeBuild runner** — invoked by provisioning worker; executes `./lesser up` in the tenant account using verified release artifacts
- **Lesser release verification** — `docs/lesser-release-contract.md` defines the ingest shape; `scripts/managed-release-certification/*` + `scripts/managed-release-readiness/*` are the verification tooling
- **Body release verification** — `docs/lesser-body-release-contract.md` + same script family
- **Managed-update flow** — `POST /api/v1/portal/instances/{slug}/updates` with step-level error recovery and rollback
- **Recovery runbooks** — `docs/managed-update-recovery.md`, `docs/provisioning-recovery-plan.md`, `docs/recovery.md`
- **Retention sweep** — `docs/retention-sweep.md`, `cmd/render-worker/` (or equivalent)

## When this skill runs

Invoke when:

- A change modifies the provisioning flow (new step, modified step, error handling, recovery logic)
- A change modifies how the provisioning worker invokes CodeBuild
- A change modifies the per-slug AWS account setup (IAM, DNS delegation, SSM parameter seeding)
- A change modifies release-verification scripts (`scripts/managed-release-certification/*`, `scripts/managed-release-readiness/*`)
- A change modifies the release-contract documents (`docs/lesser-release-contract.md`, `docs/lesser-body-release-contract.md`)
- A change modifies the managed-update flow
- A change modifies recovery procedures or tooling
- A change adds new managed-instance types / variants
- A change affects tenant-isolation guarantees in the provisioning pipeline
- `scope-need` or `investigate-issue` flags a change as provisioning / managed-update / release-verification-touching

## Preconditions

- **The change is described concretely.** "Improve provisioning" is too vague; "add a step to the provisioning worker that verifies the tenant's Route53 delegation is propagated before invoking CodeBuild, with a 60-second initial check and exponential backoff, aborting with alert if not propagated after 15 minutes" is concrete.
- **MCP tools healthy**, `memory_recent` first — provisioning edge cases accumulate per-slug.
- **The affected tenant(s) named** if applicable. Changes to live provisioning pipelines may affect specific slugs; name them.

## The six-dimension walk

### Dimension 1: Per-slug AWS account setup

For changes affecting tenant account provisioning:

- **AWS Organizations integration** — how host creates / federates the per-slug account
- **IAM roles in the tenant account** — what roles host assumes, what the CodeBuild runner uses, what lesser/body use post-deploy
- **Tenant-isolation integrity** — no credentials cross tenants; no cross-tenant queries
- **SSM parameter seeding** — what parameters the tenant account needs before deploy (e.g. `LESSER_API_BASE_URL`, `LESSER_HOST_INSTANCE_KEY`, stage-specific config)
- **Secrets Manager entries** — what secrets the tenant account needs (JWT secret, comm-API instance key, etc.)
- **Idempotency** — re-running provisioning for an existing slug produces same state, not duplicates

### Dimension 2: DNS delegation

For changes affecting `slug.greater.website` delegation:

- **Parent zone (`greater.website`) management** — zone is in host's account; NS records delegate to tenant's child zone
- **Child zone in tenant account** — tenant's Route53 hosts the delegated zone
- **ACM certificate provisioning** — per-stage ACM certificates for the tenant's subdomains
- **Propagation timing** — DNS changes take time to propagate; flows that depend on fresh delegation must tolerate the delay
- **Delegation cleanup** — if a slug is ever decommissioned, child-zone / parent-NS cleanup is part of the lifecycle (never automatic without explicit authorization)

### Dimension 3: Consumer release verification (the supply-chain frontier)

For every provisioning attempt:

- **Release manifest fetch** — download `lesser-release.json` / `lesser-body-release.json` from the GitHub Release
- **Asset download** — Lambda bundles, CDK synthesis artifacts, any other deployable
- **Checksum verification** — every asset's SHA256 matches the manifest
- **Mismatch abort** — any mismatch halts provisioning and alerts; provisioning does not proceed
- **Release-certification scripts** (`scripts/managed-release-certification/*`) encode the verification; changes here are governance-adjacent
- **Readiness scripts** (`scripts/managed-release-readiness/*`) check prerequisites (e.g. is a release marked as "provisioning-ready"?)

**Never skip verification for "trusted" commits.** The release artifact is the trusted payload; everything else is supply-chain attack surface.

Changes to this dimension require elevated scrutiny. A bug in release verification could let unverified artifacts through to managed deploys.

### Dimension 4: CodeBuild runner invocation

For changes to how provisioning invokes CodeBuild:

- **Build spec** — the commands the runner executes (`./lesser up`, `cdk deploy`, etc.)
- **Environment** — AWS credentials the runner uses (tenant-account role), environment variables, secrets references
- **Resource limits** — timeout, compute type, memory
- **Artifact output** — deploy receipts, evidence artifacts, failure diagnostics
- **Failure handling** — what does the provisioning worker do on CodeBuild failure (abort, retry, rollback, alert)

### Dimension 5: Managed-update flow and step-level recovery

For changes to the managed-update flow (`POST /api/v1/portal/instances/{slug}/updates`):

- **Update steps** — enumerate each step (e.g. verify release → pre-update checks → deploy → post-update checks)
- **Step-level error recovery** — each step can fail; what does recovery look like per step?
- **Rollback discipline** — which steps are rollbackable; which are not; what's the rollback procedure for each
- **Progress visibility** — the customer portal shows update progress; what's the contract for progress events
- **Idempotency** — re-triggering an update that's already in-flight or completed must not corrupt state

### Dimension 6: Tenant-isolation preservation

For every provisioning / managed-update change:

- **Does this change preserve the tenant boundary?** Default answer: yes.
- **Any cross-tenant reads / writes in provisioning?** If yes, refuse unless explicitly authorized.
- **Any shared credentials across tenants?** If yes, refuse.
- **Any cross-tenant logs?** Aggregation in host's plane should not include tenant content.
- **Audit logging per provisioning event** — who triggered, which slug, what steps executed, evidence emitted.

## The audit output

```markdown
## Provisioning / managed-update / release-verification audit: <change name>

### Proposed change
<concrete description>

### Pipeline surfaces affected
<per-slug AWS account / DNS delegation / release verification / CodeBuild runner / managed-update flow / recovery>

### Per-slug AWS account setup (if applicable)
- AWS Organizations integration change: <...>
- IAM roles: <...>
- Tenant-isolation preservation: <confirmed>
- SSM / Secrets Manager seeding: <...>
- Idempotency: <confirmed>

### DNS delegation (if applicable)
- Parent zone change: <...>
- Child zone change: <...>
- ACM certificate flow: <...>
- Propagation timing accommodation: <...>

### Consumer release verification (if applicable — elevated scrutiny)
- Release manifest fetch flow: <...>
- Checksum verification logic: <...>
- Mismatch abort behavior: <preserved>
- Release-certification scripts changed: <list>
- Readiness-scripts changed: <list>
- End-to-end test against a known-good release: <confirmed>
- End-to-end test against a known-bad / checksum-mismatched release: <confirmed — must abort>

### CodeBuild runner (if applicable)
- Build spec change: <...>
- Environment / credentials: <...>
- Resource limits: <...>
- Failure handling: <...>

### Managed-update flow (if applicable)
- Steps added / modified / removed: <...>
- Step-level recovery: <...>
- Rollback: <per step>
- Progress visibility: <...>
- Idempotency: <confirmed>

### Tenant-isolation impact
- Preserves isolation: <confirmed — default>
- Cross-tenant reads / writes: <none — default; if present, refuse>
- Shared credentials: <none — default>
- Audit logging: <per-event events emit with slug, action, evidence>

### Consumer-of-the-pipeline impact
- Existing managed instances: <no impact / migration needed>
- Future provisioning: <new flow>
- Operator portal UX: <no change / update>

### Test coverage
- Unit tests: <added / existing>
- Integration tests against test slug: <added / existing>
- Dry-run provisioning: <added / existing>
- End-to-end test with known-bad release: <added — must abort>
- Recovery / rollback tests: <added / existing>

### Proposed next skill
<enumerate-changes if audit clean; audit-trust-and-safety if the change touches trust-API / attestation / instance-auth; scope-need if audit surfaces scope growth; investigate-issue if audit reveals an existing bug>
```

## Refusal cases

- **"Skip checksum verification for this trusted lesser release."** Refuse. Never. Trusted commits are exactly the shape of supply-chain compromise.
- **"Allow provisioning to proceed on checksum mismatch; surface an alert but don't block."** Refuse. Blocking is the gate.
- **"Share a Secrets Manager entry across tenants for cost."** Refuse. Tenant isolation is absolute.
- **"Read tenant data into host's plane for a debugging view."** Refuse.
- **"Skip the Route53 propagation wait; CodeBuild will retry if DNS isn't ready."** Evaluate; retry + backoff may be acceptable but a timeout-with-alert is preferable to a silent provisioning failure.
- **"Manually provision this customer; the worker has a bug."** Manual provisioning outside the documented flow is refused unless there's a specific incident-response exception with Aron authorization — and even then, the flow bug is fixed before other slugs use the broken path.
- **"Skip recovery planning for this step; it's rare."** Refuse. Every step needs a recovery story.
- **"Keep provisioning logs including raw tenant data in host's CloudWatch."** Refuse. Logs sanitize tenant content.
- **"Add a cross-tenant reporting query for billing aggregation."** Route through explicit authorization; cross-tenant queries are refused by default.
- **"Allow an old lesser release (no longer certified) to provision because a legacy customer wants it."** Refuse. Certification is the gate.

## Persist

Append when the walk surfaces something worth remembering — a per-slug AWS-account-setup subtlety, a DNS-propagation-timing observation, a release-verification edge case (especially one that caught a near-miss), a managed-update recovery pattern, a tenant-isolation finding worth documenting. Routine audits aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- **Audit clean, no cross-boundary concerns** — invoke `enumerate-changes`.
- **Audit clean, trust-API surfaces touched** (e.g. provisioning seeds instance-auth keys) — also invoke `audit-trust-and-safety`.
- **Audit clean, soul-registry surfaces touched** (e.g. provisioning mints a soul on-chain) — also invoke `evolve-soul-registry`.
- **Audit surfaces governance-rubric need** (e.g. a new verifier that validates release-certification evidence) — invoke `maintain-governance-rubric`.
- **Audit surfaces scope growth** — revisit `scope-need`.
- **Audit reveals an existing pipeline bug** — route through `investigate-issue`.
- **Audit surfaces framework awkwardness** — `coordinate-framework-feedback`.
- **Audit surfaces sibling-repo coordination** (lesser / body release-contract changes) — coordinate via their stewards through the user before proceeding.
