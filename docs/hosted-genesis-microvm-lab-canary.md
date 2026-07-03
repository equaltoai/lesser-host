# Hosted Genesis AppTheory MicroVM lab canary (M3R)

Project 51 M3R upgrades the Host dogfood path to AppTheory v1.15.0 M16 and retires the Project 51 M3 provisional MicroVM workaround. The path stays behind fail-closed **lab-only** gates.

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
- `hostedGenesisMicrovmAuthorizerTokenSha256` — SHA-256 digest only, never a raw token

The MicroVM image code artifact is built in-repo from `cmd/hosted-genesis-microvm-workload` at synth time and uploaded as a CDK S3 asset; no external `codeArtifactUri` context is required.

The wiring fails closed outside `lab` and fails closed when the digest is not a SHA-256 value.

## Non-deploying canary proof

Run the local, non-deploying contract canary:

```bash
./scripts/hosted-genesis-microvm-canary.sh
```

That script runs deterministic Go tests that exercise AppTheory M16 operations (`run`, `get`, `list`, `suspend`, `resume`, `terminate`, `auth-token`, and `shell-auth-token`) through `runtime/microvm.NewRealController`, deterministic fake providers, and the controller routes registered by `microvm.RegisterControllerRoutes`. It also checks marshaled canary evidence and controller token responses for secret-bearing vocabulary such as bearer tokens, AWS credentials, raw Instance API keys, provider secrets, wallet signatures, raw transcripts, and endpoint tokens.

## Operator lab deploy/run follow-up

This PR does **not** deploy, run a cloud canary, mutate SSM/Secrets, sign transactions, or modify sibling repos. If operators want a real lab canary after review, they must:

1. Provide the lab-only context above from operator-owned infrastructure.
2. Provide only `hostedGenesisMicrovmAuthorizerTokenSha256` in CDK context; keep the raw bearer token out of git, logs, docs, fixtures, and CloudFormation outputs.
3. Run the normal `theory app up --stage lab --execute` path themselves. Do not bypass AppTheory's deploy contract and do not set a CDK deploy timeout.
4. Exercise the controller endpoint with AppTheory M16 controller routes: `POST /microvms`, `GET /microvms`, `GET /microvms/{session_id}`, `POST /microvms/{session_id}/suspend`, `POST /microvms/{session_id}/resume`, `DELETE /microvms/{session_id}`, `POST /microvms/{session_id}/auth-token`, and `POST /microvms/{session_id}/shell-auth-token`.
5. Verify logs contain no MicroVM endpoint tokens, bearer tokens, raw Instance API keys, provider keys, SSM values, wallet signatures, AWS credentials, raw transcripts, or raw lifecycle payloads.

## Truth layering

`HostedGenesisSession` remains Host business/source truth for user-visible status, idempotency, billing, declaration checkpoints, recovery, and publish/finalize gates. The AppTheory MicroVM session registry and `MicroVMLifecycleRef` are execution/cache state only and can be reconstructed from Host state through `microvm.SessionReconstructionHook` / `microvm.NewReconstructingSessionRegistry`.

## Framework adoption status

AppTheory v1.15.0 is the only MicroVM provider/controller implementation used by Host in this lab path. Host now uses `microvm.NewAWSLambdaMicroVMProvider` for the real provider initialization path, deterministic fake providers only in tests, and no Host-local MicroVM provider fork or raw AWS SDK substitute. If AppTheory is missing a future capability, Host must stop and route a framework feedback brief rather than adding a local workaround.
