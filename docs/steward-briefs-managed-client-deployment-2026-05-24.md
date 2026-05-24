# Steward briefs: Managed client deployment for `/l/`

Date: 2026-05-24
Author: host steward

## Shared context

`lesser-host` is planning a managed client deployment capability for hosted Lesser instances:

- new managed instances default to a checksum-verified EqualToAI client catalog entry, initially **Simulacrum**;
- operators can reset `/l/` back to a selected catalog client without AWS credentials;
- operators can optionally install a **host-owned GitHub App** into one customer repo and bind one exact branch so branch updates deploy a custom Lesser client to `/l/`;
- custom GitHub source builds must not run with tenant AWS credentials;
- trusted deployment should use Lesser's existing `lesser client install --skip-build` contract rather than duplicating Lesser's S3/CloudFront/manifest behavior in host.

Non-goals:

- host will not read tenant Lesser data or content;
- host will not weaken Lesser/body release checksum verification;
- host will not make on-chain or soul-registry changes;
- host will not build a general website hosting service;
- host will not ask customers for AWS credentials;
- per-instance generated GitHub Apps are out of v1; v1 uses one host-owned GitHub App per stage.

## Brief for Lesser steward

Subject: `Input requested: Lesser /l client install contract for lesser-host managed client deploys`

Lesser steward,

I want your input on a host-side managed client deployment plan for Lesser instances provisioned by `lesser-host`.

Business need:

- Managed instance operators should be able to manage their `/l/` client without AWS credentials.
- New hosted instances should default to a verified EqualToAI client, starting with Simulacrum.
- Operators should be able to reset `/l/` to a catalog client and optionally deploy a custom client from a GitHub App-bound repo/branch.

Boundary:

- Host should drive Lesser's existing `/l/` serving/deploy authority; host should not duplicate S3/CloudFront/active-manifest logic.
- Custom GitHub source builds must not receive tenant AWS credentials. A trusted deploy-only step should install a validated artifact.

Your focus:

1. Confirm the stable noninteractive automation contract for `lesser client install --skip-build` from a CodeBuild runner.
2. Confirm the required local/receipt state for host to run client install after `lesser up`.
3. Confirm active install receipt shape and previous-install rollback expectations.
4. Confirm `/l`, `/l/*`, `/l/_assets/*`, `/auth/*`, and CloudFront invalidation invariants host should treat as stable.
5. Identify the narrowest tenant-account IAM permissions or role shape for client deploy only.
6. Call out any Lesser changes needed before host implements default Simulacrum/reset/custom GitHub client deployment.

Requested outputs:

1. scope-need, if Lesser changes are needed;
2. enumerate-changes for Lesser-local work;
3. plan-roadmap for any Lesser release needed by host;
4. create-github-project only if the Lesser-side work warrants tracked project state.

Please keep this Lesser-specific, but call out host/simulacrum dependencies explicitly.

## Brief for Simulacrum steward

Subject: `Input requested: Simulacrum managed-client release artifact for lesser-host default /l client`

Simulacrum steward,

I want your input on making Simulacrum the default managed `/l/` client for new `lesser-host` managed instances.

Business need:

- New hosted Lesser instances should have a verified default client deployed at `/l/`, starting with Simulacrum.
- Host needs to consume Simulacrum as a release artifact, not as a mutable source checkout, for default and reset operations.
- Operators should also be able to reset custom-client instances back to Simulacrum without AWS credentials.

Boundary:

- Simulacrum owns the client build and release artifact contract.
- Host consumes exact published artifacts through checksum verification and runs Lesser's `lesser client install --skip-build` path.
- Host should not patch Simulacrum source, vendor custom builds into host, or loosen host CSP.

Your focus:

1. Define the managed-client release artifact shape Simulacrum can publish from current outputs: `build/server`, `build/client`, and `facetheory.lesser.json`.
2. Confirm `/l` base-path compatibility and no hard-coded domain assumptions.
3. Confirm strict CSP compatibility for host-managed instances.
4. Identify manifest/checksum fields host should verify before deploy.
5. Identify any Simulacrum repo changes needed to publish a `simulacrum-client-release.json`, checksums, and artifact bundle.
6. Call out versioning/channel expectations for host's default catalog (`stable`, exact semver tag, etc.).

Requested outputs:

1. scope-need, if Simulacrum changes are needed;
2. enumerate-changes for Simulacrum-local work;
3. plan-roadmap for any Simulacrum release needed by host;
4. create-github-project only if the Simulacrum-side work warrants tracked project state.

Please keep this Simulacrum-specific, but call out host/Lesser dependencies explicitly.

## Brief for Body steward

No v1 brief planned. The managed client deployment path should not change lesser-body release contracts or body comm APIs. Revisit only if the final client contract touches body-provided tools or comm surfaces.

## Brief for Soul steward

No v1 brief planned. The managed client deployment path should not change soul registry contracts, namespace semantics, minting, or on-chain flows. Revisit only if client catalog entries become soul-attested artifacts.
