# ADR 0009: AppTheory MicroVM lab gates for hosted genesis

## Status

Accepted for Project 51 M3R implementation PR; later amended by the 2026-07-07 hosted-genesis MicroVM registry-boundary corrective. Lab/live deploys remain operator-owned.

## Context

Host is dogfooding the AppTheory Lambda MicroVM contract foundation in the Soul Genesis lab path. Project 51 M3 originally carried a Host-local provisional adapter because AppTheory v1.14.0 did not yet expose the concrete Go AWS Lambda MicroVM provider needed by Host. AppTheory v1.15.0 (tag `8c18ad95ea7b55ec0c13bf5a515d0a8d04738e36`) now provides the M16 runtime/CDK surface that closes that gap:

- Go `runtime/microvm` real-controller, provider, session registry, and reconstruction APIs.
- Canonical operation vocabulary and routes: `run`, `get`, `list`, `suspend`, `resume`, `terminate`, `auth-token`, and `shell-auth-token`.
- `microvm.NewAWSLambdaMicroVMProvider` for the non-forked AWS Lambda MicroVM provider path.
- `microvm.SessionReconstructionHook` / `microvm.NewReconstructingSessionRegistry` so product business truth can reconstruct controller execution/cache state.
- CDK `AppTheoryMicrovmController` wiring for M16 routes, ingress/egress/shell-ingress connector refs, fail-closed authorization, registry table, and controller IAM.

## Decision

Host wires AppTheory MicroVM behind explicit, fail-closed lab gates only:

1. CDK emits no MicroVM resources by default.
2. `hostedGenesisMicrovmLabEnabled=true` is accepted only for `stage=lab` and requires operator-owned VPC/subnet/security-group/image/build-role/artifact context plus a SHA-256 authorizer token digest.
3. CDK uses AppTheory constructs directly for network connectors, image, and controller; Host glue only validates context, supplies lab-only auth/state-table environment, and packages the controller/authorizer Lambdas.
4. The controller Lambda uses AppTheory `microvm.NewRealController`, `microvm.RegisterControllerRoutes`, `microvm.Provider`, `microvm.NewAWSLambdaMicroVMProvider`, and `microvm.NewReconstructingSessionRegistry`.
5. Host's provisional dogfood adapter is retired; Host must not replace it with a framework fork, raw AWS SDK workaround, or local substitute for AppTheory MicroVM features.
6. `HostedGenesisSession` remains Host business/source truth. Registry/session/cache/lifecycle state is reconstructible execution state only and is persisted, when needed, through Host's repo-owned `HostedGenesisMicroVMExecution` model and `store.NewHostedGenesisMicroVMRegistry` adapter. Host deployed code must not initialize AppTheory's generic `runtimemicrovm.SessionRegistryRecord` TableTheory model or call `NewTableTheorySessionRegistry`.
7. Token-producing controller responses remain internal/controller-scoped and tests assert only sanitized metadata; browser/public Host surfaces must not expose MicroVM endpoint tokens, auth tokens, shell tokens, raw Instance API keys, provider keys, SSM values, wallet signatures, AWS credentials, raw transcripts, or raw lifecycle payloads.

## Consequences

- M3R proves Host can consume AppTheory v1.15.0 M16 through the real framework APIs without a cloud deploy or live mutation.
- A real lab canary remains an operator follow-up after review and context provisioning.
- Future MicroVM capability gaps must be source-proven and routed to AppTheory instead of patched locally in Host.
- Any change to expose the controller beyond the lab-only guarded path requires a new governance/security review.

## 2026-07-07 amendment: Host-owned registry cache boundary

A lab rebuild exposed that directly registering AppTheory's generic `runtimemicrovm.SessionRegistryRecord` with Host's
TableTheory state table fails Host's platform boundary: the generic model uses snake_case attribute tags and directly
exposes table-row shape where Host requires repo-owned camelCase models and semantic repository methods. The corrective
decision is to keep AppTheory's controller/provider/routes, but adapt its `SessionRegistry` interface through Host's
`HostedGenesisMicroVMExecution` cache model. `HostedGenesisSession` remains source truth, and
`microvm.NewReconstructingSessionRegistry` remains the reconstruction mechanism for absent/stale cache.

This amendment is local to Host's registry persistence boundary. It is not a framework fork, not a raw AWS provider
replacement, not an on-chain change, and not permission to coordinate framework feedback in this milestone.
