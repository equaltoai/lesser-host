# Hosted Genesis AppTheory MicroVM lab canary (M3)

Project 51 M3 wires the first Host dogfood of AppTheory v1.14.0 MicroVM contracts behind fail-closed **lab-only** gates.

## What is enabled by default

Nothing. `hostedGenesisMicrovmLabEnabled` is absent/false by default, so `cdk synth` emits no MicroVM controller, image, network connector, controller Lambda, authorizer Lambda, MicroVM session registry, or controller endpoint.

When explicitly enabled for `stage=lab`, CDK requires all operator-owned context values before it synthesizes the AppTheory constructs:

- `hostedGenesisMicrovmVpcId`
- `hostedGenesisMicrovmPrivateSubnetId`
- `hostedGenesisMicrovmPrivateSubnetAvailabilityZone`
- `hostedGenesisMicrovmSecurityGroupId`
- `hostedGenesisMicrovmBaseImageArn`
- `hostedGenesisMicrovmBaseImageVersion`
- `hostedGenesisMicrovmBuildRoleArn`
- `hostedGenesisMicrovmCodeArtifactUri`
- `hostedGenesisMicrovmAuthorizerTokenSha256` — SHA-256 digest only, never a raw token

The wiring fails closed outside `lab` and fails closed when the digest is not a SHA-256 value.

## Non-deploying canary proof

Run the local, non-deploying contract canary:

```bash
./scripts/hosted-genesis-microvm-canary.sh
```

That script runs the deterministic Go tests that exercise AppTheory `create`, `start`, `status`, `session`, and `stop` through `runtime/microvm.NewController` and `testkit/microvm.FakeClient`. It also checks the marshaled canary evidence for secret-bearing vocabulary such as bearer tokens, AWS credentials, raw Instance API keys, provider secrets, wallet signatures, raw transcripts, and endpoint tokens.

## Operator lab deploy/run follow-up

This PR does **not** deploy, run a cloud canary, mutate SSM/Secrets, sign transactions, or modify sibling repos. If operators want a real lab canary after review, they must:

1. Provide the lab-only context above from operator-owned infrastructure.
2. Provide only `hostedGenesisMicrovmAuthorizerTokenSha256` in CDK context; keep the raw bearer token out of git, logs, docs, fixtures, and CloudFormation outputs.
3. Run the normal `theory app up --stage lab --execute` path themselves. Do not bypass AppTheory's deploy contract and do not set a CDK deploy timeout.
4. Exercise the controller endpoint with AppTheory controller envelopes for `create`, `start`, `status`, `session`, and `stop` using the raw token only in the `Authorization` header.
5. Verify logs contain no MicroVM endpoint tokens, bearer tokens, raw Instance API keys, provider keys, SSM values, wallet signatures, AWS credentials, raw transcripts, or raw lifecycle payloads.

## Truth layering

`HostedGenesisSession` remains Host business/source truth for user-visible status, idempotency, billing, declaration checkpoints, recovery, and publish/finalize gates. The AppTheory MicroVM session registry and `MicroVMLifecycleRef` are execution/cache state only and can be reconstructed from Host state.

## Adapter status

AppTheory v1.14.0 provides the runtime/controller/session/lifecycle contracts, CDK constructs, and registry-backed client primitives, but not a concrete Go AWS Lambda MicroVM lifecycle adapter. Host's M3 `ProvisionalDogfoodMicroVMClient` stays behind `microvm.Client`, delegates to AppTheory's registry client for non-deploying contract proof, and references Factory's routed AppTheory feedback `delivery-bcb585616b891657` for upstream adapter guidance.
