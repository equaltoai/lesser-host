# Contributing

Thanks for contributing to `lesser-host`.

## Branching and releases

Feature branches are opened against `staging`, not `main`. The `staging` branch is the integration gate and requires the existing Gov-infra rubric check plus the seven parallel CI jobs. Promotion from `staging` to `main` is operator-owned; `main` accepts PRs only from `staging` and releases are manual `v*` tags cut from `main`. See `docs/release-branching.md`.

Git branch `staging` is not a deploy stage. Deploy stages remain `lab -> live` through `theory app up/down --stage <lab|live> --execute`.

## Development quickstart

- Go tests: `go test ./...`
- Web: `cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build`
- CDK: `cd cdk && npm ci && npm run synth`
- Contracts: `cd contracts && npm ci && npm test`

## Operator-local deploy configuration

Use the AppTheory deploy contract for deploys. `app-theory/app.json` is the source of truth for the standard
lesser-host web custom-domain binding used by `theory app up/down`; CDK reads
`lesserHost.webDomain.<stage>.{rootDomain,hostedZoneId,hostedZoneName}` from that file and fails closed if the active
stage entry is missing, placeholder-like, or invalid. Do not move web domain values into `cdk/cdk.json`,
`cdk/cdk.context.local.json`, environment variables, CLI context overrides, or a sidecar file.

`cdk/cdk.context.local.json` remains the operator-local home for non-domain CDK context overrides such as contract
addresses, managed provisioning account/bootstrap values, and local operator placeholders:

- Copy `cdk/cdk.context.local.json.example` to `cdk/cdk.context.local.json`
- Fill in your real non-domain values locally

The CDK app automatically merges values from `cdk/cdk.context.local.json` at synth time for those non-domain settings.
After local non-domain context is prepared, deploy through the AppTheory contract:

```bash
theory app up --aws-profile my-profile --stage lab --execute
```

## Secrets

Do not commit secrets (API keys, private keys, webhook secrets, etc). Runtime credentials are loaded from AWS SSM
Parameter Store.
