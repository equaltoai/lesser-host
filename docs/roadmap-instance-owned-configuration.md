# Roadmap: Instance-Owned Configuration (Stop Env-Var Drift) + Domain-First Managed Provisioning

Status: proposal (written 2026-02-17)

This roadmap is about one outcome: **managed instances never lose critical configuration** (trust/attestation wiring,
translation, tipping, AI feature flags) because of a redeploy, a local CDK run, or a runner mismatch.

It complements `docs/roadmap-domain-first.md` (routing + single-origin), and assumes Lesser implements persisted
instance config per `lesser/docs/planning/lesser-instance-owned-configuration-roadmap.md`.

## Non-negotiables

1) **Domains only**
   - No AWS-generated hostnames (Lambda Function URLs, `amazonaws.com`, etc.) in any config returned to clients or
     written into Lesser instance state.
2) **Provisioning + updates are the source of truth**
   - Every managed deploy and update must re-apply the same config deterministically.
3) **Instance-owned config**
   - `lesser-host` provides **managed defaults**, but operators must be able to override config **on the instance**
     (and those overrides must not be clobbered by managed updates).
4) **Well-known names**
   - Use stable, versioned, well-known config keys and schemas; no stringly-typed one-offs.

## Problem (why we keep regressing)

Today the managed runner primarily wires integrations via **Lambda environment variables** during `lesser up`.

In AWS, this is inherently brittle:

- CloudFormation treats `Environment.Variables` as authoritative desired state.
- Any deploy path that does not include a key deletes it.
- A later update from a different runner (or a developer machine) can silently drop env vars and disable features.

We need managed provisioning/update to configure instances in a way that **survives redeploys** and is **merge-safe**.

## Target design

### Lesser owns the effective config; lesser-host supplies managed defaults

Lesser persists config in its own DynamoDB state under well-known keys (conceptually):

- `PK="INSTANCE#CONFIG"`
- `SK="TRUST_CONFIG" | "TRANSLATION_CONFIG" | "TIPS_CONFIG" | "AI_CONFIG" | ...`

Each record supports two layers:

- `managed` (written by managed provisioning/update)
- `override` (written by the instance operator/admin)

Effective config resolution is: `override` → `managed` → built-in defaults.

### lesser-host uses provisioning input (not env vars) as the durable config contract

`lesser-host` already runs `lesser up ... --provisioning-input "$PROVISION_INPUT"` for managed deploys.

The durable contract becomes:

- `lesser-host` constructs a **full managed config snapshot** for the instance
- passes it as `--provisioning-input` on every deploy/update
- Lesser writes/merges it into instance config as `managed` defaults (never clearing fields on omission)

Env vars remain optional legacy bootstrap inputs only.

### Secrets: always ARNs; never plaintext

For instance-key auth to trust services:

- `lesser-host` creates/ensures an instance-scoped key secret in the **instance account** (AWS Secrets Manager).
- The provisioning input contains the **secret ARN** only.

## Roadmap

### H1 — Persist a managed config snapshot as instance state in lesser-host (1–3 days)

**Goal:** `lesser-host` has a single durable “source of truth” for what config a managed instance should have.

**Work**
- Extend the instance metadata record (or add a dedicated per-instance config record) in the `lesser-host` state table
  to store:
  - `trust.managed.base_url` (computed; domain-only, stage-aware)
  - `trust.managed.attestations_url` (computed; usually same as base)
  - `trust.managed.instance_key_secret_arn` (created once; reused on updates)
  - `translation.managed.enabled`
  - `tips.managed.*` (enabled + chain/contract identifiers if applicable)
  - `ai.managed.*` (feature flags/defaults as adopted by Lesser)
- Add merge semantics:
  - If a field is missing, compute/set it.
  - If a field exists, reuse it on updates (do not blank or rotate implicitly).

**Acceptance criteria**
- An “update instance” job can run without any ad-hoc operator input because it can fully reconstruct config.

---

### H2 — Ensure the instance key secret exists (1–2 days)

**Goal:** managed instances never depend on manual key copy/paste.

**Work**
- During provisioning (and during “repair” if missing), assume role into the instance account and:
  - create (or upsert) the trust instance key in AWS Secrets Manager
  - record the ARN in the lesser-host instance config snapshot
- Rotation is explicit (separate job/flow); do not rotate during routine updates.

**Acceptance criteria**
- Fresh provision produces a stable `instance_key_secret_arn`.
- Re-running an update does not change the ARN unless rotation was requested.

---

### H3 — Make the runner pass a complete provisioning input on every deploy/update (2–4 days)

**Goal:** no managed deploy relies on CloudFormation env vars to stay intact.

**Work**
- Build `PROVISION_INPUT` from the stored instance config snapshot (H1) and pass it to `lesser up` on every run.
- Include only **domain-only** URLs:
  - stage-aware canonical base: `https://<stage>.lesser.host` (or `https://lesser.host` for live)
- Add guardrails that fail the job early if:
  - any URL host matches AWS-generated patterns (Lambda Function URLs, `amazonaws.com`, etc.)
  - required values are missing (e.g., secret ARN for trust-enabled instances)
- Keep env-var injection only as a temporary backward-compatibility path while older Lesser releases exist; treat it as
  “legacy mode” and ensure the runner still passes provisioning-input.

**Acceptance criteria**
- Two successive “update” runs produce identical config on the instance (no drift).
- A developer re-deploy in the instance account does not disable managed features (because config is now persisted).

---

### H4 — Portal-triggered “Apply configuration” + “Update Lesser” jobs (1–2 weeks)

**Goal:** configuration and version updates are routine portal actions, not shell scripts.

**Work**
- Add/update API endpoints + UI to:
  - show the instance’s stored managed config snapshot (and last applied job)
  - trigger an “Apply configuration” update job (no version bump)
  - trigger an “Update Lesser” job (to latest release)
- Jobs always reuse the stored config snapshot unless the operator changes it explicitly.

**Acceptance criteria**
- Portal can re-apply config to a broken instance without any manual runner intervention.

---

### H5 — Post-deploy verification gates (2–4 days)

**Goal:** every managed update reports whether trust/translation/tips/AI are actually usable.

**Work**
- After `lesser up` completes, run best-effort checks and persist results on the job record:
  - `GET https://<instance-domain>/api/v2/instance` and verify flags match intended config
  - a trust-proxy “sanity” call that should return a structured response (not a generic 503)
  - a translation smoke call when enabled (verifies IAM + runtime wiring)
  - tipping enabled check (contract/chain metadata present in instance config)
- Surface these as green/red indicators in the portal.

**Acceptance criteria**
- An update job is not “ok” unless it has a recorded verification result for each enabled feature area.

---

### H6 — Backfill + Sim repair (same day once H1–H5 exist)

**Goal:** migrate existing managed instances to the new durable config path.

**Work**
- For each managed instance:
  - populate its managed config snapshot in lesser-host (H1)
  - ensure instance key secret exists (H2)
  - run “Apply configuration” (H4) to write managed defaults into the instance config table
- For Sim specifically:
  - confirm all trust/translation/tips/AI flags are present in `/api/v2/instance`
  - confirm no AWS-generated URLs appear anywhere in instance metadata or API responses

**Acceptance criteria**
- A subsequent local deploy in the instance account does not disable trust/translation/tips/AI.

## Execution order (across repos)

1) **Lesser first:** implement persisted instance config + merge-safe managed defaults (see
   `lesser/docs/planning/lesser-instance-owned-configuration-roadmap.md`).
2) **lesser-host next:** implement H1–H5 to persist and re-apply managed config snapshots on every job.
3) **Then migrate environments:** run H6 backfill/update jobs (Sim first), and stop treating Lambda env vars as the
   durable integration mechanism.

