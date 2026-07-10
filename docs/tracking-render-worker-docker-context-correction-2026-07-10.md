# Tracking — Render-worker Docker asset context correction

Last updated: 2026-07-10
Owner: host steward + operator
Stage focus: corrective PR and `lab` first; no further `live` rollout before the gates below

## Purpose

Track the bounded correction for a Render-worker Docker build-context exposure discovered while validating the
Hosted Genesis Phase 0 live rollout. This correction is a prerequisite to further rollout work in
`docs/roadmap-hosted-offchain-reads-and-mainnet-soul-2026-07-09.md`; it does not activate the Soul registry, change an
on-chain contract, or authorize any cloud mutation by itself.

## Incident assessment

- The Render-worker image asset used the repository root with a sparse CDK exclusion list. During the affected
  deployment, operator-local ignored files and generated outputs entered local CDK `AssetStaging` and were sent to the
  local BuildKit build context.
- The broad build-stage copy locally materialized ignored credential/configuration material in generated asset copies
  and the BuildKit intermediate-layer/cache surface. The known CDK staging copies were removed after discovery;
  BuildKit cache cleanup remains operator-controlled.
- Inspection of the final deployed ECR image found only the expected runtime artifacts and no operator-local
  credential/configuration files. No external disclosure has been proven. That final-image result does not erase the
  local staging/cache exposure or remove the need for credential hygiene.
- No raw credential value, operator-local context value, or tenant content belongs in this tracker or the corrective
  PR.

## Locked correction

- Treat the repo-root Render-worker Docker context as **default-deny** and allow only `go.mod`, `go.sum`, the
  Render-worker build sources/Dockerfile, and required non-test `internal/**/*.go` sources.
- Make CDK use Docker ignore semantics explicitly so `.dockerignore` is the governing asset-context allowlist.
- Replace broad `COPY . .` behavior with explicit source-directory copies. The Dockerfile remains a second containment
  boundary rather than relying on the CDK asset filter alone.
- Keep the final runtime image minimal; do not add operator-local configuration, credentials, tests, Gov-infra
  artifacts, repository metadata, or generated CDK outputs.

## Acceptance and Evidence

Local corrective preflight on 2026-07-10 established the implementation target:

- default-deny staging reduced the context from approximately 989 MB to 3.47 MB;
- the staged file inventory contained only the required allowlisted build inputs;
- changes to representative ignored/operator-local files did not change the Docker asset hash;
- changes to required Go inputs did change the asset hash; and
- the corrected Docker image built successfully.

The PR is not accepted for rollout until all of the following are true:

- [ ] Focused CDK regression tests prove required-input inclusion, sensitive/generated-input exclusion, and stable
      asset hashing for ignored-file changes.
- [ ] `cd cdk && npm test` and a clean `lab` synth pass.
- [ ] The canonical Gov-infra verifier passes and emits fresh QUA-2 Evidence.
- [ ] Review confirms the built image contains only expected runtime artifacts and the diff does not change Soul,
      tenant isolation, Consumer release verification, CSP single-origin, or on-chain behavior.

## Operator-controlled cleanup and rollout order

Cache deletion and credential rotation are destructive/security-sensitive operator actions, not PR automation. Before
the next deployment, the operator must prune the affected local BuildKit cache and rotate any credential that was
present in the exposed local context; values and rotation receipts remain outside Git.

Rollout order is mandatory:

1. Review and merge the corrective PR through `staging` with the existing required checks.
2. Complete the operator-controlled cache cleanup and credential rotation.
3. Deploy the reviewed correction to `lab` through `theory app up --stage lab --execute`; never set a deployment
   timeout.
4. Inspect the synthesized/staged context and final image, then complete the required `lab` canary and soak.
5. Restore the separately reviewed pre-activation live Soul context before any later live deployment; do not bundle
   mainnet activation into this correction.
6. Obtain fresh explicit authorization for a `live` deploy and the bounded `TheoryLive` acceptance canary.
7. Resume roadmap Phase 1 only after the independent Phase 0 rollout gate is accepted.

## Governance scope

This is an intentional THR-5 coverage clarification using the existing QUA-2 CDK test/evidence path. It changes no
Gov-infra Pack, rubric points, Verifier command, threshold, or Evidence output path. Generated Evidence remains the
output of `bash gov-infra/verifiers/gov-verify-rubric.sh` and must not be hand-edited.
