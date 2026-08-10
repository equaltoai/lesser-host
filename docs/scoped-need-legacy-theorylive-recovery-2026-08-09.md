# Scoped Need: Legacy TheoryLive authoritative recovery

## Background

Four active hosted TheoryLive identities predate Body's current registry/content projections. Host retains authoritative identity and hosted-genesis evidence, but the existing public and instance read surfaces do not provide a side-effect-free adoption contract. Silas additionally advertises a missing public registration artifact.

## Driver

Principal-direct coordination request relayed from the Lesser Body steward on 2026-08-09. This is not an advisor brief.

## Problem

Body cannot adopt the four identities without either depending on incomplete public projections, reading private conversation data, replaying Host finalization, or inventing migration provenance. Host also lacks a tenant-bounded inventory for finding other Host-defined identities missing downstream projections.

## Surface affected

Control plane Soul registry instance API, off-chain Soul state reads, hosted-genesis recovery, OpenAPI/contract fixtures, audit logging, rate limiting, integrity tooling, and recovery documentation.

## Lambda affected

`control-plane-api` only.

## Classification

Soul-registry, operational-reliability, tenant-isolation, instance-auth, bug-fix, test-coverage, and docs.

## Gate 1: Host-mission verdict

Yes. Host owns hosted identity, Soul registry state, HostedGenesisSession truth, and instance-authenticated control-plane reads. Body owns its later projections. Lesser owns binding state. No tenant content or general identity-provider scope is absorbed into Host.

## Narrowest-scope proposal

1. Keep the strengthened `soul-integrity-scan-m2` finding for identities that claim a version without version history/current artifact.
2. Add a pure recovery-source selector that validates Slug, domain, registration, agent, conversation, promotion, version, declaration, and S3 checksum bindings.
3. Add two additive InstanceKey-authenticated reads:
   - `GET /api/v1/soul/instance/recovery/agents`: bounded metadata inventory for Host-defined agents owned by the authenticated Slug;
   - `GET /api/v1/soul/instance/recovery/agents/{agentId}`: exact declaration detail plus explicit provenance/integrity classification.
4. Make both reads Soul-business-state side-effect-free: no `convergeHostedGenesisPublished`, finalize, publication,
   version increment, identity/session/version/S3 mutation, or audit content containing declarations. Preserve normal
   `InstanceKey.LastUsedAt` and redacted security-audit telemetry.
5. Treat Silas as `legacy_declarations_only`. Do not fabricate or silently restore an alleged historical registration. A future public artifact requires a separately authorized forward publication/re-attestation with a new honest version.
6. Return Della's and Iris's complete immutable version-chain metadata without flattening or replacing version 2.

## What this need explicitly does not cover

- No direct DynamoDB/S3 repair, reminting, on-chain mutation, Safe-ready payload, or Mint-signer use.
- No changes to agent IDs, local IDs, Slugs, domains, Lesser bindings, or Body tables.
- No transcript/message/provider-output exposure.
- No public recovery endpoint, auth bypass, cross-tenant query result, or raw Instance API key storage/logging.
- No claim that public registration is lossless genesis evidence.
- No historical hash, issued time, signature, registration ID, conversation ID, or version fabrication.
- No automatic Body adoption.

## Success criteria

- The four agents receive deterministic recovery classifications from read-only Host state.
- Exact legacy declaration bytes are returned only to the owning authenticated Slug with a SHA256 explicitly labeled as a migration-read digest, not a historical publication digest.
- Della/Iris version 1 and 2 history remains visible and checksum verified; Mags version 1 remains intact.
- Silas is recoverable for exact declarations without turning his `404` into fabricated history.
- Inventory is bounded, paginated, tenant-scoped, and content-free.
- Repeating reads produces identical semantic output and no identity/promotion/session/conversation/version/S3 writes,
  publication, or Host version changes. InstanceKey last-used and redacted audit telemetry are expected.
- Invalid/revoked keys, cross-tenant agents, mismatched bindings, checksum failures, and oversize payloads fail closed and audit safely.
- Full Go, contract, and Gov-infra rubric gates pass with fresh Evidence.

## Specialist routing

- Governance rubric: not touched; existing Verifiers remain deterministic 0-or-full-points.
- Provisioning/Managed update/Consumer release verification: not touched.
- Soul registry: audited via `evolve-soul-registry`; off-chain only.
- Trust API/CSP/instance-auth: instance-auth audited via `audit-trust-and-safety`; Trust API and CSP unchanged.
- Framework consumption: existing AppTheory/TableTheory patterns are sufficient; no local framework workaround.
- Advisor brief: n/a.

## Consumer impact

- Managed operators: gain an auditable recovery path without state mutation.
- Body: gains an honest source contract for operator-gated adoption and calls it directly through its existing managed `LESSER_HOST_INSTANCE_KEY_ARN` credential path.
- Lesser: has no recovery-read dependency. Existing Lesser-mediated private self-scope authorization remains separate and is not widened into InstanceKey authority.
- Sim: gains integration cases for migration and tenant isolation.
- Public Soul registry readers: no contract change; Silas remains `404` until a separately authorized forward publication.

## Multi-tenant isolation impact

Elevated scrutiny required. Reads must derive the Slug from the authenticated InstanceKey and require domain plus HostedGenesisSession bindings. No cross-tenant inventory or global scan result is exposed.

## On-chain impact

None; off-chain only.

## AGPL posture

No dependency or licensing change. All source, schemas, tests, and runbooks remain AGPL-3.0 in-tree.

## Open questions

1. Does the principal want a later forward publication/re-attestation for Silas after adoption, knowing it must create a new honest version rather than recreate version 1?

Resolved coordination question: Body confirmed on 2026-08-09 that its deployed runtime already resolves the Host InstanceKey through `LESSER_HOST_INSTANCE_KEY_ARN`; the secret is not stored inline. Recovery is therefore direct Body-to-Host and requires no Lesser proxy.
