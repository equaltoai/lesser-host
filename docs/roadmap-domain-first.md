# Roadmap: Domain-first URLs + Provisioned Configuration + Managed Updates

This roadmap focuses on one non-negotiable outcome:

- **Every URL used by clients or Lesser instances MUST be a first‑party domain** (`*.lesser.host`), never a Lambda Function URL or AWS-generated hostname.
- **All required configuration MUST be applied during provisioning and updates**, not via manual after-the-fact env tweaks.

Status: proposal (written 2026-02-16)

## Current problems (what’s failing and why)

### 1) Trust / attestations / previews return 503 or “unavailable”

Root cause: managed instances are being deployed without the Lesser env vars that wire trust to `lesser.host`:

- `LESSER_HOST_URL`
- `LESSER_HOST_INSTANCE_KEY_ARN` (or inline `LESSER_HOST_INSTANCE_KEY`)
- optional `LESSER_HOST_ATTESTATIONS_URL`

Additionally, the `lesser.host` CloudFront distribution only routes:

- `/.well-known/*` and `/attestations*` → `trust-api`
- `/api/*` → `control-plane-api`

So authenticated trust paths like `/api/v1/previews` only work on the *trust Lambda URL*, which then leaks into config and breaks the “domain-only” requirement.

### 2) Translation is “not enabled”

Root cause: instances are deployed without `TRANSLATION_ENABLED=true` (and translation also requires AWS Translate IAM permissions in the instance account; tracked as a Lesser-side change).

### 3) Updates keep “regressing”

Root cause: `lesser up` is idempotent, but our managed deploy runner is not **configuration-idempotent**:

- config is not stored as first-class instance state in `lesser-host`
- runner does not apply the same config on subsequent runs

## Design principles (constraints we must keep)

1) **Single-origin portal remains intact**
   - keep strict CSP (`connect-src 'self'`)
   - avoid CORS by serving portal + APIs behind the same CloudFront origin

2) **Domain-first, stage-aware**
   - `lab` → `https://lab.lesser.host`
   - `staging` → `https://staging.lesser.host`
   - `live` → `https://lesser.host`

3) **Provisioning and updates are the source of truth**
   - the system must be able to re-run a deploy at any time and produce the same functional integration without manual steps

## Target URL model (what Lesser instances should use)

Per control-plane stage (`STAGE`):

- **Canonical base URL** (portal + APIs): `https://<stage>.lesser.host` (or `https://lesser.host` for live)
- **Trust API (instance-scoped, authenticated)**: same origin paths
  - `POST /api/v1/previews`
  - `POST /api/v1/renders`
  - `POST /api/v1/publish/jobs`
  - `POST /api/v1/ai/*`
  - `POST /api/v1/budget/debit`
- **Public trust (cacheable)**: same origin paths
  - `GET /.well-known/jwks.json`
  - `GET /attestations` and `GET /attestations/{id}`

This keeps everything on one first-party origin while still allowing separate backends via CloudFront path routing.

## Milestone 0 — Fix CloudFront routing so domains work for *all* trust endpoints (1–2 days)

### Work

In `lesser-host` CloudFront distribution (`cdk/lib/lesser-host-stack.ts`), add **explicit trust behaviors** for the trust API paths so they route to `trust-api` instead of `control-plane-api`:

- `api/v1/previews*`
- `api/v1/renders*`
- `api/v1/publish/jobs*`
- `api/v1/ai/*`
- `api/v1/budget/debit`

Keep these routed to `control-plane-api`:

- `api/v1/portal/*`
- `api/v1/operators/*`
- `auth/*`, `setup/*`

Also ensure we have a **single canonical output** for public consumption:

- “Public base URL”: `https://<stage>.lesser.host` (or `https://lesser.host` for live)

Lambda Function URLs may remain as *internal origins* for CloudFront, but must be treated as debug-only and must not be used by:

- Lesser instance config/env
- portal responses intended for customers
- documentation intended for integrators

### Acceptance criteria

- `curl -sS -I https://lab.lesser.host/api/v1/previews` returns `405` (allow: POST) from `trust-api` (not 404 from `control-plane-api`).
- Public JWKS and attestations continue to work on-domain:
  - `https://lab.lesser.host/.well-known/jwks.json`
  - `https://lab.lesser.host/attestations?...`

## Milestone 1 — Make trust/translation configuration part of managed provisioning (1–2 weeks)

### Work: store configuration as instance state

Extend the instance model (or add a dedicated “deployment config” record) to persist:

- `lesser_version` (already exists on `ProvisionJob`, but should be visible as instance state)
- trust integration inputs (derived, stage-aware):
  - `lesser_host_base_url` (computed: `https://<stage>.lesser.host`)
  - `lesser_host_attestations_url` (computed; usually same as base)
- trust integration secret reference (instance-specific):
  - `lesser_host_instance_key_secret_arn` (stored once; reused on updates; rotation tracked)
- feature flags:
  - `translation_enabled` (and any other Lesser env-driven flags we adopt)

### Work: automatically provision the instance key secret

Stop relying on a manual “create instance key then hand-copy it” flow for managed instances.

Proposed safe approach:

1) Control plane generates an Instance Key (plaintext) and stores only `sha256(key)` in `lesser-host` state (existing `InstanceKey` model).
2) Provisioning worker assumes role into the target instance account and writes the plaintext key into **AWS Secrets Manager** in that account.
3) The resulting secret ARN is stored on the instance record and passed into `lesser up` as `LESSER_HOST_INSTANCE_KEY_ARN`.

### Work: ensure the runner always applies config

Update the provisioning worker / CodeBuild runner contract so every managed deploy sets (at minimum):

- `LESSER_HOST_URL=https://<stage>.lesser.host`
- `LESSER_HOST_ATTESTATIONS_URL=https://<stage>.lesser.host` (optional but explicit)
- `LESSER_HOST_INSTANCE_KEY_ARN=<instance-account-secret-arn>`
- `TRANSLATION_ENABLED=true|false`

Important: the runner must be able to re-run with the same inputs and re-apply the same config (idempotent).

### Acceptance criteria

- A newly provisioned managed instance can immediately call trust endpoints through the instance proxy without any manual steps.
- Instance `/api/v2/instance` reflects expected flags (trust + translation).
- A re-run of “update” (re-running `lesser up`) does not drop any trust/translation configuration.

## Milestone 2 — Add “Update instance” jobs (portal-triggered) (2–4 weeks)

Goal: customers (and operators) can apply config changes and/or bump Lesser versions from the portal, without shell access.

### Work

1) Add an update job model (similar to `ProvisionJob` but separate semantics):
   - job type: `update`
   - desired Lesser version (optional)
   - desired config snapshot (trust + translation + other flags)
   - status/step + CodeBuild run id

2) Portal API:
   - `POST /api/v1/portal/instances/{slug}/updates` (create update job)
   - `GET /api/v1/portal/instances/{slug}/updates` (list + status)

3) Worker:
   - trigger CodeBuild runner with the stored instance config
   - re-run `lesser up` in the existing instance account (no account vending)
   - verify post-deploy invariants (see below)

4) UI:
   - “Apply configuration” button (for env/config changes)
   - “Update Lesser version” flow (select tag or “latest”)
   - progress + logs deep link + audit trail

### Post-deploy verification checklist (automated)

After each update job completes:

- Verify the instance reports the config we intended:
  - `GET /api/v2/instance` includes expected `configuration.translation.enabled`
- Verify trust wiring is live (best-effort, non-destructive):
  - call a trust endpoint that requires instance auth and expects a structured error if inputs are missing (not 503)
- Record pass/fail signals into the update job record so the portal can surface “trust configured / not configured”.

## Milestone 3 — Backfill existing instances and update Sim (1–2 days once M0–M2 exist)

### Sim target state

For the `Sim` managed instance account:

- Instance config references domain-only endpoints:
  - `LESSER_HOST_URL=https://lab.lesser.host` (or `https://lesser.host` for live)
  - `LESSER_HOST_INSTANCE_KEY_ARN=<sim secret arn>`
  - `TRANSLATION_ENABLED=true` (if enabled for Sim)

### Work

1) Run an update job for Sim (using the new “Update instance” path).
2) If the Sim account does not yet have an instance key secret created by the platform, create it and persist the ARN.
3) Validate the instance from the portal (and optionally with operator curl checks).

### Notes / dependencies outside lesser-host

Translation also requires AWS Translate IAM permissions in the instance account’s Lambda role(s), which is a Lesser-side CDK/IAM change.

## Milestone 4 — Hardening and guardrails (ongoing)

- Make domain endpoints the only values shown/returned in portal customer responses.
- Add an alert on trust proxy 503 rates (per instance slug).
- Add explicit “trust integration health” status to the portal instance detail page.
- Add rotation flow for instance keys (portal-driven, safe rollout with dual-key overlap if needed).

## Appendix: Related Lesser-side fixes (tracked outside this repo)

These are not `lesser-host` changes, but they impact the “it works end-to-end” outcome:

- **WebSockets (GraphQL subscriptions):** API Gateway WebSocket does not echo `Sec-WebSocket-Protocol`, which breaks clients that require `graphql-transport-ws`. Choose one:
  - adjust the client to not require subprotocol echo, or
  - move subscriptions to a WS origin that supports subprotocol negotiation.
- **GraphQL search regression:** ensure GraphQL resolvers never return nil elements for non-null list fields (`[Actor!]!`, etc).
- **Translation IAM:** add AWS Translate permissions when translation is enabled.
- **Stage-aware defaults:** avoid stage-incorrect defaults for `LESSER_HOST_URL`; provisioning should set explicitly, but sane defaults reduce footguns.

