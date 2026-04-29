# M5 rollout checklist — Codex Security 2026-04-29

This checklist is the operator handoff for issue [#210](https://github.com/equaltoai/lesser-host/issues/210). It is
intentionally explicit because M5 includes actions that `implement-milestone` does not perform: host stage deploys,
on-chain deployments, and managed-instance canaries.

## Hard rules

- Do not run live deploys without explicit operator authorization.
- Do not set timeouts on CDK/AppTheory deploy commands.
- Do not delete CloudFormation stacks, Lambda versions, retained DynamoDB/S3/SSM/Secrets Manager resources, Route53 zones,
  or CloudFront distributions during rollout.
- Do not deploy on-chain contracts to mainnet single-signer.
- Do not skip Slither, Hardhat tests, solhint, Sepolia evidence, or Safe-ready review for contract bytecode changes.
- Do not bypass managed release checksum verification for lesser, lesser-body, or Lesser M9 assets.
- Do not loosen trust-API instance auth or CSP to make rollout pass.

## Pre-lab verification

Record the exact commit deployed to lab:

- Commit: `____________________________`
- PR set included: #212, #213, #214, #215, #216, #222, and this M5 PR.
- Local/CI validation evidence:
  - [ ] `go test ./...`
  - [ ] `go vet ./...`
  - [ ] `gofmt -l .` empty
  - [ ] web lint/typecheck/tests/build where web changed
  - [ ] `cd cdk && npm run synth`
  - [ ] `bash gov-infra/verifiers/gov-verify-rubric.sh`
  - [ ] Slither / solhint / Hardhat evidence for contract changes

## Lab deploy

Command (operator-run, no timeout):

```bash
AWS_PROFILE=<lab-profile> theory app up --stage lab
```

Capture:

- Start time (UTC): `____________________________`
- End time (UTC): `____________________________`
- CloudFormation stack events reviewed: `yes / no`
- Deploy output artifact/log location: `____________________________`

## Lab smoke checks

### Control plane and portal

- [ ] Wallet login challenge/login succeeds for an operator test account.
- [ ] Portal customer wallet login works.
- [ ] Portal instance list/details routes load without 5xx.
- [ ] Portal managed-update version fields accept intended blank/config-only actions and reject invalid versions.
- [ ] Portal self-service soul provisioning remains policy-gated.

### Public soul and trust surfaces

- [ ] Public soul lookup/search/version endpoints return bounded pages and usable cursors.
- [ ] Public avatar enrichment does not trigger unbounded chain/RPC fanout.
- [ ] Dispute and validation public responses are redacted or auth-gated as expected.
- [ ] Trust API public routes still serve `/.well-known/jwks.json` and `/attestations*`.
- [ ] Instance-authenticated trust routes still require bearer token hashes; raw keys are not logged.

### Comm, mailbox, and provider webhooks

- [ ] Valid SES/email ingress sample with passing verdict is accepted.
- [ ] Failing SES verdict sample is rejected or avoids sender enrichment.
- [ ] Valid Telnyx/Migadu webhook samples are accepted.
- [ ] Invalid/replayed webhook samples are rejected and do not debit budgets or mutate delivery state.
- [ ] Mailbox duplicate `Message-ID` sample cannot overwrite content.
- [ ] Outbound voice status callback bills the originating soul-owned channel exactly once.

### Web/CSP

- [ ] Web app loads from the first-party origin.
- [ ] Browser console has no CSP violations for normal portal paths.
- [ ] Markdown content with unsafe HTML/URLs is sanitized, not rendered as executable content.

### Managed provisioning / update dry-run

- [ ] Managed update dry-run/test slug uses pinned lesser/body versions.
- [ ] Release checksum mismatch test still fails closed.
- [ ] Instance-key secret names include stage isolation.
- [ ] Trust verification after update uses authenticated instance key flow.
- [ ] Lesser M9 is not consumed unless exact M9 assets have passed certification/readiness.

## On-chain rollout gate

For contract bytecode changes from M2:

- [ ] Hardhat tests passed for the exact artifact.
- [ ] solhint completed; warnings/errors dispositioned.
- [ ] Slither completed; findings fixed or documented.
- [ ] Sepolia deploy executed.
- [ ] Sepolia contract source/bytecode verified.
- [ ] Sepolia behavior smoke tested.
- [ ] Mainnet Safe-ready payload prepared only after Sepolia evidence.
- [ ] Mainnet execution explicitly authorized by Safe signers.
- [ ] `docs/deployments/` updated after any authorized deployment.

## Lab soak

Minimum soak: one business day unless Aron explicitly authorizes a compressed hotfix soak.

Monitor during soak:

- CloudWatch error rate per Lambda entrypoint.
- CloudFront 4xx/5xx by path family.
- `control-plane-api` errors for `/api/v1/soul/*`, `/api/v1/portal/*`, `/auth/*`, and `/setup/*`.
- `trust-api` instance-auth failures and 5xx rate.
- `email-ingress` SES verdict rejects and parse failures.
- `comm-worker` queue depth and DLQ.
- Provider webhook rejects by reason and replay/idempotency counters where available.
- `provision-worker` SQS depth, CodeBuild failures, and managed-update failures.
- `ai-worker` queue depth, LLM token/cost error rates, and fallback behavior.
- `soul-reputation-worker` failures and suspended-agent reputation output.
- eth_rpc latency/error rates and public read request latency.

Lab soak result:

- Start time (UTC): `____________________________`
- End time (UTC): `____________________________`
- Blocking regressions found: `yes / no`
- Regression issue/PR links: `____________________________`
- Operator who accepted lab soak: `____________________________`

## Live authorization checklist

Before live deploy:

- [ ] Lab soak accepted.
- [ ] M5 PR merged and CI green.
- [ ] Sepolia/on-chain evidence captured if contract deployment is in scope.
- [ ] Managed canary target selected if provisioning/update rollout is in scope.
- [ ] Customer/operator communications prepared from release notes.
- [ ] Rollback owner available.
- [ ] Live deploy explicitly authorized by Aron/operator.

Live command (operator-run, no timeout):

```bash
AWS_PROFILE=<live-profile> theory app up --stage live
```

## Post-live monitoring

Watch for at least the first hour, then through the next business-day window:

- CloudFront 4xx/5xx by route family.
- Lambda error and duration p95/p99 by entrypoint.
- DynamoDB throttles/conditional failures that indicate unexpected contention.
- SQS queue age and DLQ depth for provision, comm, preview, safety, render, and AI queues.
- Provider webhook authentication reject rates.
- SES inbound verdict reject rates.
- Public soul read/search/version latency and error rates.
- trust-api instance-auth failure rates.
- Billing/usage ledger idempotency errors.
- Managed provisioning/update CodeBuild failures.
- On-chain receipt validation rejects and successful validated operations.

Post-live result:

- Live deploy start (UTC): `____________________________`
- Live deploy end (UTC): `____________________________`
- First-hour monitor accepted by: `____________________________`
- Next-business-day monitor accepted by: `____________________________`
- Follow-up issues opened: `____________________________`
