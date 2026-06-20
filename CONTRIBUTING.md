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

## Operational values (CDK context)

Operational deployment values (account IDs, hosted zone IDs, contract addresses, etc) are intentionally not tracked in
git.

- Copy `cdk/cdk.context.local.json.example` to `cdk/cdk.context.local.json`
- Fill in your real values locally

The CDK apps will automatically merge values from `cdk/cdk.context.local.json` at synth time.

## Secrets

Do not commit secrets (API keys, private keys, webhook secrets, etc). Runtime credentials are loaded from AWS SSM
Parameter Store.
