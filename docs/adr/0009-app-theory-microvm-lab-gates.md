# ADR 0009: AppTheory MicroVM lab gates for hosted genesis

## Status

Accepted for Project 51 M3 implementation PR; lab deploy/run remains operator-owned.

## Context

Host is the first product dogfood for AppTheory v1.14.0 MicroVM contracts in the Soul Genesis flow. AppTheory provides:

- Go `runtime/microvm` controller/session/lifecycle contracts (`create`, `start`, `stop`, `status`, `session`), validators, and registry-backed client primitives.
- `testkit/microvm.FakeClient` for deterministic contract tests.
- CDK constructs `AppTheoryMicrovmNetworkConnector`, `AppTheoryMicrovmImage`, and `AppTheoryMicrovmController`.

Factory routed the missing concrete Go AWS Lambda MicroVM client adapter to AppTheory as `delivery-bcb585616b891657`.

## Decision

Host wires AppTheory MicroVM behind explicit, fail-closed lab gates only:

1. CDK emits no MicroVM resources by default.
2. `hostedGenesisMicrovmLabEnabled=true` is accepted only for `stage=lab` and requires operator-owned VPC/subnet/security-group/image/build-role/artifact context plus a SHA-256 authorizer token digest.
3. CDK uses AppTheory constructs directly for the network connector, image, and controller; Host glue only validates context and packages the controller/authorizer Lambdas.
4. The controller Lambda uses AppTheory `runtime/microvm.NewController`, `ControllerRequest`, contract validators, and a `microvm.Client` boundary.
5. Host's provisional dogfood client delegates to AppTheory's registry client and does not expose raw AWS SDK clients, tokens, or lifecycle bypasses to Host business logic.
6. `HostedGenesisSession` remains Host business/source truth. AppTheory registry/session/cache/lifecycle state is reconstructible execution state only.

## Consequences

- M3 can prove the AppTheory contract shape without a cloud deploy or live mutation.
- A real lab canary remains an operator follow-up after review and context provisioning.
- Any future concrete AWS Lambda MicroVM adapter must either come from AppTheory or replace the provisional Host adapter through a scoped follow-up that preserves the `microvm.Client` boundary.
- Browser/public Host responses must continue to exclude MicroVM endpoint tokens, auth tokens, raw Instance API keys, provider keys, SSM values, wallet signatures, AWS credentials, raw transcripts, and raw lifecycle payloads.
